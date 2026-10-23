package amiga

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ahiUsr is AHI 6's user archive (Aminet driver/audio/m68k-amigaos-ahiusr).
const ahiUsr = "../AHI/m68k-amigaos-ahiusr.lha"

// ahiMachine boots Workbench 3.2 on an A1200 from a folder with AHI
// installed and goAmiga's volume beside it. Before Workbench, the
// volume's script installs goAmiga's driver, AUDIO: is mounted, and
// script runs.
func ahiMachine(t *testing.T, files map[string][]byte, script ...string) (*Amiga, string) {
	t.Helper()
	usr := unlha(t, ahiUsr)
	rom, err := LoadROM("../ROMS/kick32a1200.rom")
	if err != nil {
		t.Skip(err)
	}
	boot := filepath.Join(t.TempDir(), "Boot")
	adfExtract(t, readDisk(t, "../Workbench/Install3.2.adf"), boot)
	copyTree(t, filepath.Join(usr, "m68k-amigaos-ahi", "User", "Devs"), filepath.Join(boot, "Devs"))
	copyTree(t, filepath.Join(usr, "m68k-amigaos-ahi", "User", "C"), filepath.Join(boot, "C"))
	copyTree(t, filepath.Join(usr, "m68k-amigaos-ahi", "User", "L"), filepath.Join(boot, "L"))
	// Only goAmiga's modes: with no AHI preferences, AHI picks from those
	// it has.
	modes, _ := filepath.Glob(filepath.Join(boot, "Devs", "AudioModes", "*"))
	for _, m := range modes {
		os.Remove(m)
	}
	// goAmiga's volume, from which its script installs the driver.
	vol := filepath.Join(t.TempDir(), "goAmiga")
	for name, b := range AmigaFiles() {
		p := filepath.Join(vol, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, b, 0o644)
	}
	for name, b := range files {
		os.WriteFile(filepath.Join(boot, name), b, 0o644)
	}
	ss := filepath.Join(boot, "S", "Startup-Sequence")
	b, _ := os.ReadFile(ss)
	pre := append([]string{"Execute goAmiga:Install-AHI-Driver >SYS:install.log", "Mount DEVS:DOSDrivers/AUDIO"}, script...)
	os.WriteFile(ss, []byte(strings.Replace(string(b), "LoadWB", strings.Join(pre, "\n")+"\nLoadWB", 1)), 0o644)

	a, _ := New(rom, nil)
	a.SetCPU("68020", 0)
	a.SetChipset("aga")
	a.SetChipRAM(2048)
	a.ConfigureMemory(0, 8192)
	if err := a.AttachDirectory(boot, "", false); err != nil {
		t.Fatal(err)
	}
	a.AttachDirectory(vol, "goAmiga", true)
	if os.Getenv("HD_TRACE") != "" {
		a.HardDisk.Trace = logWriter{t}
	}
	a.Video.DisplayActive = true
	a.Reset()
	return a, boot
}

// tone is a mono 16-bit signed sine (big-endian, as the Amiga has it).
func tone(freq, rate float64, seconds float64) []byte {
	n := int(rate * seconds)
	b := make([]byte, 2*n)
	for i := range n {
		v := int16(12000 * math.Sin(2*math.Pi*freq*float64(i)/rate))
		binary.BigEndian.PutUint16(b[2*i:], uint16(v))
	}
	return b
}

// A tone played through AHI (AUDIO:, AHI's DOS device) with goAmiga's
// driver comes out on the host, at its pitch and in time: AHI mixes it on
// the Amiga and the driver hands it over.
func TestAHIPlaysOnTheHost(t *testing.T) {
	a, boot := ahiMachine(t, map[string][]byte{"tone.raw": tone(440, 22050, 3)},
		`Copy SYS:tone.raw "AUDIO:B/16/C/1/F/22050/T/SIGNED" >SYS:play.log`)
	m := &whdMachine{a: a}
	peak := 0.0
	rate := 0.0
	for i := 0; i < 40; i++ {
		before := a.Paula.ahi.pushed
		m.frames(25)
		if i >= 6 && i < 11 { // while the tone plays: AHI keeps pace with emulated time
			rate += float64(a.Paula.ahi.pushed-before) / 25 * a.FrameRate() / 5
		}
		peak = math.Max(peak, a.Paula.ahi.peak())
	}
	t.Logf("AHI delivered %.0f host samples a second while playing", rate)
	if math.Abs(rate-AudioSampleRate) > AudioSampleRate/50 {
		t.Errorf("AHI delivered %.0f samples a second, want %d", rate, AudioSampleRate)
	}
	for _, f := range []string{"install.log", "play.log"} {
		b, _ := os.ReadFile(filepath.Join(boot, f))
		t.Logf("%s: %q", f, b)
	}
	savePNG(t, a, "/tmp/ahi.png")
	t.Logf("AHI peak %.3f, %d host samples queued", peak, len(a.Paula.ahi.buf)-a.Paula.ahi.start)
	// AHI scales by its channel count (8 by default) so a full mix can't
	// clip: the tone's 0.37 comes out at about a tenth of that.
	if peak < 0.02 {
		t.Fatal("no sound from AHI reached the host")
	}
	// Its pitch, from the zero crossings of the queued samples.
	q := a.Paula.ahi.buf[a.Paula.ahi.start:]
	crossings, first, last := 0, -1, 0
	for i := 1; i < len(q); i++ {
		if q[i-1][0] < 0 && q[i][0] >= 0 {
			if first < 0 {
				first = i
			}
			crossings++
			last = i
		}
	}
	if crossings < 10 {
		t.Fatalf("only %d cycles of the tone queued", crossings)
	}
	freq := float64(crossings-1) * AudioSampleRate / float64(last-first)
	t.Logf("tone at %.1f Hz on the host", freq)
	if math.Abs(freq-440) > 5 {
		t.Errorf("tone at %.1f Hz, want 440", freq)
	}
}

// A state saved while AHI plays carries on playing when loaded.
func TestAHISaveStateWhilePlaying(t *testing.T) {
	a, _ := ahiMachine(t, map[string][]byte{"tone.raw": tone(440, 22050, 6)},
		`Copy SYS:tone.raw "AUDIO:B/16/C/1/F/22050/T/SIGNED"`)
	m := &whdMachine{a: a}
	m.frames(200) // playing
	var st bytes.Buffer
	if err := a.SaveState(&st); err != nil {
		t.Fatal(err)
	}
	m.frames(50)
	if err := a.LoadState(&st); err != nil {
		t.Fatal(err)
	}
	before := a.Paula.ahi.pushed
	m.frames(50)
	if got := a.Paula.ahi.pushed - before; got < 40000 {
		t.Errorf("%d host samples in the 50 frames after loading: AHI didn't carry on", got)
	}
}
