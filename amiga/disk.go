package amiga

import (
	"encoding/binary"
	"fmt"
	"os"
)

const (
	ADFSize         = 901120 // standard ADF file size
	SectorsPerTrack = 11
	BytesPerSector  = 512
	TracksPerDisk   = 160                              // 80 cylinders * 2 sides
	TrackSize       = SectorsPerTrack * BytesPerSector // 5632 bytes raw data per track

	// A DD drive spins at 300 RPM (200 ms per revolution) with 2 µs MFM
	// bit cells: 100,000 cells = 6,250 words per track, one word every
	// 32 µs = 227 CPU cycles (two per scanline).
	DiskCyclesPerWord    = 227
	MFMTrackWords        = 6250
	DiskRevolutionCycles = DiskCyclesPerWord * MFMTrackWords // 1,418,750
)

// DiskDrive is the floppy disk controller (Paula's disk DMA and the drive
// signals on the CIAs) with up to four drives. DF0: is always connected and
// embedded, so a.Disk.InsertDisk, a.Disk.motor and so on refer to it;
// DF(n) reaches the others.
type DiskDrive struct {
	*Drive // DF0:

	amiga  *Amiga
	drives [4]*Drive // nil = no drive connected

	side int // /SIDE: 0 = lower head, 1 = upper (shared by all drives)

	// Write statistics: sectors committed to the image, and sectors in
	// write DMA that were dropped (bad checksum or format) or whose header
	// named a different track than the head was on.
	SectorsWritten, BadSectorWrites, TrackMismatches int

	// SaveErr is the last error writing a track back to an image file.
	SaveErr error

	// DF0: select and status-read counters (diagnostics).
	SelectCount         int
	DeselectCount       int
	StatusReads         int
	StatusSelectedTrue  int
	StatusSelectedFalse int

	// DMA registers
	dskpt        uint32 // DMA destination pointer
	dsklen       uint16 // DSKLEN register
	dsklenWrites int    // double-write counter
	dsksync      uint16 // sync word (typically $4489)
	adkcon       uint16 // disk control bits

	// Status
	wordequal bool // set when sync word found during DMA

	// DMA timing: data is transferred immediately (like the blitter), but
	// DSKSYN fires when the sync word passes the head (syncCycles, 0 = none
	// pending) and DSKBLK when the last word has (completionCycles).
	syncCycles       int
	completionCycles int
}

// NewDiskDrive creates a new disk drive attached to the given Amiga.
func NewDiskDrive(a *Amiga) *DiskDrive {
	df0 := &Drive{Unit: 0}
	return &DiskDrive{
		Drive:   df0,
		drives:  [4]*Drive{df0},
		amiga:   a,
		dsksync: 0x4489,
	}
}

// ConnectDrive attaches drive n (1-3) if it is not already, and returns it.
// Kickstart finds external drives at boot by reading their IDs.
func (d *DiskDrive) ConnectDrive(n int) *Drive {
	if d.drives[n] == nil {
		d.drives[n] = &Drive{Unit: n}
	}
	return d.drives[n]
}

// DF returns drive n (DFn:, 0-3), or nil if none is connected.
func (d *DiskDrive) DF(n int) *Drive {
	if n < 0 || n > 3 {
		return nil
	}
	return d.drives[n]
}

// SetWriteProtected write-protects (or not) every connected drive.
func (d *DiskDrive) SetWriteProtected(on bool) {
	for _, dr := range d.drives {
		if dr != nil {
			dr.WriteProtected = on
		}
	}
}

// active is the drive the controller talks to: the lowest-numbered
// selected drive, or nil.
func (d *DiskDrive) active() *Drive {
	for _, dr := range d.drives {
		if dr != nil && dr.selected {
			return dr
		}
	}
	return nil
}

// pendingDisk is an image queued by SwapDisk.
type pendingDisk struct {
	data []byte
	path string
}

// Disk swap timing. Swapping instantly confuses AmigaDOS: trackdisk only
// learns the drive is empty when it polls (with the motor off, it steps the
// head and reads /DSKCHANGE), and if the new disk is already in by then it
// never reports the old one gone. A head step while the drive is busy (motor
// on) is a data seek, not a poll; worse, the next seek with the new disk in
// clears /DSKCHANGE before trackdisk looks, so DOS goes on reading the new
// disk as the old volume ("checksum error on disk block ..."). So SwapDisk
// ejects, waits for a step with the motor off (trackdisk's poll), lets that
// poll finish, then inserts. If nothing polls, it inserts once the drive
// has been idle for swapIdleWait, or after swapMaxWait regardless.
const (
	swapAfterPoll = 7_093_790 / 2  // 0.5 s after the empty-drive poll
	swapIdleWait  = 7_093_790 * 3  // insert after 3 s if the motor is off
	swapMaxWait   = 7_093_790 * 10 // insert after 10 s regardless
)

