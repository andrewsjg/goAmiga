package amiga

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"io"
	"strings"

	"github.com/andrewsjg/goamiga/m68k/m68kmem"
)

// Hard disk controller: a Zorro II Autoconfig board with an autoboot ROM
// and a device driver, goamiga.device, serving hardfiles.
//
// It follows the scheme in Commodore's autoboot ROM example (and UAE's
// uaehf.device) rather than emulating a real controller chip:
//
//   - The board's DiagArea is copied to RAM by expansion.library, which
//     calls its DiagPoint; that patches the RomTag in the copy so the
//     system finds and initialises it.
//   - The RomTag's init routine adds the device (whose base is built in
//     the board's RAM), then for each partition calls MakeDosNode and
//     enqueues a BootNode on expansion.library's MountList.
//   - To boot from the disk the system calls the DiagArea's BootPoint,
//     which starts dos.library.
//
// The 68000 code is generated here once the board knows its address, and
// is only glue: each entry point writes a trap number to the board's trap
// register, and Go does the work with full access to the CPU registers and
// memory: opening units, performing I/O requests, building structures.

const (
	hdBoardSize = 64 << 10
	hdTrapReg   = 0x0100 // word write: trap number
	hdROM       = 0x1000 // DiagArea and code
	hdRAM       = 0x8000 // device base, units, parameter packets, boot nodes

	hdDevBase  = hdRAM + 36    // after the 6-entry jump table
	hdUnits    = hdRAM + 0x100 // hdUnitSize bytes per unit (a struct Unit)
	hdUnitSize = 64
	hdCmdList  = hdRAM + 0x800 // NSCMD_DEVICEQUERY supported commands
	hdParms    = hdRAM + 0x1000
	hdParmSize = 128
	hdNames    = hdRAM + 0x3000
	hdNameSize = 32
	hdNodes    = hdRAM + 0x4000 // BootNodes, 20 bytes each
	hdMaxParts = 64
	// HardfileLimit is how many hardfiles the board takes: its Unit
	// structures fill the RAM up to the command list.
	HardfileLimit = (hdCmdList - hdUnits) / hdUnitSize

	hdDeviceName = "goamiga.device"
)

// Trap numbers.
const (
	hdTrapDiag = iota + 1
	hdTrapInit
	hdTrapNextParm
	hdTrapBootNode
	hdTrapOpen
	hdTrapClose
	hdTrapBeginIO
	hdTrapFSPrepare
	hdTrapFSLoad
	hdTrapDirStart
	hdTrapDirPool
	hdTrapDirPacket
)

type hdBoard struct {
	autoconfigState
	amiga *Amiga
	Files []*Hardfile
	Dirs  []*HostDir // host folders (hostfs.go)
	mem   [hdBoardSize]byte

	// Offsets in mem of code the traps need, set when the ROM is built.
	diagOff, romtagOff, diagEnd uint32
	devNameAddr                 uint32
	dirSeg                      uint32 // the folder handler's segment

	configDev uint32 // from DiagPoint
	parts     []hdPart
	nextPart  int

	// Filesystems from RDBs (hdfilesys.go): by DosType, those ready for
	// DeviceNodes this boot, and the one waiting for memory.
	mounts  map[uint32]fsMount
	pending *fsPending

	// Folder handlers that have started, by port, with their lock pools,
	// until their startup packets say which folder they serve.
	newPorts map[uint32]uint32

	// The clipboard helper (clipboard.go): its code, and its progress.
	clipTaskCode, clipVBLCode, clipSeg uint32
	clip                               clipState

	// The AHI driver (ahi.go): its code's addresses and strings, and its
	// state.
	ahiEntry                         uint32 // offset in the board
	ahiVBLCode, ahiSeg, ahiSlaveName uint32
	dosNameAddr                      uint32
	ahiStr                           map[string]uint32
	ahi                              ahiState

	// Trace, if set, logs driver activity.
	Trace io.Writer

	lastIO    uint64 // Amiga.cycles at the last read or write
	lastWrite bool
}

// hdLEDHold keeps the activity light on after a transfer: transfers are
// instant here, so without it the light would never be seen.
const hdLEDHold = 3 * CyclesPerFrame

// Activity reports whether the disk was read or written recently, and if
// so whether the last transfer was a write.
func (h *hdBoard) Activity() (active, write bool) {
	if h.lastIO == 0 {
		return false, false
	}
	return h.amiga.cycles-h.lastIO < hdLEDHold, h.lastWrite
}

