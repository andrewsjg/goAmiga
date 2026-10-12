package amiga

import (
	"encoding/binary"
	"fmt"
)

// SCSI devices, for the SCSI controllers (a2091.go).
//
// A command runs whole: the controller hands the device the command
// descriptor block and, for a command that sends data, the data; the
// device returns the data it sends back and its status byte.

// scsiDevice is a target on the SCSI bus.
type scsiDevice interface {
	// dataOut is how many bytes the command sends to the device (0 if it
	// sends none).
	dataOut(cdb []byte) int
	// command runs cdb with the data sent, and returns the data to send
	// back and the status byte.
	command(lun int, cdb, out []byte) (in []byte, status byte)
}

const (
	scsiGood           = 0x00
	scsiCheckCondition = 0x02

	senseNone           = 0x0
	senseNotReady       = 0x2
	senseMediumError    = 0x3
	senseIllegalRequest = 0x5
	senseDataProtect    = 0x7
	senseBlankCheck     = 0x8
)

// cdbLength is the length of a command descriptor block from its group
// (the top 3 bits of the opcode).
func cdbLength(op byte) int {
	switch op >> 5 {
	case 0:
		return 6
	case 1, 2:
		return 10
	case 5:
		return 12
	}
	return 6
}

// scsiDisk is a hard disk backed by a hardfile.
type scsiDisk struct {
	hf    *Hardfile
	sense [3]byte // key, additional sense code, qualifier
	onIO  func(write bool)
}

func newSCSIDisk(hf *Hardfile) *scsiDisk { return &scsiDisk{hf: hf} }

func (d *scsiDisk) fail(key, asc byte) ([]byte, byte) {
	d.sense = [3]byte{key, asc, 0}
	return nil, scsiCheckCondition
}

func (d *scsiDisk) blocks() uint32 { return uint32(d.hf.blocks) }

// rw decodes the block address and count of READ/WRITE(6) and (10).
func rwArgs(cdb []byte) (lba uint32, n uint32) {
	be := binary.BigEndian
	if cdb[0]>>5 == 0 {
		lba = uint32(cdb[1]&0x1F)<<16 | uint32(be.Uint16(cdb[2:]))
		n = uint32(cdb[4])
		if n == 0 {
			n = 256
		}
		return
	}
	return be.Uint32(cdb[2:]), uint32(be.Uint16(cdb[7:]))
}

func (d *scsiDisk) dataOut(cdb []byte) int {
	switch cdb[0] {
	case 0x0A, 0x2A, 0x2E: // WRITE(6), WRITE(10), WRITE AND VERIFY
		_, n := rwArgs(cdb)
		return int(n) * hdBlockSize
	case 0x15: // MODE SELECT(6)
		return int(cdb[4])
	case 0x04: // FORMAT UNIT with a defect list
		if cdb[1]&0x10 != 0 {
			return 4
		}
	}
	return 0
}

