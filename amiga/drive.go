package amiga

import "fmt"

// Drive is one 3.5" double-density floppy drive: its disk, motor latch,
// head position and status signals. The disk controller (DiskDrive) talks to
// whichever drives are selected.
type Drive struct {
	Unit int // 0-3: DF0:-DF3:

	// Disk image
	adf  []byte // 901120 bytes (nil = no disk)
	path string // file the image came from; written tracks are saved there

	// WriteProtected makes the drive report the disk as write-protected
	// and ignore write DMA, like the tab on a real disk.
	WriteProtected bool

	// A disk waiting to go in after SwapDisk ejected the old one.
	pending        *pendingDisk
	pendingWait    int // cycles until insertion (shortened by a poll)
	pendingElapsed int // cycles since the eject

	motor      bool // latched from /MTR when the drive is selected
	cylinder   int  // 0-79
	selected   bool // /SELn low
	diskChange bool // /DSKCHANGE low: set by eject/insert, cleared by a step with a disk in

	// External drives identify themselves: after the motor is turned off,
	// each select clocks out the next bit of a 32-bit ID on /RDY (low =
	// 1). A 3.5" DD drive's ID is $FFFFFFFF; an empty port reads 0.
	idBit int
}

// InsertDisk loads an ADF image into the drive.
func (dr *Drive) InsertDisk(data []byte) error {
	if len(data) != ADFSize {
		return fmt.Errorf("invalid ADF size: %d bytes (expected %d)", len(data), ADFSize)
	}
	dr.adf = data
	dr.path = ""
	dr.diskChange = true
	dr.pending = nil
	return nil
}

// Eject removes the disk. /DSKCHANGE goes low and stays low until the head
// is stepped with a disk in the drive; trackdisk polls for that by
// stepping, which is how it notices a swap. A DMA already running still
// completes, as the controller does not know the disk has gone.
func (dr *Drive) Eject() {
	dr.adf = nil
	dr.path = ""
	dr.diskChange = true
}

// Inserted reports whether a disk is in the drive.
func (dr *Drive) Inserted() bool { return dr.adf != nil }

// Path is the file the disk in the drive came from, if any.
func (dr *Drive) Path() string { return dr.path }

// SwapDisk ejects the current disk and inserts data (from file path, or
// "" for an image with no file) once the Amiga has noticed the drive is
// empty (see swapAfterPoll).
func (dr *Drive) SwapDisk(data []byte, path string) error {
	if len(data) != ADFSize {
		return fmt.Errorf("invalid ADF size: %d bytes (expected %d)", len(data), ADFSize)
	}
	dr.Eject()
	dr.pending = &pendingDisk{data: data, path: path}
	dr.pendingWait = swapMaxWait
	dr.pendingElapsed = 0
	return nil
}

// Swapping reports whether a SwapDisk is waiting to insert its disk.
func (dr *Drive) Swapping() bool { return dr.pending != nil }

// insertPending puts the disk queued by SwapDisk in the drive.
func (dr *Drive) insertPending() {
	p := dr.pending
	dr.InsertDisk(p.data)
	dr.path = p.path
}

// tickPending advances a pending SwapDisk insertion.
func (dr *Drive) tickPending(cpuCycles int) {
	if dr.pending == nil {
		return
	}
	dr.pendingWait -= cpuCycles
	dr.pendingElapsed += cpuCycles
	idle := !dr.motor && dr.pendingElapsed >= swapIdleWait
	if dr.pendingWait <= 0 || idle || dr.pendingElapsed >= swapMaxWait {
		dr.insertPending()
	}
}

// step moves the head one cylinder (inward when in is true).
func (dr *Drive) step(in bool) {
	if in {
		if dr.cylinder < 79 {
			dr.cylinder++
		}
	} else if dr.cylinder > 0 {
		dr.cylinder--
	}
	if dr.adf != nil {
		dr.diskChange = false // stepping clears disk change only when disk is present
	} else if dr.pending != nil && !dr.motor && dr.pendingWait > swapAfterPoll {
		dr.pendingWait = swapAfterPoll // trackdisk polled the empty drive
	}
}

// status returns the CIA-A PRA status bits this drive pulls low when
// selected: /DSKRDY (5), /DSKTRACK0 (4), /DSKPROT (3), /DSKCHANGE (2).
func (dr *Drive) status() byte {
	var low byte
	switch {
	case dr.motor && dr.adf != nil:
		low |= 0x20 // ready
	case !dr.motor && dr.Unit > 0:
		low |= 0x20 // ID bit: 1 for every bit of a 3.5" drive's $FFFFFFFF
	}
	if dr.cylinder == 0 {
		low |= 0x10
	}
	if dr.adf != nil && dr.WriteProtected {
		low |= 0x08
	}
	if dr.adf == nil || dr.diskChange {
		low |= 0x04
	}
	return low
}