type hdPart struct {
	unit int
	p    *Partition
	dir  *HostDir // a host folder rather than a partition of a hardfile
}

// AttachHardfile connects a hard disk image as the next unit of the
// controller (fitting the controller first). Call it before Reset.
func (a *Amiga) AttachHardfile(path string, readOnly bool) error {
	if a.HardDisk == nil {
		a.HardDisk = &hdBoard{amiga: a}
	}
	h := a.HardDisk
	if len(h.Files) >= HardfileLimit {
		return fmt.Errorf("%s: goAmiga's hard disk controller takes at most %d hardfiles", filepath.Base(path), HardfileLimit)
	}
	// An unformatted plain hardfile gets the ROM's native filesystem:
	// FFS from Kickstart 2.0 (V36) on, OFS before.
	dosType := uint32(0x444F5300)
	if a.KickstartVersion() >= 36 {
		dosType |= 1
	}
	f, err := OpenHardfile(path, readOnly, len(h.Files), dosType)
	if err != nil {
		return err
	}
	h.Files = append(h.Files, f)
	a.wireMemoryMap(a.overlay)
	return nil
}

// KickstartVersion is the ROM's exec version (34 for 1.3, 47 for 3.2).
func (a *Amiga) KickstartVersion() int {
	var b [2]byte
	a.ROM.Read(12, b[:])
	return int(binary.BigEndian.Uint16(b[:]))
}

func (h *hdBoard) configByte(reg int) byte {
	switch reg {
	case 0x00: // er_Type: Zorro II, DiagArea valid, 64 KB
		return 0xC0 | 0x10 | sizeCode(hdBoardSize)
	case 0x04: // er_Product
		return 0x02
	case 0x10: // er_Manufacturer $07DB
		return 0x07
	case 0x14:
		return 0xDB
	case 0x28: // er_InitDiagVec
		return hdROM >> 8
	case 0x2C:
		return hdROM & 0xFF
	}
	return 0
}

func (h *hdBoard) space() m68kmem.Memory { return h }
func (h *hdBoard) size() int             { return hdBoardSize }

func (h *hdBoard) configured(base uint32) {
	h.buildROM(base)
}

func (h *hdBoard) Read(addr int, p []byte) (int, error) {
	return copy(p, h.mem[addr:]), nil
}

func (h *hdBoard) Write(addr int, p []byte) (int, error) {
	switch {
	case addr == hdTrapReg && len(p) >= 2:
		h.trap(int(binary.BigEndian.Uint16(p)))
	case addr >= hdRAM:
		copy(h.mem[addr:], p)
	}
	return len(p), nil
}

func (h *hdBoard) Reset() error { return nil }

// ---- The ROM ----

// asm68k assembles the handful of 68000 instructions the ROM needs.
type asm68k struct {
	org    uint32 // address of code[0]
	code   []byte
	labels map[string]uint32
	fixups []asmFixup
}

type asmFixup struct {
	pos   int
	kind  byte // 'b': 8-bit branch; 'L': 32-bit address; 'W': 16-bit offset from label rel
	label string
	rel   string
}

func (s *asm68k) pc() uint32 { return s.org + uint32(len(s.code)) }

func (s *asm68k) w(ws ...uint16) {
	for _, v := range ws {
		s.code = binary.BigEndian.AppendUint16(s.code, v)
	}
}

func (s *asm68k) l(v uint32) { s.code = binary.BigEndian.AppendUint32(s.code, v) }

func (s *asm68k) label(name string) { s.labels[name] = s.pc() }

// addr emits a 32-bit reference to a label.
func (s *asm68k) addr(label string) {
	s.fixups = append(s.fixups, asmFixup{pos: len(s.code), kind: 'L', label: label})
	s.l(0)
}

// offset emits label - rel as a word.
func (s *asm68k) offset(label, rel string) {
	s.fixups = append(s.fixups, asmFixup{pos: len(s.code), kind: 'W', label: label, rel: rel})
	s.w(0)
}

// branch emits a short branch (op is $6000 BRA, $6700 BEQ, ...).
func (s *asm68k) branch(op uint16, label string) {
	s.fixups = append(s.fixups, asmFixup{pos: len(s.code), kind: 'b', label: label})
	s.w(op)
}

