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

// DiskDrive emulates an Amiga 3.5" floppy disk drive (DF0:).
type DiskDrive struct {
	amiga *Amiga

	// Disk image
	adf  []byte // 901120 bytes (nil = no disk)
	path string // file the image came from; written tracks are saved there

	// WriteProtected makes the drive report the disk as write-protected
	// and ignore write DMA, like the tab on a real disk.
	WriteProtected bool

	// Write statistics: sectors committed to the image, and sectors in
	// write DMA that were dropped (bad checksum or format) or whose header
	// named a different track than the head was on.
	SectorsWritten, BadSectorWrites, TrackMismatches int

	// SaveErr is the last error writing a track back to the image file.
	SaveErr error

	// Drive state
	motor               bool
	side                int // 0=lower, 1=upper
	cylinder            int // 0-79
	selected            bool
	SelectCount         int // times SEL0 goes low
	DeselectCount       int // times SEL0 goes high
	StatusReads         int
	StatusSelectedTrue  int
	StatusSelectedFalse int
	diskChange          bool // true until first head step after insert

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
	return &DiskDrive{
		amiga:   a,
		dsksync: 0x4489,
	}
}

// InsertDisk loads an ADF image into the drive.
func (d *DiskDrive) InsertDisk(data []byte) error {
	if len(data) != ADFSize {
		return fmt.Errorf("invalid ADF size: %d bytes (expected %d)", len(data), ADFSize)
	}
	d.adf = data
	d.path = ""
	d.diskChange = true
	return nil
}

// HandleCIABWrite processes CIA-B PRB writes for drive control signals.
// PRB bit layout: 7=/MTR, 6=/SEL3, 5=/SEL2, 4=/SEL1, 3=/SEL0, 2=/SIDE, 1=/DIR, 0=/STEP
// DF0 select = bit 3 (/SEL0). Unit select mask $77 = motor ON + DF0 selected.
func (d *DiskDrive) HandleCIABWrite(old, new byte) {
	// Drive select: DF0 uses /SEL0 (bit 3), active low
	wasSelected := d.selected
	d.selected = new&0x08 == 0
	if d.selected && !wasSelected {
		d.SelectCount++
	} else if !d.selected && wasSelected {
		d.DeselectCount++
	}

	// Motor control: direct-set latch. When /SELn has a falling edge,
	// the motor state is SET (not toggled) to the /MTR level:
	// /MTR LOW (bit 7=0) at select time → motor ON
	// /MTR HIGH (bit 7=1) at select time → motor OFF
	if !wasSelected && d.selected {
		d.motor = new&0x80 == 0
	}

	// Side select: 0=upper head, 1=lower head (active low)
	if new&0x04 == 0 {
		d.side = 1 // upper
	} else {
		d.side = 0 // lower
	}

	// Step: falling edge on bit 0
	if old&0x01 != 0 && new&0x01 == 0 {
		if new&0x02 == 0 {
			// DIR=0: step toward center (higher cylinder)
			if d.cylinder < 79 {
				d.cylinder++
			}
		} else {
			// DIR=1: step toward track 0 (lower cylinder)
			if d.cylinder > 0 {
				d.cylinder--
			}
		}
		if d.adf != nil {
			d.diskChange = false // stepping clears disk change only when disk is present
		}
	}
}

// ReadStatus modifies CIA-A PRA on read to reflect drive status.
// CIA-A PRA ($BFE001) bits:
// Bit 5: /DKRDY (low when motor on + selected)
// Bit 4: /DKTRACK0 (low when cylinder==0)
// Bit 3: /DKPROT (low = write protected, always set when disk present)
// Bit 2: /DKCHANGE (low when disk was changed = diskChange==true)
func (d *DiskDrive) ReadStatus(regVal byte) byte {
	val := regVal | 0x3C // set bits 5-2 high (inactive) by default
	d.StatusReads++
	if d.selected {
		d.StatusSelectedTrue++
	} else {
		d.StatusSelectedFalse++
	}

	if d.selected {
		if d.motor && d.adf != nil {
			val &^= 0x20 // /DSKRDY = low (ready)
		}
		if d.cylinder == 0 {
			val &^= 0x10 // /DSKTRACK0 = low
		}
		if d.adf != nil && d.WriteProtected {
			val &^= 0x08 // /DSKPROT = low (write protected)
		}
		if d.adf == nil || d.diskChange {
			val &^= 0x04 // /DKCHANGE = low when no disk or disk was recently changed
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
	if d.motor {
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
	if d.adf == nil || !d.motor || !d.selected {
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
		if !d.WriteProtected {
			d.writeTrack(wordCount)
		}
		d.completionCycles = wordCount * DiskCyclesPerWord
		return
	}

	track := d.cylinder*2 + d.side
	mfm := d.encodeMFMTrack(track)
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
func (d *DiskDrive) writeTrack(words int) {
	buf := make([]byte, 2*words)
	for i := 0; i < len(buf); i += 2 {
		d.amiga.ChipRAM.Read(int(d.dskpt)+i, buf[i:i+2])
	}
	track := d.cylinder*2 + d.side
	n := 0
	for _, sec := range decodeMFMSectors(buf) {
		if !sec.ok || sec.sector >= SectorsPerTrack {
			d.BadSectorWrites++
			continue
		}
		if sec.track != track {
			d.TrackMismatches++
		}
		copy(d.adf[track*TrackSize+sec.sector*BytesPerSector:], sec.data[:])
		n++
	}
	d.SectorsWritten += n
	if n > 0 && d.path != "" {
		d.SaveErr = d.saveTrack(track)
		if d.SaveErr != nil {
			fmt.Fprintf(os.Stderr, "goamiga: saving track %d to %s: %v\n", track, d.path, d.SaveErr)
		}
	}
}

// saveTrack writes one track of the image back to its file.
func (d *DiskDrive) saveTrack(track int) error {
	f, err := os.OpenFile(d.path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteAt(d.adf[track*TrackSize:(track+1)*TrackSize], int64(track*TrackSize))
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
	return d.selected && d.motor && d.adf != nil
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
	// Index pulse once per revolution on CIA-B FLG.
	if d.spinning() {
		now := d.amiga.cycles
		if (now+uint64(cpuCycles))/DiskRevolutionCycles != now/DiskRevolutionCycles {
			d.amiga.CIAB.SetFLG()
		}
	}
}

// CyclesToNextEvent returns the CPU cycles until the next disk interrupt
// source (sync, block done, or an index pulse CIA-B will signal), or -1.
func (d *DiskDrive) CyclesToNextEvent() int {
	next := -1
	consider := func(c int) {
		if c > 0 && (next < 0 || c < next) {
			next = c
		}
	}
	consider(d.syncCycles)
	consider(d.completionCycles)
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
func (d *DiskDrive) encodeMFMTrack(track int) []byte {
	if track < 0 || track >= TracksPerDisk {
		return nil
	}

	trackOffset := track * TrackSize
	rawTrack := d.adf[trackOffset : trackOffset+TrackSize]

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
