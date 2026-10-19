package amiga

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestGameBatch boots a list of games in parallel and writes contact
// sheets of screenshots, to triage which run. GOAMIGA_BATCH names a list
// file, one game per line (diag/games.txt is the collection's):
//
//	label | disk image | options
//
// options are comma-separated key=value:
//   - rom (path), cpu (68000/68020...), speed (CPU speed percent),
//     chipset, video (pal/ntsc), chip, slow and fast (KB), gayle (any
//     value: fit the A1200's IDE interface), frames;
//   - swap: a frame number changes DF0: to the next disk of a multi-disk
//     zip then, again half as long again later, round the disks; "auto"
//     changes it at a disk prompt, when the head has stepped a few times
//     with no transfer since the last and the drive has been quiet a
//     second (a game waiting for a disk polls by stepping);
//   - df1 (any value): put the second disk in DF1:;
//   - more: further disk files, ;-separated, for games zipped a disk to a
//     file;
//   - type: FRAME:TEXT types TEXT from that frame, a key every 10 frames
//     ("\n" is Return, US layout), for games that ask a question.
//
// The defaults are an A1200: rom=../ROMS/kick32a1200.rom cpu=68020
// chipset=aga chip=2048 slow=0 fast=0 frames=3000. Each game gets left
// clicks and joystick fire presses at intervals to get past title
// screens. Screenshots are taken at a third, two thirds and the end;
// GOAMIGA_OUT is the folder for the sheets (sheet-N.png, 8 games each;
// row numbers match the log).
//
//	GOAMIGA_BATCH=../diag/games.txt GOAMIGA_OUT=/tmp/sheets go test -tags noebitengine -run TestGameBatch -v ./amiga/
func TestGameBatch(t *testing.T) {
	list := os.Getenv("GOAMIGA_BATCH")
	if list == "" {
		t.Skip("set GOAMIGA_BATCH to a list of games")
	}
	out := os.Getenv("GOAMIGA_OUT")
	if out == "" {
		out = t.TempDir()
	}
	f, err := os.Open(list)
	if err != nil {
		t.Fatal(err)
	}
	type game struct {
		label, disk string
		opts        map[string]string
	}
	var games []game
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		g := game{label: strings.TrimSpace(parts[0]), opts: map[string]string{}}
		if len(parts) > 1 {
			g.disk = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			for _, kv := range strings.Split(parts[2], ",") {
				if k, v, ok := strings.Cut(strings.TrimSpace(kv), "="); ok {
					g.opts[k] = v
				}
			}
		}
		games = append(games, g)
	}
	f.Close()

	const shots = 3
	thumbs := make([][shots]*image.RGBA, len(games))
	notes := make([]string, len(games))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i, g := range games {
		wg.Add(1)
		go func(i int, g game) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					notes[i] = fmt.Sprintf("panic: %v", r)
				}
			}()
			opt := func(k, def string) string {
				if v, ok := g.opts[k]; ok {
					return v
				}
				return def
			}
			num := func(k, def string) int { n, _ := strconv.Atoi(opt(k, def)); return n }
			rom, err := LoadROM(opt("rom", "../ROMS/kick32a1200.rom"))
			if err != nil {
				notes[i] = err.Error()
				return
			}
			a, _ := New(rom, nil)
			if err := a.SetCPU(opt("cpu", "68020"), num("speed", "0")); err != nil {
				notes[i] = err.Error()
				return
			}
			a.SetChipset(opt("chipset", "aga"))
			if opt("gayle", "") != "" {
				a.EnableGayle()
			}
			if err := a.SetVideoStandard(opt("video", "pal")); err != nil {
				notes[i] = err.Error()
				return
			}
			if err := a.SetChipRAM(num("chip", "2048")); err != nil {
				notes[i] = err.Error()
				return
			}
			if err := a.ConfigureMemory(num("slow", "0"), num("fast", "0")); err != nil {
				notes[i] = err.Error()
				return
			}
			disks, err := DiskImagesIn(g.disk)
			// more: further disk files (games zipped a disk to a file),
			// separated by semicolons.
			for _, m := range strings.Split(opt("more", ""), ";") {
				if m = strings.TrimSpace(m); m != "" && err == nil {
					var ds []string
					if ds, err = DiskImagesIn(m); err == nil {
						disks = append(disks, ds...)
					}
				}
			}
			if err == nil {
				err = a.LoadDisk(disks[0])
			}
			if err != nil {
				notes[i] = err.Error()
				return
			}
			swapAt, next := num("swap", "0"), 1
			autoSwap, idle := opt("swap", "") == "auto", 0
			lastTransfers, stepsAtRead := 0, 0
			if opt("df1", "") != "" && len(disks) > 1 {
				if err := a.LoadDiskUnit(1, disks[1]); err != nil {
					notes[i] = err.Error()
					return
				}
			}
			a.Reset()
			a.Video.DisplayActive = true
			frames := num("frames", "3000")
			typeAt, typing := 0, []byte(nil)
			if at, text, ok := strings.Cut(opt("type", ""), ":"); ok {
				typeAt, _ = strconv.Atoi(at)
				typing = []byte(strings.ReplaceAll(text, `\n`, "\n"))
			}
			for fr := 1; fr <= frames; fr++ {
				if len(typing) > 0 && fr >= typeAt && (fr-typeAt)%10 == 0 {
					if code, shift, ok := amigaKey(typing[0]); ok {
						if shift {
							a.Keyboard.EnqueueKey(0x60, true)
						}
						a.Keyboard.EnqueueKey(code, true)
						a.Keyboard.EnqueueKey(code, false)
						if shift {
							a.Keyboard.EnqueueKey(0x60, false)
						}
					}
					typing = typing[1:]
				}
				phase := fr % 600
				a.Mouse.LeftButton = fr > 600 && phase >= 300 && phase < 304
				a.Joystick.Fire = fr > 600 && phase >= 450 && phase < 456
				if autoSwap && len(disks) > 1 {
					// Answer a disk prompt. A game waiting for a disk
					// polls the drive by stepping its head (which is how
					// the disk-change line clears) without reading; so
					// once the head has stepped a few times since the last
					// transfer, and the drive has been quiet for a second,
					// put in the next disk.
					if t := a.Disk.transfers; t != lastTransfers {
						lastTransfers, stepsAtRead = t, a.Disk.DF(0).steps
					}
					if a.Disk.DF(0).motor {
						idle = 0
					} else {
						idle++
					}
					if a.Disk.DF(0).steps-stepsAtRead >= 4 && idle >= 50 {
						a.SwapDisk(disks[next])
						next = (next + 1) % len(disks)
						stepsAtRead, idle = a.Disk.DF(0).steps, 0
					}
				}
				if fr == swapAt && len(disks) > 1 {
					a.SwapDisk(disks[next])
					// Later the one after, round to the first again.
					swapAt, next = swapAt+(swapAt+1)/2, (next+1)%len(disks)
				}
				if err := a.RunFrame(); err != nil {
					notes[i] = fmt.Sprintf("frame %d: %v", fr, err)
					break
				}
				for s := 1; s <= shots; s++ {
					if fr == frames*s/shots {
						thumbs[i][s-1] = thumbnail(a.Video.ScreenImage())
					}
				}
			}
			if notes[i] == "" {
				notes[i] = fmt.Sprintf("ran; PC=$%06X SR=$%04X", a.CPU.PC, a.CPU.SR)
			}
		}(i, g)
	}
	wg.Wait()

	for i, g := range games {
		t.Logf("%2d %-40s %s", i, g.label, notes[i])
	}
	// Contact sheets: 8 rows of 3 thumbnails, the row number in dots.
	const tw, th, per = 320, 128, 8
	for s := 0; s*per < len(games); s++ {
		n := min(per, len(games)-s*per)
		sheet := image.NewRGBA(image.Rect(0, 0, 40+shots*(tw+4), n*(th+4)))
		for r := 0; r < n; r++ {
			gi := s*per + r
			y0 := r * (th + 4)
			drawNumber(sheet, 4, y0+4, gi)
			for k := 0; k < shots; k++ {
				if im := thumbs[gi][k]; im != nil {
					x0 := 40 + k*(tw+4)
					for y := 0; y < th; y++ {
						copy(sheet.Pix[(y0+y)*sheet.Stride+4*x0:], im.Pix[y*im.Stride:y*im.Stride+4*tw])
					}
				}
			}
		}
		p := filepath.Join(out, fmt.Sprintf("sheet-%d.png", s))
		fh, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(fh, sheet)
		fh.Close()
		t.Logf("wrote %s", p)
	}
}