func (s *asm68k) str(label, v string) {
	s.label(label)
	s.code = append(s.code, v...)
	s.code = append(s.code, 0)
}

func (s *asm68k) align(n int) {
	for len(s.code)%n != 0 {
		s.code = append(s.code, 0)
	}
}

func (s *asm68k) resolve() {
	for _, f := range s.fixups {
		target, ok := s.labels[f.label]
		if !ok {
			panic("asm68k: undefined label " + f.label)
		}
		switch f.kind {
		case 'L':
			binary.BigEndian.PutUint32(s.code[f.pos:], target)
		case 'W':
			binary.BigEndian.PutUint16(s.code[f.pos:], uint16(target-s.labels[f.rel]))
		case 'b':
			d := int(target) - int(s.org) - f.pos - 2
			if d < -128 || d > 127 || d == 0 {
				panic(fmt.Sprintf("asm68k: branch to %s out of range (%d)", f.label, d))
			}
			s.code[f.pos+1] = byte(int8(d))
		}
	}
}

// Instructions.
func (s *asm68k) rts()               { s.w(0x4E75) }
func (s *asm68k) moveq(n int8)       { s.w(0x7000 | uint16(uint8(n))) } // moveq #n,d0
func (s *asm68k) tstD0()             { s.w(0x4A80) }
func (s *asm68k) jsrLVO(lvo int16)   { s.w(0x4EAE, uint16(lvo)) } // jsr lvo(a6)
func (s *asm68k) execBase()          { s.w(0x2C78, 0x0004) }      // movea.l 4.w,a6
func (s *asm68k) leaA1(label string) { s.w(0x43F9); s.addr(label) }
func (s *asm68k) trap(base uint32, n int) {
	s.w(0x33FC, uint16(n)) // move.w #n,(trap register).l
	s.l(base + hdTrapReg)
}

// Library vector offsets.
const (
	lvoFindResident = -96
	lvoAllocMem     = -198
	lvoFindTask     = -294
	lvoPutMsg       = -366
	lvoGetMsg       = -372
	lvoWaitPort     = -384
	lvoEnqueue      = -270
	lvoReplyMsg     = -378
	lvoCloseLibrary = -414
	lvoAddDevice    = -432
	lvoOpenLibrary  = -552
	lvoMakeDosNode  = -144 // expansion.library
)

