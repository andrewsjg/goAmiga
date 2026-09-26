package amiga

import (
	"fmt"
	"testing"
)

// Inspect outline connectivity just before first Flood.
func TestOutlineGaps(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	const flood = uint32(0xFC5AD0)
	const bpl1 = 0xA572
	bpr := 40
	width, rows := 320, 200

	readPen := func(x, y int) int {
		byteOff := y*bpr + x/8
		bit := uint(7 - (x % 8))
		buf := make([]byte, 1)
		a.ChipRAM.Read(bpl1+byteOff, buf)
		pen := 0
		if buf[0]&(1<<bit) != 0 {
			pen |= 1
		}
		a.ChipRAM.Read(0xC4B2+byteOff, buf)
		if buf[0]&(1<<bit) != 0 {
			pen |= 2
		}
		return pen
	}

	for i := 0; i < 3_000_000; i++ {
		if a.CPU.PC == flood {
			sx, sy := int(a.CPU.D[0]), int(a.CPU.D[1])
			t.Logf("Flood seed (%d,%d) pen=%d mode=%d", sx, sy, readPen(sx, sy), a.CPU.D[2])

			// BFS from seed on color-0, count reachable + find if hits border
			type pt struct{ x, y int }
			vis := make([]bool, width*rows)
			q := []pt{{sx, sy}}
			vis[sy*width+sx] = true
			reach := 0
			hitBorder := false
			minx, miny, maxx, maxy := sx, sy, sx, sy
			for len(q) > 0 {
				p := q[0]
				q = q[1:]
				reach++
				if p.x == 0 || p.y == 0 || p.x == width-1 || p.y == rows-1 {
					hitBorder = true
				}
				if p.x < minx {
					minx = p.x
				}
				if p.y < miny {
					miny = p.y
				}
				if p.x > maxx {
					maxx = p.x
				}
				if p.y > maxy {
					maxy = p.y
				}
				for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := p.x+d[0], p.y+d[1]
					if nx < 0 || ny < 0 || nx >= width || ny >= rows {
						continue
					}
					i := ny*width + nx
					if vis[i] {
						continue
					}
					if readPen(nx, ny) != 0 {
						continue // blocked by outline
					}
					vis[i] = true
					q = append(q, pt{nx, ny})
				}
			}
			t.Logf("color-0 component from seed: %d pixels, hitBorder=%v bbox=(%d,%d)-(%d,%d)",
				reach, hitBorder, minx, miny, maxx, maxy)

			// Count outline pixels and sample rows with bits
			outline := 0
			for y := 0; y < rows; y++ {
				for x := 0; x < width; x++ {
					if readPen(x, y) != 0 {
						outline++
					}
				}
			}
			t.Logf("total nonzero pixels before flood: %d", outline)

			// ASCII of region around seed
			t.Log("neighborhood around seed (5=seed, #=outline, .=empty):")
			for y := sy - 15; y <= sy+15; y++ {
				if y < 0 || y >= rows {
					continue
				}
				line := ""
				for x := sx - 20; x <= sx+20; x++ {
					if x < 0 || x >= width {
						line += " "
						continue
					}
					if x == sx && y == sy {
						line += "S"
					} else if readPen(x, y) != 0 {
						line += "#"
					} else {
						line += "."
					}
				}
				t.Logf("  %3d: %s", y, line)
			}

			// Full-screen ASCII tiny
			t.Log("full screen outline map:")
			for y := 0; y < rows; y += 4 {
				line := ""
				for x := 0; x < width; x += 4 {
					on := false
					for dy := 0; dy < 4 && y+dy < rows; dy++ {
						for dx := 0; dx < 4 && x+dx < width; dx++ {
							if readPen(x+dx, y+dy) != 0 {
								on = true
							}
						}
					}
					if on {
						line += "#"
					} else {
						line += "."
					}
				}
				t.Log(line)
			}
			break
		}
		a.Step()
	}
	_ = fmt.Sprintf
}