// HandleCIABWrite processes CIA-B PRB writes for the drive signals.
// PRB: 7=/MTR, 6=/SEL3, 5=/SEL2, 4=/SEL1, 3=/SEL0, 2=/SIDE, 1=/DIR, 0=/STEP.
// Each drive latches /MTR when it is selected (falling edge of its /SELn),
// and only selected drives respond to /STEP.
func (d *DiskDrive) HandleCIABWrite(old, new byte) {
	for n, dr := range d.drives {
		if dr == nil {
			continue
		}
		was := dr.selected
		dr.selected = new&(0x08<<n) == 0
		if n == 0 {
			if dr.selected && !was {
				d.SelectCount++
			} else if !dr.selected && was {
				d.DeselectCount++
			}
		}
		if dr.selected && !was {
			on := new&0x80 == 0
			switch {
			case dr.motor && !on:
				dr.idBit = 0 // stopping the motor resets the ID shift register
			case !on:
				dr.idBit++ // each select with the motor off clocks an ID bit
			}
			dr.motor = on
		}
	}

	if new&0x04 == 0 {
		d.side = 1 // upper head
	} else {
		d.side = 0
	}

	if old&0x01 != 0 && new&0x01 == 0 { // /STEP falling edge
		in := new&0x02 == 0 // /DIR low: towards the centre
		for _, dr := range d.drives {
			if dr != nil && dr.selected {
				dr.step(in)
			}
		}
	}
}

// ReadStatus returns CIA-A PRA with the drive status bits: selected drives
// pull /DSKRDY (5), /DSKTRACK0 (4), /DSKPROT (3) and /DSKCHANGE (2) low
// (the drives' outputs are wired together).
func (d *DiskDrive) ReadStatus(regVal byte) byte {
	val := regVal | 0x3C // inactive (high) unless a selected drive pulls low
	d.StatusReads++
	if d.drives[0].selected {
		d.StatusSelectedTrue++
	} else {
		d.StatusSelectedFalse++
	}
	for _, dr := range d.drives {
		if dr != nil && dr.selected {
			val &^= dr.status()
		}
	}
	return val
}

// WriteDSKPT sets the high or low word of the DMA pointer.
func (d *DiskDrive) WriteDSKPT(high bool, val uint16) {
	if high {
		d.dskpt = (d.dskpt & 0x0000FFFF) | uint32(val)<<16
	} else {
		d.dskpt = (d.dskpt & 0xFFFF0000) | uint32(val)
	}
}

// WriteDSKLEN implements the double-write protocol for DSKLEN.
// DMA only starts when two consecutive writes have bit 14 (DMA enable) set.
func (d *DiskDrive) WriteDSKLEN(val uint16) {
	d.dsklen = val

	if val&0x8000 != 0 {
		// DMA enable bit set
		d.dsklenWrites++
		if d.dsklenWrites >= 2 {
			d.startDMA()
		}
	} else {
		// DMA enable bit clear: reset counter, stop any pending DMA
		d.dsklenWrites = 0
	}
}

// WriteDSKSYNC sets the sync word register.
func (d *DiskDrive) WriteDSKSYNC(val uint16) {
	d.dsksync = val
}

// WriteADKCON handles set/clear writes to ADKCON.
func (d *DiskDrive) WriteADKCON(val uint16) {
	if val&0x8000 != 0 {
		d.adkcon |= val & 0x7FFF
	} else {
		d.adkcon &^= val & 0x7FFF
	}
}

// ReadADKCONR returns the current ADKCON value.
func (d *DiskDrive) ReadADKCONR() uint16 {
	return d.adkcon
}

// ReadDSKBYTR returns the DSKBYTR register value.
// Bit 15: DSKBYT (always 1 when motor on)
// Bit 12: WORDEQUAL (sync word matched)
func (d *DiskDrive) ReadDSKBYTR() uint16 {
	var val uint16
	if dr := d.active(); dr != nil && dr.motor {
		val |= 0x8000 // DSKBYT
	}
	if d.wordequal {
		val |= 0x1000 // WORDEQUAL
	}
	return val
}

