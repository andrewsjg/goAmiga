package amiga

import (
	"encoding/binary"
	"fmt"
)

// An IDE (ATA) hard disk on the A600/A1200 Gayle's interface, with a
// hardfile as the disk. It answers the commands Kickstart's scsi.device and
// HDToolBox use, in PIO mode: identify, read and write (sector by sector or
// in blocks), the drive geometry, and the housekeeping commands.
//
// Sector data comes from the image as is: the Amiga reads the data
// register as big-endian words of the image's bytes, the way WinUAE
// presents hardfiles on IDE (and the reason Amiga-formatted IDE disks look
// byte-swapped in a PC). The drive's own words, IDENTIFY's, are
// little-endian as ATA defines them, so the Amiga reads them swapped and
// its drivers swap them back.

// ATA status and error bits.
const (
	ataBSY  = 0x80
	ataDRDY = 0x40
	ataDSC  = 0x10
	ataDRQ  = 0x08
	ataERR  = 0x01

	ataABRT = 0x04 // error: command aborted
	ataIDNF = 0x10 // error: sector not found
)

// ideCommandDelay is how long a command keeps the drive busy, in CPU
// cycles: long enough that the driver is waiting for the interrupt when it
// comes, as with a real drive.
const ideCommandDelay = 400

type ideDrive struct {
	hf *Hardfile

	// The task file.
	features, nsector, sector, lcyl, hcyl, selectReg, status, errReg byte

	// Translation geometry (IDENTIFY's default, or INITIALIZE DEVICE
	// PARAMETERS).
	cyls, heads, spt int
	defCyls          int
	defHeads         int
	defSpt           int
	multiple         int // sectors per DRQ block for READ/WRITE MULTIPLE (0: off)

	buf     [512 * 16]byte
	bufLen  int // bytes in the current DRQ block
	bufPos  int
	lba     int64 // next sector
	left    int   // sectors still to transfer
	writing bool
	block   int // sectors per DRQ block for this command

	irq    bool // INTRQ asserted (cleared by reading status)
	busy   int  // cycles until the pending step happens
	onDone func()

	onIO func(write bool) // for the activity light
}

func (d *ideDrive) activity(write bool) {
	if d.onIO != nil {
		d.onIO(write)
	}
}

func newIDEDrive(hf *Hardfile) *ideDrive {
	d := &ideDrive{hf: hf}
	total := hf.Size() / 512
	// The hardfile's own geometry when ATA's CHS limits allow it (an RDB's
	// geometry then matches the drive's), else the usual 16 heads, 63
	// sectors.
	if hf.Heads > 0 && hf.Heads <= 16 && hf.Sectors > 0 && hf.Sectors <= 255 && hf.Cylinders > 0 && hf.Cylinders <= 65535 {
		d.defCyls, d.defHeads, d.defSpt = hf.Cylinders, hf.Heads, hf.Sectors
	} else {
		d.defHeads, d.defSpt = 16, 63
		d.defCyls = int(min(total/(16*63), 16383))
	}
	d.reset()
	return d
}

// reset is a power-on or software reset: the ATA signature and defaults.
func (d *ideDrive) reset() {
	d.cyls, d.heads, d.spt = d.defCyls, d.defHeads, d.defSpt
	d.nsector, d.sector, d.lcyl, d.hcyl = 1, 1, 0, 0 // ATA device signature
	d.errReg = 1                                    // diagnostics passed
	d.status = ataDRDY | ataDSC
	d.multiple, d.bufLen, d.bufPos, d.left = 0, 0, 0, 0
	d.irq, d.busy, d.onDone = false, 0, nil
}

func (d *ideDrive) totalSectors() int64 { return d.hf.Size() / 512 }

// address is the sector the task file names: LBA (select bit 6) or CHS
// through the translation geometry.
func (d *ideDrive) address() (int64, bool) {
	if d.selectReg&0x40 != 0 {
		return int64(d.selectReg&0x0F)<<24 | int64(d.hcyl)<<16 | int64(d.lcyl)<<8 | int64(d.sector), true
	}
	c, h, s := int64(d.hcyl)<<8|int64(d.lcyl), int64(d.selectReg&0x0F), int64(d.sector)
	if s == 0 || s > int64(d.spt) || h >= int64(d.heads) {
		return 0, false
	}
	return (c*int64(d.heads)+h)*int64(d.spt) + s - 1, true
}

// setAddress writes sector lba back into the task file, as a drive does
// after a transfer.
func (d *ideDrive) setAddress(lba int64) {
	if d.selectReg&0x40 != 0 {
		d.sector, d.lcyl, d.hcyl = byte(lba), byte(lba>>8), byte(lba>>16)
		d.selectReg = d.selectReg&0xF0 | byte(lba>>24)&0x0F
		return
	}
	if d.spt == 0 || d.heads == 0 {
		return
	}
	s := lba%int64(d.spt) + 1
	t := lba / int64(d.spt)
	h, c := t%int64(d.heads), t/int64(d.heads)
	d.sector, d.lcyl, d.hcyl = byte(s), byte(c), byte(c>>8)
	d.selectReg = d.selectReg&0xF0 | byte(h)
}

