package amiga

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

func readLong(a *Amiga, addr uint32) uint32 {
	buf := make([]byte, 4)
	a.CPU.M.Read(int(addr), buf)
	return binary.BigEndian.Uint32(buf)
}

func readWord(a *Amiga, addr uint32) uint16 {
	buf := make([]byte, 2)
	a.CPU.M.Read(int(addr), buf)
	return binary.BigEndian.Uint16(buf)
}

func TestCopperDiag(t *testing.T) {
	romData, err := os.ReadFile("../Kick13.rom")
	if err != nil {
		t.Skipf("Cannot load ROM: %v", err)
	}
	a, err := New(romData, nil)
	if err != nil {
		t.Fatalf("Error creating Amiga: %v", err)
	}
	diskData, err := os.ReadFile("../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skipf("Cannot load disk: %v", err)
	}
	if err := a.Disk.InsertDisk(diskData); err != nil {
		t.Fatalf("Error inserting disk: %v", err)
	}
	if err := a.Reset(); err != nil {
		t.Fatalf("Error resetting: %v", err)
	}

	int2entries := 0
	int3entries := 0
	logCount := 0
	const maxLog = 150
	fc0d14count := 0

	for step := 0; step < 5_000_000; step++ {
		pc := a.CPU.PC

		// Dynamically read vectors since they change during boot
		int2vec := readLong(a, 0x68)
		int3vec := readLong(a, 0x6C)

		// Track INT2 entries (only valid ROM addresses)
		if pc == int2vec && int2vec >= 0xFC0000 {
			int2entries++
			if logCount < maxLog && (int2entries <= 10 || int2entries%10000 == 0) {
				logCount++
				nar := readWord(a, 0xDFF01C)
				reqr := readWord(a, 0xDFF01E)
				fmt.Printf("Step %d: INT2 #%d INTENAR=$%04X INTREQR=$%04X SR=$%04X IPL=%d\n",
					step, int2entries, nar, reqr, a.CPU.SR, (a.CPU.SR>>8)&7)
			}
		}

		// Track INT3 entries (only valid ROM addresses)
		if pc == int3vec && int3vec >= 0xFC0000 {
			int3entries++
			if logCount < maxLog && (int3entries <= 10 || int3entries%10000 == 0) {
				logCount++
				nar := readWord(a, 0xDFF01C)
				reqr := readWord(a, 0xDFF01E)
				fmt.Printf("Step %d: INT3 #%d INTENAR=$%04X INTREQR=$%04X SR=$%04X IPL=%d\n",
					step, int3entries, nar, reqr, a.CPU.SR, (a.CPU.SR>>8)&7)
			}
		}

		// Track PC=0xFC0D14 regardless of vector state
		if pc == 0xFC0D14 {
			fc0d14count++
			if logCount < maxLog && (fc0d14count <= 5 || (step > 2_000_000 && fc0d14count <= 20)) {
				logCount++
				nar := readWord(a, 0xDFF01C)
				reqr := readWord(a, 0xDFF01E)
				fmt.Printf("Step %d: FC0D14 #%d INTENAR=$%04X INTREQR=$%04X SR=$%04X IPL=%d v2=$%08X v3=$%08X\n",
					step, fc0d14count, nar, reqr, a.CPU.SR, (a.CPU.SR>>8)&7, int2vec, int3vec)
			}
		}

		// Every million steps
		if step > 0 && step%1_000_000 == 0 {
			nar := readWord(a, 0xDFF01C)
			reqr := readWord(a, 0xDFF01E)
			v2 := readLong(a, 0x68)
			v3 := readLong(a, 0x6C)
			fmt.Printf("--- %dM: INTENAR=$%04X INTREQR=$%04X INT2=%d INT3=%d fc0d14=%d PC=$%08X v2=$%08X v3=$%08X ---\n",
				step/1_000_000, nar, reqr, int2entries, int3entries, fc0d14count, pc, v2, v3)
		}

		if err := a.Step(); err != nil {
			fmt.Printf("Step %d: Error: %v PC=$%08X\n", step, err, a.CPU.PC)
			break
		}
	}

	fmt.Printf("\nFinal: INT2=%d INT3=%d fc0d14=%d\n", int2entries, int3entries, fc0d14count)
}