func (d *scsiDisk) command(lun int, cdb, out []byte) ([]byte, byte) {
	if lun != 0 && cdb[0] != 0x12 && cdb[0] != 0x03 {
		return d.fail(senseIllegalRequest, 0x25) // logical unit not supported
	}
	be := binary.BigEndian
	switch cdb[0] {
	case 0x00, 0x01, 0x0B, 0x16, 0x17, 0x1B, 0x1D, 0x1E, 0x2B, 0x2F, 0x35, 0x04:
		// TEST UNIT READY, REZERO, SEEK, RESERVE, RELEASE, START STOP,
		// SEND DIAGNOSTIC, PREVENT ALLOW, SEEK(10), VERIFY, SYNCHRONIZE
		// CACHE, FORMAT UNIT (the image is already "formatted").
	case 0x03: // REQUEST SENSE
		s := make([]byte, 18)
		s[0] = 0x70
		s[2] = d.sense[0]
		s[7] = 10
		s[12], s[13] = d.sense[1], d.sense[2]
		d.sense = [3]byte{}
		if lun != 0 {
			s[2], s[12] = senseIllegalRequest, 0x25
		}
		return clip(s, int(cdb[4])), scsiGood
	case 0x12: // INQUIRY
		r := make([]byte, 36)
		if lun != 0 {
			r[0] = 0x7F // no device on this LUN
		}
		r[2], r[3], r[4] = 2, 2, 31 // SCSI-2, its format, more bytes
		copy(r[8:], fmt.Sprintf("%-8s%-16s%-4s", "GOAMIGA", "HARDFILE", "1.0"))
		return clip(r, int(cdb[4])), scsiGood
	case 0x25: // READ CAPACITY
		r := make([]byte, 8)
		be.PutUint32(r, d.blocks()-1)
		be.PutUint32(r[4:], hdBlockSize)
		return r, scsiGood
	case 0x08, 0x28: // READ
		lba, n := rwArgs(cdb)
		if uint64(lba)+uint64(n) > uint64(d.blocks()) {
			return d.fail(senseIllegalRequest, 0x21) // LBA out of range
		}
		buf := make([]byte, int(n)*hdBlockSize)
		if err := d.hf.ReadAt(buf, int64(lba)*hdBlockSize); err != nil {
			return d.fail(senseMediumError, 0x11)
		}
		if d.onIO != nil {
			d.onIO(false)
		}
		return buf, scsiGood
	case 0x0A, 0x2A, 0x2E: // WRITE
		lba, n := rwArgs(cdb)
		if uint64(lba)+uint64(n) > uint64(d.blocks()) {
			return d.fail(senseIllegalRequest, 0x21)
		}
		if err := d.hf.WriteAt(out, int64(lba)*hdBlockSize); err != nil {
			if err == errHDProtected {
				return d.fail(senseDataProtect, 0x27)
			}
			return d.fail(senseMediumError, 0x0C)
		}
		if d.onIO != nil {
			d.onIO(true)
		}
	case 0x15: // MODE SELECT: accepted and ignored
	case 0x1A: // MODE SENSE(6)
		return clip(d.modeSense(cdb[2]&0x3F, cdb[1]&8 != 0), int(cdb[4])), scsiGood
	case 0x37: // READ DEFECT DATA: none
		r := []byte{0, cdb[2] & 0x1F, 0, 0}
		return clip(r, int(be.Uint16(cdb[7:]))), scsiGood
	default:
		return d.fail(senseIllegalRequest, 0x20) // invalid command
	}
	return nil, scsiGood
}

// modeSense builds the MODE SENSE reply: a header, a block descriptor
// and the pages asked for (3, format; 4, geometry; or $3F, all).
func (d *scsiDisk) modeSense(page byte, noBlockDescriptor bool) []byte {
	be := binary.BigEndian
	r := []byte{0, 0, 0, 0}
	if d.hf.ReadOnly {
		r[2] = 0x80 // write protected
	}
	if !noBlockDescriptor {
		bd := make([]byte, 8)
		be.PutUint32(bd, d.blocks()&0xFFFFFF)
		be.PutUint32(bd[4:], hdBlockSize)
		r = append(r, bd...)
		r[3] = 8
	}
	heads, spt := max(d.hf.Heads, 1), max(d.hf.Sectors, 1)
	cyls := int(d.blocks()) / (heads * spt)
	if page == 3 || page == 0x3F {
		p := make([]byte, 24)
		p[0], p[1] = 3, 22
		be.PutUint16(p[10:], uint16(spt))
		be.PutUint16(p[12:], hdBlockSize)
		r = append(r, p...)
	}
	if page == 4 || page == 0x3F {
		p := make([]byte, 24)
		p[0], p[1] = 4, 22
		p[2], p[3], p[4] = byte(cyls>>16), byte(cyls>>8), byte(cyls)
		p[5] = byte(heads)
		be.PutUint16(p[20:], 3600)
		r = append(r, p...)
	}
	r[0] = byte(len(r) - 1)
	return r
}

func clip(b []byte, n int) []byte {
	if n < len(b) {
		return b[:n]
	}
	return b
}