// after runs fn once the drive has been busy for a moment.
func (d *ideDrive) after(fn func()) {
	d.status = ataBSY
	d.busy, d.onDone = ideCommandDelay, fn
}

// tick advances a busy drive.
func (d *ideDrive) tick(cycles int) {
	if d.busy <= 0 {
		return
	}
	if d.busy -= cycles; d.busy <= 0 {
		d.busy = 0
		fn := d.onDone
		d.onDone = nil
		if fn != nil {
			fn()
		}
	}
}

// finish ends a command with status and an interrupt.
func (d *ideDrive) finish(err byte) {
	d.status = ataDRDY | ataDSC
	d.errReg = err
	if err != 0 {
		d.status |= ataERR
	}
	d.irq = true
}

func (d *ideDrive) abort() { d.finish(ataABRT) }

// command starts command cmd.
func (d *ideDrive) command(cmd byte) {
	d.errReg = 0
	d.irq = false
	switch cmd {
	case 0xEC: // IDENTIFY DEVICE
		d.after(func() {
			d.identify()
			d.bufLen, d.bufPos, d.left, d.writing = 512, 0, 0, false
			d.status = ataDRDY | ataDSC | ataDRQ
			d.irq = true
		})
	case 0x20, 0x21, 0xC4: // READ SECTORS (with/without retries), READ MULTIPLE
		if cmd == 0xC4 && d.multiple == 0 {
			d.after(d.abort)
			return
		}
		d.startTransfer(false, cmd == 0xC4)
	case 0x30, 0x31, 0xC5: // WRITE SECTORS, WRITE MULTIPLE
		if cmd == 0xC5 && d.multiple == 0 {
			d.after(d.abort)
			return
		}
		d.startTransfer(true, cmd == 0xC5)
	case 0xC6: // SET MULTIPLE MODE
		n := int(d.nsector)
		d.after(func() {
			if n == 0 || n > 16 || n&(n-1) != 0 {
				d.abort()
				return
			}
			d.multiple = n
			d.finish(0)
		})
	case 0x91: // INITIALIZE DEVICE PARAMETERS
		spt, heads := int(d.nsector), int(d.selectReg&0x0F)+1
		d.after(func() {
			if spt == 0 {
				d.abort()
				return
			}
			d.spt, d.heads = spt, heads
			d.cyls = int(min(d.totalSectors()/int64(spt*heads), 65535))
			d.finish(0)
		})
	case 0x40, 0x41: // READ VERIFY SECTORS
		d.after(func() {
			lba, ok := d.address()
			n := int64(d.nsector)
			if n == 0 {
				n = 256
			}
			if !ok || lba+n > d.totalSectors() {
				d.finish(ataIDNF)
				return
			}
			d.setAddress(lba + n - 1)
			d.finish(0)
		})
	case 0x90: // EXECUTE DEVICE DIAGNOSTIC
		d.after(func() {
			d.nsector, d.sector, d.lcyl, d.hcyl = 1, 1, 0, 0
			d.status, d.errReg, d.irq = ataDRDY|ataDSC, 1, true
		})
	case 0xE5, 0x98: // CHECK POWER MODE: active
		d.after(func() { d.nsector = 0xFF; d.finish(0) })
	case 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F, // RECALIBRATE
		0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7A, 0x7B, 0x7C, 0x7D, 0x7E, 0x7F, // SEEK
		0xE0, 0xE1, 0xE2, 0xE3, 0xE6, 0x94, 0x95, 0x96, 0x97, 0x99, // standby, idle, sleep
		0xE7, 0xEA, // FLUSH CACHE
		0xEF: // SET FEATURES
		d.after(func() { d.finish(0) })
	default: // ATAPI commands and anything else
		d.after(d.abort)
	}
}

// startTransfer begins a read or write of nsector sectors.
func (d *ideDrive) startTransfer(write, multiple bool) {
	n := int(d.nsector)
	if n == 0 {
		n = 256
	}
	lba, ok := d.address()
	if !ok || lba+int64(n) > d.totalSectors() || (write && d.hf.ReadOnly) {
		e := byte(ataIDNF)
		if ok && write && d.hf.ReadOnly {
			e = ataABRT
		}
		d.after(func() { d.finish(e) })
		return
	}
	d.lba, d.left, d.writing = lba, n, write
	d.block = 1
	if multiple {
		d.block = d.multiple
	}
	if write {
		// The first block's data is asked for at once, without an
		// interrupt.
		d.prepareWriteBlock()
		return
	}
	d.after(d.readBlock)
}