// buildROM generates the DiagArea, RomTag and driver code for a board at
// base.
func (h *hdBoard) buildROM(base uint32) {
	s := &asm68k{org: base + hdROM, labels: map[string]uint32{}}
	const bra, beq = 0x6000, 0x6700

	// struct DiagArea
	s.label("diag")
	s.code = append(s.code, 0x90, 0x00) // da_Config: DAC_WORDWIDE|DAC_CONFIGTIME; da_Flags
	s.offset("diagEnd", "diag")         // da_Size: copied to RAM
	s.offset("diagPoint", "diag")
	s.offset("bootPoint", "diag")
	s.offset("diagName", "diag")
	s.w(0, 0)

	// DiagPoint (runs in the RAM copy; a0 board, a2 copy, a3 ConfigDev).
	s.label("diagPoint")
	s.trap(base, hdTrapDiag) // patches the copy's RomTag; d0 = 1
	s.rts()

	// BootPoint: start dos.library, which boots from the MountList.
	s.label("bootPoint")
	s.execBase()
	s.leaA1("dosName")
	s.jsrLVO(lvoFindResident)
	s.tstD0()
	s.branch(beq, "bootFail")
	s.w(0x2040)         // movea.l d0,a0
	s.w(0x2068, 0x0016) // movea.l RT_INIT(a0),a0
	s.w(0x4E90)         // jsr (a0)
	s.label("bootFail")
	s.rts()

	// struct Resident. MatchTag and EndSkip are fixed up in the RAM copy
	// by DiagPoint; the other pointers are absolute into this ROM.
	s.align(2)
	s.label("romtag")
	s.w(0x4AFC)
	s.l(0)                              // rt_MatchTag
	s.l(0)                              // rt_EndSkip
	s.code = append(s.code, 1, 1, 3, 0) // RTF_COLDSTART, version 1, NT_DEVICE, pri 0
	s.addr("devName")
	s.addr("idString")
	s.addr("init")

	s.str("devName", hdDeviceName)
	s.str("idString", "goamiga.device 1.0 (27.9.2026) hardfile driver\r\n")
	s.str("dosName", "dos.library")
	s.str("expName", "expansion.library")
	s.str("diagName", "goAmiga hard disk")
	s.align(4)
	s.label("diagEnd")

	// RomTag init (a6 = ExecBase): add the device, mount the partitions.
	s.label("init")
	s.w(0x48E7, 0x3F3E)      // movem.l d2-d7/a2-a6,-(sp)
	s.trap(base, hdTrapInit) // a1 = device base
	s.jsrLVO(lvoAddDevice)
	s.leaA1("expName")
	s.moveq(0)
	s.jsrLVO(lvoOpenLibrary)
	s.tstD0()
	s.branch(beq, "initDone")
	s.w(0x2C40) // movea.l d0,a6
	s.label("nextPart")
	s.trap(base, hdTrapNextParm) // a0 = parameter packet; d0 = 0 when done
	s.tstD0()
	s.branch(beq, "initClose")
	s.jsrLVO(lvoMakeDosNode)
	s.tstD0()
	s.branch(beq, "nextPart")
	s.w(0x2E00)                   // move.l d0,d7
	s.trap(base, hdTrapFSPrepare) // d0/d1 = memory for its filesystem, or 0
	s.tstD0()
	s.branch(beq, "fsReady")
	s.w(0x2A4E) // movea.l a6,a5
	s.execBase()
	s.jsrLVO(lvoAllocMem)
	s.w(0x2C4D)                // movea.l a5,a6
	s.trap(base, hdTrapFSLoad) // d0 = the memory
	s.label("fsReady")
	s.w(0x2007)                  // move.l d7,d0
	s.trap(base, hdTrapBootNode) // a1 = BootNode, a0 = &eb_MountList
	s.w(0x2A4E)                  // movea.l a6,a5
	s.execBase()
	s.jsrLVO(lvoEnqueue)
	s.w(0x2C4D) // movea.l a5,a6
	s.branch(bra, "nextPart")
	s.label("initClose")
	s.w(0x224E) // movea.l a6,a1
	s.execBase()
	s.jsrLVO(lvoCloseLibrary)
	s.label("initDone")
	// The clipboard helper (a6 = ExecBase): a task, and the vertical
	// blank server that wakes it.
	s.trap(base, hdTrapClipInit) // d0 = 1, a1-a3 for AddTask; 0: none
	s.tstD0()
	s.branch(beq, "initEnd")
	s.jsrLVO(lvoAddTask)
	s.trap(base, hdTrapClipIntInit) // a1 = the Interrupt, d0 = INTB_VERTB
	s.jsrLVO(lvoAddIntServer)
	s.label("initEnd")
	s.w(0x4CDF, 0x7CFC) // movem.l (sp)+,d2-d7/a2-a6
	s.moveq(0)
	s.rts()

	// The clipboard task: calls what Go sets up (a5 = function).
	s.label("clipTask")
	s.trap(base, hdTrapClipTaskCall)
	s.w(0x4E95) // jsr (a5)
	s.trap(base, hdTrapClipTaskRet)
	s.branch(bra, "clipTask")

	// Its vertical blank server: d0 = signal to send the task (a1), or 0.
	s.label("clipVBL")
	s.trap(base, hdTrapClipVBL)
	s.tstD0()
	s.branch(beq, "clipVBLDone")
	s.execBase()
	s.jsrLVO(lvoSignal)
	s.label("clipVBLDone")
	s.moveq(0) // and Z: the next server runs
	s.rts()

	// Device entry points (a6 = device base, a1 = IORequest).
	s.label("devOpen")
	s.trap(base, hdTrapOpen)
	s.rts()
	s.label("devClose")
	s.trap(base, hdTrapClose)
	s.moveq(0)
	s.rts()
	s.label("devNull") // Expunge, the reserved vector, AbortIO
	s.moveq(0)
	s.rts()
	s.label("devBeginIO")
	s.trap(base, hdTrapBeginIO) // d0 = 1: reply the request
	s.tstD0()
	s.branch(beq, "beginIODone")
	s.w(0x2F0E) // move.l a6,-(sp)
	s.execBase()
	s.jsrLVO(lvoReplyMsg)
	s.w(0x2C5F) // movea.l (sp)+,a6
	s.label("beginIODone")
	s.rts()

	// The folder handler: a segment, run as a process by DOS for each
	// folder's DeviceNode (GlobalVec -1: plain machine code).
	s.align(4)
	s.l(16) // segment size: not freed, as DOS never unloads it
	s.label("dirSeg")
	s.l(0) // next segment
	s.execBase()
	s.w(0x93C9) // suba.l a1,a1
	s.jsrLVO(lvoFindTask)
	s.w(0x2640, 0x47EB, 0x005C)  // movea.l d0,a3; lea pr_MsgPort(a3),a3
	s.trap(base, hdTrapDirStart) // d0 = pool size
	s.w(0x223C)
	s.l(memfClear | 1) // move.l #MEMF_PUBLIC|MEMF_CLEAR,d1
	s.jsrLVO(lvoAllocMem)
	s.trap(base, hdTrapDirPool) // d0 = pool, a3 = port
	s.label("dirLoop")
	s.w(0x204B) // movea.l a3,a0
	s.jsrLVO(lvoWaitPort)
	s.w(0x204B)
	s.jsrLVO(lvoGetMsg)
	s.tstD0()
	s.branch(beq, "dirLoop")
	s.w(0x2240)                   // movea.l d0,a1
	s.trap(base, hdTrapDirPacket) // a0 = reply port, a1 = message
	s.jsrLVO(lvoPutMsg)
	s.branch(bra, "dirLoop")

	buildAHIROM(s, base)

	// The clipboard process, created by the task: a segment.
	s.align(4)
	s.l(16)
	s.label("clipSeg")
	s.l(0) // next segment
	s.label("clipProc")
	s.trap(base, hdTrapClipProcCall)
	s.w(0x4E95) // jsr (a5)
	s.trap(base, hdTrapClipProcRet)
	s.branch(bra, "clipProc")

	s.resolve()
	if len(s.code) > hdRAM-hdROM {
		panic("hdBoard: ROM too big")
	}
	h.mem = [hdBoardSize]byte{}
	copy(h.mem[hdROM:], s.code)
	off := func(l string) uint32 { return s.labels[l] - base }
	h.diagOff, h.romtagOff, h.diagEnd = off("diag"), off("romtag"), off("diagEnd")
	h.devNameAddr = s.labels["devName"]
	h.dirSeg = s.labels["dirSeg"]
	h.clipTaskCode, h.clipVBLCode, h.clipSeg = s.labels["clipTask"], s.labels["clipVBL"], s.labels["clipSeg"]
	h.ahiEntry = s.labels["ahiEntry"] - base
	h.ahiVBLCode, h.ahiSeg = s.labels["ahiVBL"], s.labels["ahiSeg"]
	h.ahiSlaveName, h.dosNameAddr = s.labels["ahiSlaveName"], s.labels["dosName"]
	h.ahiStr = map[string]uint32{}
	for _, str := range ahiStrings {
		h.ahiStr[str.label] = s.labels[str.label]
	}

	// Device base: jump table below it, Library above.
	for i, fn := range []string{"devOpen", "devClose", "devNull", "devNull", "devBeginIO", "devNull"} {
		v := hdDevBase - 6*(i+1)
		binary.BigEndian.PutUint16(h.mem[v:], 0x4EF9) // jmp (xxx).l
		binary.BigEndian.PutUint32(h.mem[v+2:], s.labels[fn])
	}
	lib := h.mem[hdDevBase:]
	lib[8] = 3                                                 // ln_Type NT_DEVICE
	binary.BigEndian.PutUint32(lib[10:], h.devNameAddr)        // ln_Name
	lib[14] = 6                                                // LIBF_SUMUSED|LIBF_CHANGED
	binary.BigEndian.PutUint16(lib[16:], 36)                   // lib_NegSize
	binary.BigEndian.PutUint16(lib[18:], 34)                   // lib_PosSize
	binary.BigEndian.PutUint16(lib[20:], 1)                    // lib_Version
	binary.BigEndian.PutUint32(lib[24:], s.labels["idString"]) // lib_IdString
	for i, cmd := range []uint16{2, 3, 4, 5, 9, 10, 11, 12, 13, 14, 15, 18, 19, 20, 21, 22, 24, 25, 26, 27,
		0x4000, 0xC000, 0xC001, 0xC002, 0xC003, 0} {
		binary.BigEndian.PutUint16(h.mem[hdCmdList+2*i:], cmd)
	}

	h.parts = h.parts[:0]
	used := map[string]bool{}
	for u, f := range h.Files {
		for i := range f.Partitions {
			if len(h.parts) < hdMaxParts {
				h.parts = append(h.parts, hdPart{u, &f.Partitions[i], nil})
				used[strings.ToUpper(f.Partitions[i].Name)] = true
			}
		}
	}
	// Folders are DH devices after the hardfiles' partitions.
	n := 0
	for i, d := range h.Dirs {
		for used[fmt.Sprintf("DH%d", n)] {
			n++
		}
		d.Device = fmt.Sprintf("DH%d", n)
		used[d.Device] = true
		if len(h.parts) < hdMaxParts {
			h.parts = append(h.parts, hdPart{hostDirUnit + i, d.partition(), d})
		}
	}
}