// startDMA performs a disk DMA transfer. Data lands in chip RAM immediately,
// but completion interrupts are deferred (see TickCycles).
func (d *DiskDrive) startDMA() {
	dr := d.active()
	if dr == nil || dr.adf == nil || !dr.motor {
		return
	}

	d.dsklenWrites = 0
	wordCount := int(d.dsklen & 0x3FFF)
	if wordCount == 0 {
		wordCount = 0x4000
	}
	isWrite := d.dsklen&0x4000 != 0

	if isWrite {
		// Writes start at the head position without waiting for a sync
		// word, and take one word time per word. A write-protected drive
		// ignores the data but the DMA still runs to completion.
		if !dr.WriteProtected {
			d.writeTrack(dr, wordCount)
		}
		d.completionCycles = wordCount * DiskCyclesPerWord
		return
	}

	track := dr.cylinder*2 + d.side
	mfm := dr.encodeMFMTrack(track)
	if mfm == nil {
		return
	}
	word := func(i int) uint16 {
		i = i % MFMTrackWords * 2
		return uint16(mfm[i])<<8 | uint16(mfm[i+1])
	}

	// The transfer starts at whatever word is under the head now. With
	// WORDSYNC (ADKCON bit 10) it waits for the next sync word and starts
	// with the word after it; if the track has none, it never starts.
	pos := d.headWord()
	wait := 0
	if d.adkcon&0x0400 != 0 {
		wait = -1
		for k := 0; k < MFMTrackWords; k++ {
			if word(pos+k) == d.dsksync {
				wait = k + 1
				break
			}
		}
		if wait < 0 {
			return
		}
		d.wordequal = true
		d.syncCycles = wait * DiskCyclesPerWord
	}

	// Copy words to chip RAM, wrapping around the track.
	buf := make([]byte, 2)
	for i := 0; i < wordCount; i++ {
		w := word(pos + wait + i)
		buf[0], buf[1] = byte(w>>8), byte(w)
		d.amiga.ChipRAM.Write(int(d.dskpt)+2*i, buf)
	}
	d.completionCycles = (wait + wordCount) * DiskCyclesPerWord
}

// writeTrack decodes the MFM sectors in a write DMA buffer of words words
// at DSKPT and stores them in the image on the track under the head, then
// saves that track to the image file if there is one.
func (d *DiskDrive) writeTrack(dr *Drive, words int) {
	buf := make([]byte, 2*words)
	for i := 0; i < len(buf); i += 2 {
		d.amiga.ChipRAM.Read(int(d.dskpt)+i, buf[i:i+2])
	}
	track := dr.cylinder*2 + d.side
	n := 0
	for _, sec := range decodeMFMSectors(buf) {
		if !sec.ok || sec.sector >= SectorsPerTrack {
			d.BadSectorWrites++
			continue
		}
		if sec.track != track {
			d.TrackMismatches++
		}
		copy(dr.adf[track*TrackSize+sec.sector*BytesPerSector:], sec.data[:])
		n++
	}
	d.SectorsWritten += n
	if n > 0 && dr.path != "" {
		d.SaveErr = dr.saveTrack(track)
		if d.SaveErr != nil {
			fmt.Fprintf(os.Stderr, "goamiga: saving track %d to %s: %v\n", track, dr.path, d.SaveErr)
		}
	}
}

