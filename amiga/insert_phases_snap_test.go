package amiga

import (
	"testing"
)

func TestInsertPhasesSnap(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	savedPre, savedPost := false, false
	for i := 0; i < 5_000_000; i++ {
		pc := a.CPU.PC
		if pc == 0xFE891E && !savedPre {
			saveBPL12PNG(a, "/tmp/insert_pre_phase2.png", 0xA572, 0xC4B2)
			saveBPL12PNG(a, "../insert_pre_phase2.png", 0xA572, 0xC4B2)
			t.Logf("step=%d saved pre-phase2", i)
			savedPre = true
		}
		if savedPre && pc == 0xFE896C && !savedPost {
			// after one complete phase2 cycle end - actually this is loop check
			// wait until FE8970 area done - FE896C is bmi to end
			// when we hit FE8970-ish after phase2
		}
		a.Step()
		// FE896C is exit of phase2 when template list ends (bmi)
		if savedPre && !savedPost && pc == 0xFE896C {
			saveBPL12PNG(a, "/tmp/insert_post_phase2.png", 0xA572, 0xC4B2)
			saveBPL12PNG(a, "../insert_post_phase2.png", 0xA572, 0xC4B2)
			t.Logf("step=%d saved post-phase2 PC=$%08X", i, pc)
			savedPost = true
			break
		}
	}
	if !savedPre || !savedPost {
		t.Fatalf("pre=%v post=%v", savedPre, savedPost)
	}
}