// ---- Traps ----

func (h *hdBoard) tracef(format string, args ...any) {
	if h.Trace != nil {
		fmt.Fprintf(h.Trace, "hd: "+format+"\n", args...)
	}
}

func (h *hdBoard) trap(n int) {
	a := h.amiga
	cpu := a.CPU
	// Go's own memory accesses are not CPU bus cycles.
	chip, cia := a.cpuChipAccesses, a.cpuCIAAccesses
	defer func() { a.cpuChipAccesses, a.cpuCIAAccesses = chip, cia }()

	switch n {
	case hdTrapDiag:
		copyAddr := cpu.A[2]
		rt := copyAddr + h.romtagOff - h.diagOff
		a.poke32(rt+2, rt)
		a.poke32(rt+6, copyAddr+h.diagEnd-h.diagOff)
		h.configDev = cpu.A[3]
		cpu.D[0] = 1
		h.tracef("DiagPoint: copy at $%06X, ConfigDev $%06X", copyAddr, h.configDev)

	case hdTrapInit:
		h.ahiReset()
		h.nextPart = 0
		h.mounts, h.pending = map[uint32]fsMount{}, nil
		h.newPorts = map[uint32]uint32{}
		for _, d := range h.Dirs {
			d.reset()
		}
		cpu.A[1] = h.base + hdDevBase
		h.tracef("init: device at $%06X, %d partitions", cpu.A[1], len(h.parts))

	case hdTrapNextParm:
		if h.nextPart >= len(h.parts) {
			cpu.D[0] = 0
			return
		}
		i := h.nextPart
		p := h.parts[i]
		name := h.base + hdNames + uint32(i*hdNameSize)
		a.pokeString(name, p.p.Name)
		pkt := h.base + hdParms + uint32(i*hdParmSize)
		a.poke32(pkt, name)
		a.poke32(pkt+4, h.devNameAddr)
		a.poke32(pkt+8, uint32(p.unit))
		a.poke32(pkt+12, 0)
		for j, v := range p.p.Env {
			a.poke32(pkt+16+uint32(4*j), v)
		}
		cpu.A[0] = pkt
		cpu.D[0] = 1
		h.tracef("mount %s: unit %d, cylinders %d-%d, DosType $%08X",
			p.p.Name, p.unit, p.p.Env[deLowCyl], p.p.Env[deHighCyl], p.p.Env[deDosType])

	case hdTrapBootNode:
		i := h.nextPart
		h.nextPart++
		p := h.parts[i]
		pri := int8(-128)
		if p.p.Bootable {
			pri = int8(max(-127, min(127, int32(p.p.Env[deBootPri]))))
		}
		bn := h.base + hdNodes + uint32(i*20)
		a.poke32(bn, 0)
		a.poke32(bn+4, 0)
		a.poke8(bn+8, 16) // NT_BOOTNODE
		a.poke8(bn+9, byte(pri))
		a.poke32(bn+10, h.configDev)
		a.poke16(bn+14, 0)
		a.poke32(bn+16, cpu.D[0]) // bn_DeviceNode
		cpu.A[1] = bn
		cpu.A[0] = cpu.A[6] + 74 // eb_MountList
		h.tracef("BootNode for %s: DeviceNode $%06X, priority %d", p.p.Name, cpu.D[0], pri)

	case hdTrapFSPrepare:
		h.fsPrepare()
	case hdTrapFSLoad:
		h.fsLoad()
	case hdTrapDirStart:
		h.dirStart()
	case hdTrapDirPool:
		h.dirPool()
	case hdTrapDirPacket:
		h.dirPacket()
	case hdTrapOpen:
		h.open(cpu.A[1], cpu.D[0])
	case hdTrapClose:
		io := cpu.A[1]
		dev := h.base + hdDevBase
		a.poke16(dev+32, a.peek16(dev+32)-1)
		a.poke32(io+20, 0xFFFFFFFF)
		a.poke32(io+24, 0xFFFFFFFF)
	case hdTrapBeginIO:
		cpu.D[0] = h.beginIO(cpu.A[1])
	case hdTrapClipInit:
		h.clipInit()
	case hdTrapClipIntInit:
		h.clipIntInit()
	case hdTrapClipVBL:
		h.clipVBL()
	case hdTrapClipTaskCall:
		h.clipTaskCall()
	case hdTrapClipTaskRet:
		h.clipTaskRet()
	case hdTrapClipProcCall:
		h.clipProcCall()
	case hdTrapClipProcRet:
		h.clipProcRet()
	case hdTrapAHICall:
		h.ahiCallTrap()
	case hdTrapAHIRet:
		h.ahiRetTrap()
	case hdTrapAHISlave:
		h.ahiSlaveTrap()
	case hdTrapAHISlaveRet:
		h.ahiSlaveRetTrap()
	case hdTrapAHIVBL:
		h.ahiVBL()
	}
}