// saveTrack writes one track of the image back to its file.
func (dr *Drive) saveTrack(track int) error {
	f, err := os.OpenFile(dr.path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteAt(dr.adf[track*TrackSize:(track+1)*TrackSize], int64(track*TrackSize))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// mfmSector is one AmigaDOS sector decoded from MFM.
type mfmSector struct {
	track, sector int
	data          [BytesPerSector]byte
	ok            bool // format byte $FF and both checksums match
}

// AmigaDOS sector layout after the sync words, in bytes: info (odd, even
// longs), 16-byte label (odd block, even block), header checksum, data
// checksum, then 512 data bytes as 128 odd longs followed by 128 even longs.
const (
	mfmSectorInfo    = 0
	mfmSectorHdrSum  = 40
	mfmSectorDataSum = 48
	mfmSectorData    = 56
	mfmSectorLen     = mfmSectorData + 2*BytesPerSector // 1080
)

// decodeMFMSectors finds every sector in an MFM stream (a run of $4489
// sync words followed by a sector) and decodes it.
func decodeMFMSectors(mfm []byte) []mfmSector {
	word := func(i int) uint16 { return binary.BigEndian.Uint16(mfm[i:]) }
	var out []mfmSector
	for i := 0; i+2 <= len(mfm); i += 2 {
		if word(i) != 0x4489 {
			continue
		}
		j := i
		for j+2 <= len(mfm) && word(j) == 0x4489 {
			j += 2
		}
		if j+mfmSectorLen > len(mfm) {
			break
		}
		long := func(off int) uint32 { return binary.BigEndian.Uint32(mfm[j+off:]) }
		pair := func(off, stride int) uint32 { return mfmDecodeLong(long(off), long(off+stride)) }

		info := pair(mfmSectorInfo, 4)
		sec := mfmSector{track: int(info >> 16 & 0xFF), sector: int(info >> 8 & 0xFF)}
		hdrOK := computeMFMChecksum(mfm[j:j+mfmSectorHdrSum]) == pair(mfmSectorHdrSum, 4)
		data := mfm[j+mfmSectorData : j+mfmSectorLen]
		dataOK := computeMFMChecksum(data) == pair(mfmSectorDataSum, 4)
		for k := 0; k < BytesPerSector/4; k++ {
			binary.BigEndian.PutUint32(sec.data[4*k:], pair(mfmSectorData+4*k, BytesPerSector))
		}
		sec.ok = info>>24 == 0xFF && hdrOK && dataOK
		out = append(out, sec)
		i = j + mfmSectorLen - 2
	}
	return out
}

// mfmDecodeLong joins the odd-bit and even-bit MFM longs of one data long.
func mfmDecodeLong(odd, even uint32) uint32 {
	return (odd&0x55555555)<<1 | even&0x55555555
}

// headWord is the index of the MFM word under the head; word 0 is at the
// index hole. The disk is taken to spin continuously.
func (d *DiskDrive) headWord() int {
	return int(d.amiga.cycles / DiskCyclesPerWord % MFMTrackWords)
}

// spinning reports whether the drive is feeding the disk signals (index,
// data) to the controller: selected, motor on, disk in.
func (d *DiskDrive) spinning() bool {
	dr := d.active()
	return dr != nil && dr.motor && dr.adf != nil
}

// TickCycles advances disk DMA timing and the index pulse. Called from
// Amiga.Step() before the beam moves, so d.amiga.cycles is the start of
// the elapsed period.
func (d *DiskDrive) TickCycles(cpuCycles int) {
	if d.syncCycles > 0 {
		d.syncCycles -= cpuCycles
		if d.syncCycles <= 0 {
			d.syncCycles = 0
			d.amiga.Custom.SetIntreq(1 << 12) // DSKSYN
		}
	}
	if d.completionCycles > 0 {
		d.completionCycles -= cpuCycles
		if d.completionCycles <= 0 {
			d.completionCycles = 0
			d.amiga.Custom.SetIntreq(1 << 1) // DSKBLK
		}
	}
	for _, dr := range d.drives {
		if dr != nil {
			dr.tickPending(cpuCycles)
		}
	}
	// Index pulse once per revolution on CIA-B FLG.
	if d.spinning() {
		now := d.amiga.cycles
		if (now+uint64(cpuCycles))/DiskRevolutionCycles != now/DiskRevolutionCycles {
			d.amiga.CIAB.SetFLG()
		}
	}
}

// CyclesToNextEvent returns the CPU cycles until the next disk interrupt
// source (sync, block done, an index pulse CIA-B will signal) or a pending
// disk insertion, or -1.
func (d *DiskDrive) CyclesToNextEvent() int {
	next := -1
	consider := func(c int) {
		if c > 0 && (next < 0 || c < next) {
			next = c
		}
	}
	consider(d.syncCycles)
	consider(d.completionCycles)
	for _, dr := range d.drives {
		if dr != nil && dr.pending != nil {
			consider(dr.pendingWait)
			consider(swapMaxWait - dr.pendingElapsed)
			if !dr.motor {
				consider(swapIdleWait - dr.pendingElapsed)
			}
		}
	}
	if d.spinning() && d.amiga.CIAB.icrMask&0x10 != 0 {
		consider(DiskRevolutionCycles - int(d.amiga.cycles%DiskRevolutionCycles))
	}
	return next
}

// encodeMFMTrack encodes one raw track from ADF into AmigaDOS MFM format.
// AmigaDOS MFM uses odd/even bit splitting: for each longword, the odd-position
// data bits (31,29,...,1) go into one MFM longword and the even-position data
// bits (30,28,...,0) go into another.
// Header (info + labels): INTERLEAVED — odd then even for each source longword.
// Data (512 bytes): BLOCK-SPLIT — all 128 odd longs first, then all 128 even longs.
func (dr *Drive) encodeMFMTrack(track int) []byte {
	if track < 0 || track >= TracksPerDisk {
		return nil
	}

	trackOffset := track * TrackSize
	rawTrack := dr.adf[trackOffset : trackOffset+TrackSize]

	// Allocate MFM buffer: each sector ~1088 words, 11 sectors + gaps
	mfm := make([]byte, 0, 13000)

	for sec := 0; sec < SectorsPerTrack; sec++ {
		// Gap: 2 words of $AAAA
		mfm = appendWord(mfm, 0xAAAA)
		mfm = appendWord(mfm, 0xAAAA)

		// Sync words: $4489 $4489
		mfm = appendWord(mfm, 0x4489)
		mfm = appendWord(mfm, 0x4489)

		// --- Header block (info + 4 label longs) in INTERLEAVED format ---
		// Info longword: format=$FF, track, sector, sectors-to-gap
		info := uint32(0xFF)<<24 | uint32(track)<<16 | uint32(sec)<<8 | uint32(SectorsPerTrack-sec)
		headerLongs := [5]uint32{info, 0, 0, 0, 0} // info + 4 zero label longs

		// Interleaved: odd then even for each source longword
		headerStart := len(mfm)
		for _, v := range headerLongs {
			mfm = appendLong(mfm, mfmOdd(v))
			mfm = appendLong(mfm, mfmEven(v))
		}

		// Header checksum: XOR all longwords in header block, masked to data bits
		hdrChk := computeMFMChecksum(mfm[headerStart:])
		mfm = appendLong(mfm, mfmOdd(hdrChk))
		mfm = appendLong(mfm, mfmEven(hdrChk))

		// --- Sector data: 512 bytes = 128 longwords, block-split format ---
		secData := rawTrack[sec*BytesPerSector : (sec+1)*BytesPerSector]

		// Build the data block in a temp buffer so we can compute checksum
		dataBlock := make([]byte, 0, 1024)
		// All odd halves first
		for i := 0; i < 128; i++ {
			off := i * 4
			v := uint32(secData[off])<<24 | uint32(secData[off+1])<<16 |
				uint32(secData[off+2])<<8 | uint32(secData[off+3])
			dataBlock = appendLong(dataBlock, mfmOdd(v))
		}
		// Then all even halves
		for i := 0; i < 128; i++ {
			off := i * 4
			v := uint32(secData[off])<<24 | uint32(secData[off+1])<<16 |
				uint32(secData[off+2])<<8 | uint32(secData[off+3])
			dataBlock = appendLong(dataBlock, mfmEven(v))
		}

		// Data checksum (comes BEFORE data in the format)
		dataChk := computeMFMChecksum(dataBlock)
		mfm = appendLong(mfm, mfmOdd(dataChk))
		mfm = appendLong(mfm, mfmEven(dataChk))

		// Data block
		mfm = append(mfm, dataBlock...)
	}

	// Gap to the end of the revolution (11 sectors fill 5,984 words).
	for len(mfm) < MFMTrackWords*2 {
		mfm = appendWord(mfm, 0xAAAA)
	}
	return mfm
}

// appendWord appends a 16-bit word in big-endian to the buffer.
func appendWord(buf []byte, w uint16) []byte {
	return append(buf, byte(w>>8), byte(w))
}

// appendLong appends a 32-bit longword in big-endian to the buffer.
func appendLong(buf []byte, v uint32) []byte {
	return append(buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// mfmOdd extracts the odd-position data bits from a longword.
// In AmigaDOS MFM, odd bits are at positions 31,29,...,1 of the original value.
// The MFM encoding shifts right by 1 and masks with 0x55555555.
func mfmOdd(val uint32) uint32 {
	return (val >> 1) & 0x55555555
}

// mfmEven extracts the even-position data bits from a longword.
// Even bits are at positions 30,28,...,0 of the original value.
func mfmEven(val uint32) uint32 {
	return val & 0x55555555
}

// computeMFMChecksum XORs all longwords in the buffer, masked to valid data bits.
func computeMFMChecksum(data []byte) uint32 {
	var chk uint32
	for i := 0; i+3 < len(data); i += 4 {
		chk ^= binary.BigEndian.Uint32(data[i : i+4])
	}
	return chk & 0x55555555
}

// resetDMA returns the controller to its reset state: no DMA in progress,
// motors off. Disks stay in their drives.
func (d *DiskDrive) resetDMA() {
	d.dskpt, d.dsklen, d.dsklenWrites = 0, 0, 0
	d.dsksync, d.adkcon, d.side = 0x4489, 0, 0
	d.wordequal = false
	d.syncCycles, d.completionCycles = 0, 0
	for _, dr := range d.drives {
		if dr != nil {
			dr.motor = false
		}
	}
}
