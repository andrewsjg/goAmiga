package amiga

import (
	"fmt"
	"os"
	"testing"
)

// Trace dos.library init and post-strap filesystem activity.
func TestDiskDosBoot(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()

	readName := func(addr uint32) string {
		if addr == 0 {
			return ""
		}
		b := make([]byte, 40)
		for i := 0; i < 40; i++ {
			v, err := a.CPU.M.Byte(int(addr) + i)
			if err != nil || v == 0 {
				return string(b[:i])
			}
			b[i] = byte(v)
		}
		return string(b)
	}

	counts := map[string]int{}
	logn := func(name string, i int, msg string, limit int) {
		counts[name]++
		if counts[name] <= limit {
			t.Logf("step=%d %s %s", i, name, msg)
		}
	}

	// LVO addresses for ExecBase $676 (from prior tests)
	const (
		lvoOpenLib  = 0x044E // -$228
		lvoAddTask  = 0x055C // -$11A
		lvoDoIO     = 0x04A2 // -$1C8? verify
		lvoSendIO   = 0x04A8
		lvoWaitIO   = 0x04AE
		lvoOpenDev  = 0x04BA
		lvoAlert    = 0x060A // -$6C → $676-0x6C = $60A
		lvoAllocMem = 0x05B0 // -$C6 → $676-0xC6 = $5B0
		lvoFindTask = 0x0556
		lvoWait     = 0x0538 // -$13E → $676-0x13E = $538
	)

	// Confirm LVO stubs by reading jump table at runtime after exec init
	lvoResolved := false
	var openLib, addTask, alertLVO, waitIO, openDev, doIO uint32

	for i := 0; i < 15_000_000; i++ {
		pc := a.CPU.PC

		// Resolve LVOs once ExecBase is valid
		if !lvoResolved && i > 500_000 {
			eb, _ := a.CPU.M.Long(4)
			if eb == 0x676 {
				// Read JMP targets from negative offsets
				// Each LVO is 6-byte JMP absolute: 4EF9 xxxx xxxx
				readLVO := func(off int) uint32 {
					// ExecBase+off points to jmp stub in library vector table
					// Actually OpenLibrary is at ExecBase-552, the code there is JMP
					addr := int(eb) + off
					op, _ := a.CPU.M.Word(addr)
					if op == 0x4EF9 {
						tgt, _ := a.CPU.M.Long(addr + 2)
						return uint32(tgt)
					}
					return uint32(addr) // entry is the stub itself
				}
				openLib = readLVO(-0x228)
				addTask = readLVO(-0x11A)
				alertLVO = readLVO(-0x6C)
				waitIO = readLVO(-0x1C8) // WaitIO is -$1C8? Actually CheckIO -$1D0, WaitIO -$1C8, DoIO -$1C2
				// Standard: DoIO=-$1C2, WaitIO=-$1C8, SendIO=-$1CE, CheckIO=-$1D0
				// Recheck: OpenDevice=-$1BC, CloseDevice=-$1C2, DoIO=-$1C8, SendIO=-$1CE, CheckIO=-$1D4, WaitIO=-$1DA
				// I'll use the bootdecision_test values:
				// WaitIO(-$1C8) at 0x04AE with ExecBase $676: $676-$1C8=$4AE
				doIO = readLVO(-0x1C8)
				waitIO = readLVO(-0x1DA)
				openDev = readLVO(-0x1BC)
				lvoResolved = true
				t.Logf("LVOs openLib=$%08X addTask=$%08X alert=$%08X doIO=$%08X waitIO=$%08X openDev=$%08X",
					openLib, addTask, alertLVO, doIO, waitIO, openDev)
			}
		}

		switch pc {
		case 0xFF3E94:
			logn("dos_init", i, fmt.Sprintf("D0=$%08X", a.CPU.D[0]), 3)
		case 0xFF3F8A:
			logn("createproc_ret", i, fmt.Sprintf("D1=$%08X D0=$%08X", a.CPU.D[1], a.CPU.D[0]), 5)
		case 0xFF4014:
			logn("dos_alert070", i, fmt.Sprintf("D7 will be $07000000"), 5)
		case 0xFF420C:
			logn("createproc_call", i, fmt.Sprintf("D1=$%08X D2=$%08X D4=$%08X", a.CPU.D[1], a.CPU.D[2], a.CPU.D[4]), 5)
		case 0xFE86EC:
			logn("strap_jmp", i, fmt.Sprintf("A0=$%08X", a.CPU.A[0]), 3)
		case 0xFE85CA:
			logn("bb_ret", i, fmt.Sprintf("D0=$%08X", a.CPU.D[0]), 3)
		case 0xFD423C:
			logn("mouse_wait", i, fmt.Sprintf("D6=$%08X A5=$%08X", a.CPU.D[6], a.CPU.A[5]), 2)
		}

		// LVO stubs in low memory (direct entries used by bootdecision)
		switch pc {
		case 0x044E: // OpenLibrary stub
			name := readName(a.CPU.A[1])
			logn("OpenLibrary", i, fmt.Sprintf("%q ver=%d", name, a.CPU.D[0]), 25)
		case 0x055C: // AddTask
			namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			logn("AddTask", i, fmt.Sprintf("task=$%08X initPC=$%08X name=%q",
				a.CPU.A[1], a.CPU.A[0], readName(uint32(namePtr))), 25)
		case 0x04BA: // OpenDevice
			logn("OpenDevice", i, fmt.Sprintf("%q unit=%d", readName(a.CPU.A[0]), a.CPU.D[0]), 20)
		case 0x060A: // Alert LVO entry?
			logn("Alert_060A", i, fmt.Sprintf("D7=$%08X D0=$%08X", a.CPU.D[7], a.CPU.D[0]), 10)
		case 0xFC3280: // common Alert ROM?
			// skip
		}

		// Detect jsr -$6c(a6) by watching known Alert ROM entry
		// Kickstart Alert implementation often at FC3118 or similar
		if pc == 0xFC3118 || pc == 0xFC3120 || pc == 0xFC30F8 {
			logn("Alert_rom", i, fmt.Sprintf("pc=$%08X D7=$%08X A6=$%08X", pc, a.CPU.D[7], a.CPU.A[6]), 8)
		}

		// FileSys / packet related in ROM
		if pc == 0xFF47C0 {
			logn("ff47c0", i, fmt.Sprintf("D0=$%08X", a.CPU.D[0]), 5)
		}

		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		if i > 0 && i%3_000_000 == 0 {
			t.Logf("--- %dM PC=$%08X cyl=%d counts subset decode-ish ---", i/1_000_000, pc, a.Disk.cylinder)
		}
	}

	t.Logf("FINAL counts=%v PC=$%08X cyl=%d", counts, a.CPU.PC, a.Disk.cylinder)
	eb, _ := a.CPU.M.Long(4)
	if eb > 0 {
		// Dump a few exec fields
		for _, off := range []int{0x114, 0x11A, 0x126, 0x202, 0x204, 0x276} {
			v, _ := a.CPU.M.Long(int(eb) + off)
			t.Logf("ExecBase+$%03X = $%08X", off, v)
		}
	}
	_ = lvoOpenLib
	_ = lvoAddTask
	_ = lvoDoIO
	_ = lvoSendIO
	_ = lvoWaitIO
	_ = lvoOpenDev
	_ = lvoAlert
	_ = lvoAllocMem
	_ = lvoFindTask
	_ = lvoWait
}