// IORequest fields.
const (
	ioDevice  = 20
	ioUnit    = 24
	ioCommand = 28
	ioFlags   = 30
	ioError   = 31
	ioActual  = 32
	ioLength  = 36
	ioData    = 40
	ioOffset  = 44

	iofQuick = 1
)

func (h *hdBoard) open(io, unit uint32) {
	a := h.amiga
	if unit >= uint32(len(h.Files)) {
		a.poke8(io+ioError, 0xFF) // IOERR_OPENFAIL
		h.tracef("open unit %d: no such unit", unit)
		return
	}
	dev := h.base + hdDevBase
	a.poke32(io+ioUnit, h.base+hdUnits+unit*hdUnitSize)
	a.poke8(io+ioError, 0)
	a.poke8(io+8, 7) // NT_REPLYMSG
	a.poke16(dev+32, a.peek16(dev+32)+1)
	a.poke8(dev+14, a.peek8(dev+14)&^8) // clear LIBF_DELEXP
	h.tracef("open unit %d", unit)
}

// Error codes.
const (
	ioerrNoCmd     = 0xFD // -3
	ioerrBadLength = 0xFC // -4
	tderrNotSpec   = 20
	tderrWriteProt = 28
)

// beginIO performs an I/O request and reports whether the 68000 side must
// reply it (it was not a quick request). TD_ADDCHANGEINT is never replied:
// the request stays with the device until TD_REMCHANGEINT.
func (h *hdBoard) beginIO(io uint32) uint32 {
	a := h.amiga
	cmd := a.peek16(io + ioCommand)
	flags := a.peek8(io + ioFlags)
	a.poke8(io+8, 5) // NT_MESSAGE
	unit := int((a.peek32(io+ioUnit) - h.base - hdUnits) / hdUnitSize)
	if unit < 0 || unit >= len(h.Files) {
		a.poke8(io+ioError, ioerrNoCmd)
		return uint32(flags&iofQuick ^ iofQuick)
	}
	f := h.Files[unit]
	length := a.peek32(io + ioLength)
	data := a.peek32(io + ioData)
	offset := int64(a.peek32(io + ioOffset))
	var errCode byte
	actual, setActual := uint32(0), true

	switch cmd {
	case 24, 25, 26, 27, 0xC000, 0xC001, 0xC002, 0xC003: // TD64 and NSD 64-bit
		offset |= int64(a.peek32(io+ioActual)) << 32
		cmd = map[uint16]uint16{24: 2, 25: 3, 26: 10, 27: 11, 0xC000: 2, 0xC001: 3, 0xC002: 10, 0xC003: 11}[cmd]
	}
	switch cmd {
	case 2: // CMD_READ
		h.lastIO, h.lastWrite = a.cycles|1, false
		buf := make([]byte, length)
		if err := f.ReadAt(buf, offset); err != nil {
			errCode = ioErr(err)
		} else {
			a.pokeBytes(data, buf)
			actual = length
		}
	case 3, 11: // CMD_WRITE, TD_FORMAT
		h.lastIO, h.lastWrite = a.cycles|1, true
		buf := make([]byte, length)
		a.peekBytes(data, buf)
		if err := f.WriteAt(buf, offset); err != nil {
			errCode = ioErr(err)
		} else {
			actual = length
		}
	case 0, 1, 4, 5, 6, 7, 8, 9, 10, 12, 13, 14, 21:
		// CMD_INVALID, RESET, UPDATE, CLEAR, STOP, START, FLUSH, TD_MOTOR
		// (was off), TD_SEEK, TD_REMOVE, TD_CHANGENUM, TD_CHANGESTATE
		// (disk present), TD_REMCHANGEINT
	case 15: // TD_PROTSTATUS
		if f.ReadOnly {
			actual = 1
		}
	case 18: // TD_GETDRIVETYPE
		actual = 1
	case 19: // TD_GETNUMTRACKS
		actual = uint32(f.Cylinders * f.Heads)
	case 20: // TD_ADDCHANGEINT
		a.poke8(io+ioFlags, flags&^iofQuick)
		a.poke8(io+ioError, 0)
		return 0
	case 22: // TD_GETGEOMETRY: struct DriveGeometry
		if length < 32 {
			errCode = ioerrBadLength
			break
		}
		g := make([]byte, 32)
		be := binary.BigEndian
		be.PutUint32(g[0:], hdBlockSize)
		be.PutUint32(g[4:], uint32(f.blocks))
		be.PutUint32(g[8:], uint32(f.Cylinders))
		be.PutUint32(g[12:], uint32(f.Heads*f.Sectors))
		be.PutUint32(g[16:], uint32(f.Heads))
		be.PutUint32(g[20:], uint32(f.Sectors))
		be.PutUint32(g[24:], 1) // MEMF_PUBLIC; DG_DIRECT_ACCESS; no flags
		a.pokeBytes(data, g)
		actual = 32
	case 0x4000: // NSCMD_DEVICEQUERY: struct NSDeviceQueryResult
		if length < 16 {
			errCode = ioerrBadLength
			break
		}
		a.poke32(data, 0)
		a.poke32(data+4, 16)
		a.poke16(data+8, 5) // NSDEVTYPE_TRACKDISK
		a.poke16(data+10, 0)
		a.poke32(data+12, h.base+hdCmdList)
		actual = 16
	default:
		errCode, setActual = ioerrNoCmd, false
	}
	a.poke8(io+ioError, errCode)
	if setActual {
		a.poke32(io+ioActual, actual)
	}
	if h.Trace != nil && cmd != 2 && cmd != 3 {
		h.tracef("unit %d command %d: error %d, actual %d", unit, cmd, int8(errCode), actual)
	}
	return uint32(flags&iofQuick ^ iofQuick)
}