// readBlock loads the next DRQ block from the disk and interrupts.
func (d *ideDrive) readBlock() {
	k := min(d.block, d.left)
	d.activity(false)
	if err := d.hf.ReadAt(d.buf[:k*512], d.lba*512); err != nil {
		d.finish(0x40) // UNC
		return
	}
	d.bufLen, d.bufPos = k*512, 0
	d.status = ataDRDY | ataDSC | ataDRQ
	d.irq = true
}

func (d *ideDrive) prepareWriteBlock() {
	k := min(d.block, d.left)
	d.bufLen, d.bufPos = k*512, 0
	d.status = ataDRDY | ataDSC | ataDRQ
}

// dataDone is called when the host has moved a whole DRQ block.
func (d *ideDrive) dataDone() {
	if d.left == 0 { // IDENTIFY
		d.status = ataDRDY | ataDSC
		return
	}
	k := d.bufLen / 512
	if d.writing {
		buf := d.buf[:d.bufLen]
		lba := d.lba
		d.after(func() {
			d.activity(true)
			if err := d.hf.WriteAt(buf, lba*512); err != nil {
				d.finish(ataABRT)
				return
			}
			d.lba += int64(k)
			d.left -= k
			d.setAddress(d.lba - 1)
			d.nsector = byte(d.left)
			if d.left == 0 {
				d.finish(0)
				return
			}
			d.prepareWriteBlock()
			d.irq = true
		})
		return
	}
	d.lba += int64(k)
	d.left -= k
	d.setAddress(d.lba - 1)
	d.nsector = byte(d.left)
	if d.left == 0 {
		d.status = ataDRDY | ataDSC
		return
	}
	d.after(d.readBlock)
}

// readData reads a word from the data register.
func (d *ideDrive) readData() uint16 {
	if d.status&ataDRQ == 0 || d.writing && d.left > 0 {
		return 0xFFFF
	}
	v := binary.BigEndian.Uint16(d.buf[d.bufPos:])
	if d.bufPos += 2; d.bufPos >= d.bufLen {
		d.dataDone()
	}
	return v
}

// writeData writes a word to the data register.
func (d *ideDrive) writeData(v uint16) {
	if d.status&ataDRQ == 0 || !d.writing {
		return
	}
	binary.BigEndian.PutUint16(d.buf[d.bufPos:], v)
	if d.bufPos += 2; d.bufPos >= d.bufLen {
		d.status = ataDRDY | ataDSC
		d.dataDone()
	}
}

// identify fills the buffer with the IDENTIFY DEVICE data.
func (d *ideDrive) identify() {
	var w [256]uint16
	str := func(first, words int, s string) {
		b := []byte(fmt.Sprintf("%-*s", 2*words, s))
		for i := 0; i < words; i++ {
			w[first+i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		}
	}
	total := d.totalSectors()
	w[0] = 0x0040 // fixed disk
	w[1] = uint16(d.defCyls)
	w[3] = uint16(d.defHeads)
	w[4] = uint16(512 * d.defSpt)
	w[5] = 512
	w[6] = uint16(d.defSpt)
	str(10, 10, "GOAMIGA0001")
	w[20], w[21] = 3, 16 // buffer type, size (sectors)
	str(23, 4, "1.0")
	str(27, 20, "goAmiga hardfile")
	w[47] = 0x8010 // READ/WRITE MULTIPLE: up to 16 sectors
	w[49] = 0x0200 // LBA
	w[51] = 0x0200 // PIO mode 2
	w[53] = 0x0001 // words 54-58 valid
	w[54], w[55], w[56] = uint16(d.cyls), uint16(d.heads), uint16(d.spt)
	cur := int64(d.cyls) * int64(d.heads) * int64(d.spt)
	w[57], w[58] = uint16(cur), uint16(cur>>16)
	if d.multiple > 0 {
		w[59] = 0x0100 | uint16(d.multiple)
	}
	lba := min(total, 0x0FFFFFFF)
	w[60], w[61] = uint16(lba), uint16(lba>>16)
	for i, v := range w {
		binary.LittleEndian.PutUint16(d.buf[2*i:], v)
	}
}

func (d *ideDrive) state(s *stateCodec) {
	for _, p := range []*byte{&d.features, &d.nsector, &d.sector, &d.lcyl, &d.hcyl, &d.selectReg, &d.status, &d.errReg} {
		num(s, p)
	}
	num(s, &d.cyls)
	num(s, &d.heads)
	num(s, &d.spt)
	num(s, &d.multiple)
	s.fixed(d.buf[:])
	num(s, &d.bufLen)
	num(s, &d.bufPos)
	num(s, &d.lba)
	num(s, &d.left)
	s.bool(&d.writing)
	num(s, &d.block)
	s.bool(&d.irq)
	if s.load { // a command half-way through its busy time just finishes
		d.busy, d.onDone = 0, nil
		if d.status&ataBSY != 0 {
			d.status = ataDRDY | ataDSC
		}
	}
}
