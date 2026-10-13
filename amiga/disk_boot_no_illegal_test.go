package amiga

import (
	"encoding/binary"
	"os"
	"testing"
)

// TestDiskBootNoIllegalInstruction boots Workbench from disk and fails if the
// CPU ever takes the illegal-instruction exception (vector 4). Emulation bugs
// (bad opcodes, corrupted code from runaway blits or disk DMA) historically
// surfaced this way. The deliberate MOVEC traps exec uses to detect a 68010+
// are allowed. On failure it dumps the faulting opcode and context.
func TestDiskBootNoIllegalInstruction(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()

	prevPC := uint32(0)
	for i := 0; i < 4_000_000; i++ {
		pc := a.CPU.PC
		buf := make([]byte, 4)
		a.ChipRAM.Read(4*4, buf) // vec 4
		handler := binary.BigEndian.Uint32(buf)
		op := binary.BigEndian.Uint16(mustReadBytes(a, int(prevPC), 2))
		isMOVEC := op == 0x4E7A || op == 0x4E7B // exec's CPU-type probe traps on purpose
		if handler != 0 && pc == handler && prevPC != handler && !isMOVEC {
			// Exception frame
			sr, _ := a.CPU.M.Word(int(a.CPU.A[7]))
			epc, _ := a.CPU.M.Long(int(a.CPU.A[7]) + 2)
			// The true faulting PC is often epc-2 for illegal (PC was advanced)
			// Dump region around both
			for _, base := range []uint32{epc - 8, epc - 2, epc, prevPC - 4, prevPC} {
				if base > 0x80000 {
					continue
				}
				b := mustReadBytes(a, int(base), 16)
				t.Logf("step=%d mem@$%08X: %02X", i, base, b)
			}
			// What was the opcode the CPU just tried? Use processor state if available
			t.Logf("step=%d Illegal: prevPC=$%08X frameEPC=$%08X SR=$%04X D0=$%08X A0=$%08X A1=$%08X A2=$%08X",
				i, prevPC, epc, sr, a.CPU.D[0], a.CPU.A[0], a.CPU.A[1], a.CPU.A[2])

			// Disambiguate: words at prevPC and epc-2
			wPrev := binary.BigEndian.Uint16(mustReadBytes(a, int(prevPC), 2))
			wEm2 := binary.BigEndian.Uint16(mustReadBytes(a, int(epc-2), 2))
			wE := binary.BigEndian.Uint16(mustReadBytes(a, int(epc), 2))
			t.Logf("  word[prevPC]=$%04X word[epc-2]=$%04X word[epc]=$%04X", wPrev, wEm2, wE)

			// Is prevPC a known problem instruction pattern?
			// Dump 32 bytes of context as disasm-friendly hex
			ctx := mustReadBytes(a, int(epc-0x20), 0x40)
			t.Logf("  ctx@$%08X: %02X", epc-0x20, ctx)
			t.Fatalf("illegal instruction exception during disk boot (step %d)", i)
		}
		prevPC = pc
		if err := a.Step(); err != nil {
			t.Logf("  mem around PC: %02X", mustReadBytes(a, int(pc-4), 16))
			t.Fatalf("Step error at %d PC=$%08X: %v", i, pc, err)
		}
	}
}