func ioErr(err error) byte {
	switch err {
	case errHDProtected:
		return tderrWriteProt
	case errHDRange:
		return ioerrBadLength
	}
	return tderrNotSpec
}

// ---- Host access to Amiga memory ----

// peekBytes and pokeBytes reach Amiga memory as the CPU would (a 24- or
// 32-bit bus: the Mapper wraps the address to its width).
func (a *Amiga) peekBytes(addr uint32, p []byte) { a.Mapper.Read(int(addr), p) }
func (a *Amiga) pokeBytes(addr uint32, p []byte) { a.Mapper.Write(int(addr), p) }

func (a *Amiga) peek8(addr uint32) byte {
	var b [1]byte
	a.peekBytes(addr, b[:])
	return b[0]
}

func (a *Amiga) peek16(addr uint32) uint16 {
	var b [2]byte
	a.peekBytes(addr, b[:])
	return binary.BigEndian.Uint16(b[:])
}

func (a *Amiga) peek32(addr uint32) uint32 {
	var b [4]byte
	a.peekBytes(addr, b[:])
	return binary.BigEndian.Uint32(b[:])
}

func (a *Amiga) poke8(addr uint32, v byte) { a.pokeBytes(addr, []byte{v}) }

func (a *Amiga) poke16(addr uint32, v uint16) {
	a.pokeBytes(addr, binary.BigEndian.AppendUint16(nil, v))
}

func (a *Amiga) poke32(addr uint32, v uint32) {
	a.pokeBytes(addr, binary.BigEndian.AppendUint32(nil, v))
}

func (a *Amiga) pokeString(addr uint32, s string) {
	a.pokeBytes(addr, append([]byte(s), 0))
}
