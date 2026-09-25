package amiga

import (
	"encoding/binary"
	"fmt"
)

const (
	ADFSize         = 901120 // standard ADF file size
	SectorsPerTrack = 11
	BytesPerSector  = 512
	TracksPerDisk   = 160                              // 80 cylinders * 2 sides
	TrackSize       = SectorsPerTrack * BytesPerSector // 5632 bytes raw data per track
)

// DiskDrive emulates an Amiga 3.5" floppy disk drive (DF0:).
type DiskDrive struct {
	amiga *Amiga

	// Disk image
	adf []byte // 901120 bytes (nil = no disk)

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

	// Deferred DMA completion: data is transferred immediately (like the
	// blitter), but DSKBLK/DSKSYN interrupts fire after a delay so the CPU
	// can finish the post-DSKLEN setup code before the interrupt is taken.
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
		if d.adf != nil {
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
		// Write DMA not implemented (read-only disk)
		return
	}

	// Calculate track index
	track := d.cylinder*2 + d.side

	// Encode track to MFM
	mfmTrack := d.encodeMFMTrack(track)
	if mfmTrack == nil {
		return
	}

	// Scan for sync word
	syncOff := -1
	for i := 0; i < len(mfmTrack)-1; i += 2 {
		w := uint16(mfmTrack[i])<<8 | uint16(mfmTrack[i+1])
		if w == d.dsksync {
			syncOff = i + 2 // start copying after the sync word
			d.wordequal = true
			break
		}
	}

	if syncOff < 0 {
		// No sync found — still schedule a completion so software unblocks
		d.completionCycles = 64
		return
	}

	// WORDSYNC mode (ADKCON bit 10): wait for sync before transferring
	if d.adkcon&0x0400 == 0 {
		// If WORDSYNC not enabled, start from beginning of MFM buffer
		syncOff = 0
	}

	// Copy words to ChipRAM
	byteCount := wordCount * 2
	dst := d.dskpt
	for i := 0; i < byteCount && syncOff+i < len(mfmTrack); i++ {
		buf := []byte{mfmTrack[syncOff+i]}
		d.amiga.ChipRAM.Write(int(dst), buf)
		dst++
	}

	// Defer interrupts: ~2 CPU cycles per word (one DMA slot), min 64.
	// Instant DSKBLK during the DSKLEN write races the post-setup code.
	cycles := wordCount * 2
	if cycles < 64 {
		cycles = 64
	}
	d.completionCycles = cycles
}

// TickCycles advances deferred disk DMA completion. Called from Amiga.Step().
func (d *DiskDrive) TickCycles(cpuCycles int) {
	if d.completionCycles <= 0 {
		return
	}
	d.completionCycles -= cpuCycles
	if d.completionCycles <= 0 {
		d.completionCycles = 0
		d.amiga.Custom.SetIntreq(1 << 12) // DSKSYN
		d.amiga.Custom.SetIntreq(1 << 1)  // DSKBLK
	}
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
