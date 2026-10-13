package amiga

import (
	"encoding/binary"
	"os"
	"testing"
)

// Find the Illegal Instruction (vec 4) that produces Software Failure / LastAlert=$4.
func TestDiskIllegalInstr(t *testing.T) {
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

	vecNames := []string{"", "", "BusErr", "AddrErr", "Illegal", "Div0", "CHK", "TRAPV", "Priv", "Trace", "LineA", "LineF"}
	counts := map[int]int{}
	lastPC := uint32(0)
	prevPC := uint32(0)

	// Read vectors each time after exec is up
	for i := 0; i < 5_000_000; i++ {
		pc := a.CPU.PC

		if i > 2_200_000 && pc != lastPC+2 && pc != lastPC+4 && pc != lastPC+6 &&
			pc != lastPC+8 && pc != lastPC {
			for vec := 2; vec <= 11; vec++ {
				buf := make([]byte, 4)
				a.ChipRAM.Read(vec*4, buf)
				handler := binary.BigEndian.Uint32(buf)
				if handler != 0 && pc == handler {
					counts[vec]++
					// Skip intentional SuperState privilege hits at FC08E6 path
					if vec == 8 && (prevPC == 0xFC08E6 || prevPC == 0xFC08F6 || prevPC == 0xFC08EA || prevPC == 0xFC08F0) {
						break
					}
					if counts[vec] <= 20 || vec == 4 {
						// frame
						sr, _ := a.CPU.M.Word(int(a.CPU.A[7]))
						epc, _ := a.CPU.M.Long(int(a.CPU.A[7]) + 2)
						// opcode at fault PC
						var op uint16
						if epc < 0x80000 {
							ob := make([]byte, 2)
							a.ChipRAM.Read(int(epc), ob)
							op = binary.BigEndian.Uint16(ob)
						} else if epc >= 0xFC0000 {
							ob := make([]byte, 2)
							a.ROM.Read(int(epc-0xFC0000), ob)
							op = binary.BigEndian.Uint16(ob)
						}
						t.Logf("step=%d %s vec=%d from=$%08X frameEPC=$%08X SR=$%04X op=$%04X SP=$%08X D0=$%08X A0=$%08X",
							i, vecNames[vec], vec, prevPC, epc, sr, op, a.CPU.A[7], a.CPU.D[0], a.CPU.A[0])
						if vec == 4 && counts[4] <= 5 {
							// dump 16 bytes at fault
							t.Logf("  code@$%08X: %02X", epc, mustReadBytes(a, int(epc), 16))
						}
						if vec == 4 && counts[4] >= 3 {
							// enough
							goto done
						}
					}
					break
				}
			}
		}

		// Also catch guru alert store of LastAlert
		if pc == 0xFC3060 {
			t.Logf("step=%d FC3060 store LastAlert D7=$%08X A5=$%08X (task?)", i, a.CPU.D[7], a.CPU.A[5])
			if a.CPU.A[5] > 0 && a.CPU.A[5] < 0x80000 {
				// ThisTask pointer content
				v, _ := a.CPU.M.Long(int(a.CPU.A[5]))
				t.Logf("  (A5)=$%08X", v)
			}
			// stack dump
			for k := 0; k < 6; k++ {
				v, _ := a.CPU.M.Long(int(a.CPU.A[7]) + k*4)
				t.Logf("  stk[%d]=$%08X", k, v)
			}
		}

		prevPC = pc
		lastPC = pc
		if err := a.Step(); err != nil {
			t.Logf("CPU err step=%d PC=$%08X: %v", i, pc, err)
			break
		}
	}
done:
	t.Logf("exception counts: %v", counts)
	eb, _ := a.CPU.M.Long(4)
	if eb > 0 {
		la, _ := a.CPU.M.Long(int(eb) + 0x202)
		t.Logf("LastAlert=$%08X PC=$%08X", la, a.CPU.PC)
	}
}
