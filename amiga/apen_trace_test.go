package amiga

import (
	"fmt"
	"testing"
)

func TestAPenTrace(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	const bpl1Start, bpl2End = 0xA572, 0xC4B2 + 8000
	prevBusy := false
	n := 0
	buf := make([]byte, 1)

	readPen := func(rp uint32) (fg, bg, drmd byte, bm uint32) {
		if rp < 0x1000 || rp > 0x70000 {
			return
		}
		a.ChipRAM.Read(int(rp+25), buf)
		fg = buf[0]
		a.ChipRAM.Read(int(rp+26), buf)
		bg = buf[0]
		a.ChipRAM.Read(int(rp+28), buf)
		drmd = buf[0]
		bb := make([]byte, 4)
		a.ChipRAM.Read(int(rp+4), bb)
		bm = uint32(bb[0])<<24 | uint32(bb[1])<<16 | uint32(bb[2])<<8 | uint32(bb[3])
		return
	}

	for i := 0; i < 8_000_000 && n < 25; i++ {
		if a.Blitter.busy && !prevBusy {
			dpt := a.Blitter.preDpt
			sz := a.Blitter.lastSize
			h := int(sz >> 6)
			w := int(sz & 0x3F)
			if h == 0 {
				h = 1024
			}
			if w == 0 {
				w = 64
			}
			if h >= 100 && w >= 10 && dpt >= bpl1Start && dpt < uint32(bpl2End) {
				plane := "BPL1"
				if dpt >= 0xC4B2 {
					plane = "BPL2"
				}
				msg := ""
				for _, reg := range []struct {
					n string
					v uint32
				}{
					{"A1", a.CPU.A[1]}, {"A2", a.CPU.A[2]},
					{"A3", a.CPU.A[3]}, {"A4", a.CPU.A[4]},
					{"A5", a.CPU.A[5]},
				} {
					fg, bg, dm, bm := readPen(reg.v)
					if bm > 0x1000 && bm < 0x80000 {
						msg += fmt.Sprintf(" %s=$%05X:Fg=%d Bg=%d DrMd=%d BM=$%05X",
							reg.n, reg.v, fg, bg, dm, bm)
					}
				}
				// stack scan for RP
				sp := a.CPU.A[7]
				sb := make([]byte, 48)
				a.ChipRAM.Read(int(sp), sb)
				for j := 0; j < 48; j += 4 {
					v := uint32(sb[j])<<24 | uint32(sb[j+1])<<16 | uint32(sb[j+2])<<8 | uint32(sb[j+3])
					fg, bg, dm, bm := readPen(v)
					if bm == 0x5C00 || (bm >= 0x5C00 && bm <= 0x5C40) || (fg <= 3 && bg <= 3 && bm > 0x5000 && bm < 0x10000) {
						msg += fmt.Sprintf(" stk+$%02X=$%05X:Fg=%d Bg=%d DrMd=%d BM=$%05X",
							j, v, fg, bg, dm, bm)
					}
				}
				t.Logf("step=%d %s con0=$%04X dpt=$%05X PC=$%08X D0=$%08X D1=$%08X%s",
					i, plane, a.Blitter.con0, dpt, a.CPU.PC, a.CPU.D[0], a.CPU.D[1], msg)
				n++
			}
		}
		prevBusy = a.Blitter.busy
		a.Step()
	}
}