// thumbnail halves the picture to 320x128 (quartering interlaced rows).
func thumbnail(src *image.RGBA) *image.RGBA {
	const w, h = 320, 128
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sb := src.Bounds()
	for y := 0; y < h; y++ {
		sy := y * sb.Dy() / h
		for x := 0; x < w; x++ {
			sx := x * sb.Dx() / w
			copy(dst.Pix[y*dst.Stride+4*x:y*dst.Stride+4*x+4], src.Pix[sy*src.Stride+4*sx:])
		}
	}
	return dst
}

// drawNumber draws n in a 3x5 pixel font, scaled up, at (x, y).
func drawNumber(img *image.RGBA, x, y, n int) {
	glyphs := [10][5]string{
		{"###", "#.#", "#.#", "#.#", "###"}, {".#.", "##.", ".#.", ".#.", "###"},
		{"###", "..#", "###", "#..", "###"}, {"###", "..#", "###", "..#", "###"},
		{"#.#", "#.#", "###", "..#", "..#"}, {"###", "#..", "###", "..#", "###"},
		{"###", "#..", "###", "#.#", "###"}, {"###", "..#", "..#", "..#", "..#"},
		{"###", "#.#", "###", "#.#", "###"}, {"###", "#.#", "###", "..#", "###"},
	}
	s := strconv.Itoa(n)
	for i, ch := range s {
		g := glyphs[ch-'0']
		for gy, row := range g {
			for gx, c := range row {
				if c != '#' {
					continue
				}
				for dy := 0; dy < 3; dy++ {
					for dx := 0; dx < 3; dx++ {
						img.Set(x+i*12+gx*3+dx, y+gy*3+dy, color.RGBA{255, 255, 0, 255})
					}
				}
			}
		}
	}
}
