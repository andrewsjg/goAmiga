package amiga

import (
	"fmt"
	"testing"
)

// Trace the call stack when the first full-plane (200x20) blit runs,
// and when Flood / SetRast LVOs are entered.
func TestSetRastCaller(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	const (
		setRastEntry = uint32(0xFC582C) // graphics SetRast stub → FCA5B4
		setRastBody  = uint32(0xFCA5B4)
		floodLVO     = uint32(0xFC5990) // from map: Flood
		bltSizePath  = uint32(0xFC5E12)
	)

	prevBusy := false
	loggedFull := 0
	floodHits := 0
	setRastHits := 0

	// Resolve actual Flood address from earlier map
	// [50] Flood $FC5990

	for i := 0; i < 4_000_000; i++ {
		pc := a.CPU.PC

		if pc == setRastEntry || pc == setRastBody {
			setRastHits++
			if setRastHits <= 10 {
				sp := a.CPU.A[7]
				sb := make([]byte, 64)
				a.ChipRAM.Read(int(sp), sb)
				var stack string
				for j := 0; j < 64; j += 4 {
					v := uint32(sb[j])<<24 | uint32(sb[j+1])<<16 | uint32(sb[j+2])<<8 | uint32(sb[j+3])
					stack += fmt.Sprintf(" $%08X", v)
				}
				fg := byte(0)
				rp := a.CPU.A[1]
				if rp > 0x1000 && rp < ChipRAMSize {
					b := make([]byte, 1)
					a.ChipRAM.Read(int(rp+25), b)
					fg = b[0]
				}
				t.Logf("step=%d SetRast entry PC=$%08X RP=$%05X Fg=%d A0=$%08X stack:%s",
					i, pc, rp, fg, a.CPU.A[0], stack)
			}
		}

		if pc == 0xFC5990 {
			floodHits++
			if floodHits <= 15 {
				sp := a.CPU.A[7]
				sb := make([]byte, 32)
				a.ChipRAM.Read(int(sp), sb)
				ret := uint32(sb[0])<<24 | uint32(sb[1])<<16 | uint32(sb[2])<<8 | uint32(sb[3])
				t.Logf("step=%d Flood A1=$%05X D0=%d D1=%d D2=%d ret=$%08X Fg would be at RP",
					i, a.CPU.A[1], a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], ret)
			}
		}

		if a.Blitter.busy && !prevBusy {
			sz := a.Blitter.lastSize
			h, w := int(sz>>6), int(sz&0x3F)
			if h == 0 {
				h = 1024
			}
			if w == 0 {
				w = 64
			}
			dpt := a.Blitter.preDpt
			if h >= 100 && w >= 10 && dpt >= 0xA572 && dpt < 0xC4B2+8000 && loggedFull < 6 {
				sp := a.CPU.A[7]
				sb := make([]byte, 80)
				a.ChipRAM.Read(int(sp), sb)
				var stack string
				for j := 0; j < 80; j += 4 {
					v := uint32(sb[j])<<24 | uint32(sb[j+1])<<16 | uint32(sb[j+2])<<8 | uint32(sb[j+3])
					stack += fmt.Sprintf(" $%08X", v)
				}
				t.Logf("step=%d FULL-BLIT con0=$%04X dpt=$%05X sz=%dx%d PC=$%08X stack:%s",
					i, a.Blitter.con0, dpt, h, w, pc, stack)
				loggedFull++
			}
		}
		prevBusy = a.Blitter.busy

		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("SetRastHits=%d FloodHits=%d fullBlitsLogged=%d", setRastHits, floodHits, loggedFull)
}
