package amiga

import (
	"os"
	"testing"
)

// Workbench in the 31 kHz modes (BEAMCON0's programmable beam), with the
// monitor drivers from an installed Workbench 3.2 (installedHDF).
func TestWorkbench31kHzModes(t *testing.T) {
	for _, m := range []struct {
		name           string
		chipset        string
		id             uint32
		w, h           uint16
		monitor        string
		width, height  int // the frame
		lineCCK, lines int // the beam
	}{
		{"Productivity", "aga", 0x00039024, 640, 480, "Multiscan", 640, 480, 0x79 + 1, 0x1F9 + 1},
		// On ECS graphics.library gives Productivity VGA's own timing.
		{"Productivity ECS", "ecs", 0x00039024, 640, 480, "Multiscan", 640, 480, 114, 525},
		{"DblPAL", "aga", 0x000A9000, 640, 256, "DblPAL", 640, 512, 0x81 + 1, 0x23D + 1},
	} {
		t.Run(m.name, func(t *testing.T) {
			a := workbenchInModeOn(t, m.chipset, m.id, m.w, m.h, 2, []string{m.monitor})
			if !a.beamVariable() {
				t.Fatalf("BEAMCON0 $%04X: no programmable beam", a.Custom.ReadReg16(0x1DC))
			}
			if got := a.lineCycles / CPUCyclesPerColorClock; got != m.lineCCK {
				t.Errorf("lines of %d colour clocks, want %d", got, m.lineCCK)
			}
			if got := a.linesThisFrame(); got != m.lines {
				t.Errorf("frames of %d lines, want %d", got, m.lines)
			}
			v := a.Video
			t.Logf("frame %dx%d from H $%X, line %d; shown at %dx%d; %.1f Hz",
				v.Width, v.Height, v.left, v.top, v.Width, v.Height, a.FrameRate())
			if v.Width != m.width || v.Height != m.height {
				t.Errorf("frame %dx%d, want %dx%d", v.Width, v.Height, m.width, m.height)
			}
			// The mouse pointer (red) is in the top left corner: sprites
			// follow the programmable beam too.
			red := false
			for y := 0; y < 12; y++ {
				for x := 0; x < 12; x++ {
					o := 4 * (y*v.Width + x)
					if r, g := v.Pixels[o], v.Pixels[o+1]; int(r) > int(g)+80 {
						red = true
					}
				}
			}
			if !red {
				t.Error("no mouse pointer in the top left corner")
			}
			if p := os.Getenv("BEAM_PNG"); p != "" {
				savePNG(t, a, p+"-"+m.name+".png")
			}
		})
	}
}

// ECS super-hires shows its first pixel 5 lores pixels after DDFSTRT.
// With the hires delay the picture sat a word to the right and its last
// 16 pixels, the window's right border with its scroll gadgets, wrapped
// round to the left edge.
func TestWorkbenchSuperHiresECS(t *testing.T) {
	a := workbenchInModeOn(t, "ecs", 0x00029020, 1280, 256, 2, nil)
	v := a.Video
	px := func(x, y int) [3]byte {
		o := 4 * (y*v.Width + x)
		return [3]byte{v.Pixels[o], v.Pixels[o+1], v.Pixels[o+2]}
	}
	y := v.Height * 3 / 4
	left, right := px(4, y), px(v.Width-4, y)
	if left == right {
		t.Errorf("the left edge shows the right border's colour %v: the picture is a word off", right)
	}
	if p := os.Getenv("BEAM_PNG"); p != "" {
		savePNG(t, a, p+"-shres-ecs.png")
	}
}
