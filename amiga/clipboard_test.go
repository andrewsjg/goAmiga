package amiga

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClipboard stands in for the host's clipboard.
type fakeClipboard struct {
	count int
	text  string
	has   bool
}

func (f *fakeClipboard) ChangeCount() int     { return f.count }
func (f *fakeClipboard) Text() (string, bool) { return f.text, f.has }
func (f *fakeClipboard) SetText(s string)     { f.text, f.has = s, true; f.count++ }
func (f *fakeClipboard) copy(s string)        { f.SetText(s) }

// clipMachine boots Workbench 3.2 on an A1200 from a folder, with host
// clipboard sharing.
func clipMachine(t *testing.T, host *fakeClipboard) (*Amiga, string) {
	t.Helper()
	rom, err := LoadROM("../ROMS/kick32a1200.rom")
	if err != nil {
		t.Skip(err)
	}
	// The Install disk boots to Workbench; clipboard.device, ConClip (the
	// Shell's copy and paste) and NewShell come from the Workbench disk.
	boot := filepath.Join(t.TempDir(), "Boot")
	adfExtract(t, readDisk(t, "../Workbench/Install3.2.adf"), boot)
	wb := readDisk(t, "../Workbench/Workbench3.2.adf")
	for _, f := range []string{"Devs/clipboard.device", "C/ConClip", "C/NewShell"} {
		os.WriteFile(filepath.Join(boot, filepath.FromSlash(f)), adfRead(wb, f), 0o755)
	}
	ssPath := filepath.Join(boot, "S", "Startup-Sequence")
	ss, _ := os.ReadFile(ssPath)
	os.WriteFile(ssPath, []byte(strings.Replace(string(ss), "LoadWB",
		"MakeDir RAM:Clipboards\nAssign CLIPS: RAM:Clipboards\nRun >NIL: C:ConClip\nLoadWB", 1)), 0o644)
	a, _ := New(rom, nil)
	a.SetCPU("68020", 0)
	a.SetChipset("aga")
	a.SetChipRAM(2048)
	a.ConfigureMemory(0, 4096)
	a.HostClipboard = host
	if err := a.AttachDirectory(boot, "", false); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("HD_TRACE") != "" {
		a.HardDisk.Trace = logWriter{t}
	}
	a.Video.DisplayActive = true
	a.Reset()
	return a, boot
}

// Text copied on the host becomes the Amiga's clip, and the Amiga's clip
// comes back to the host: clipboard.device holds it (as FTXT, in
// ISO-8859-1), and when the helper forgets which clip it saw last, it reads
// the device's current clip and hands it to the host.
func TestClipboardRoundTrip(t *testing.T) {
	host := &fakeClipboard{}
	a, _ := clipMachine(t, host)
	m := &whdMachine{a: a}
	m.frames(600) // Workbench up, the helper running
	c := &a.HardDisk.clip
	if c.procStep != clipProcIdle && c.procStep != clipProcDelay {
		t.Fatalf("the helper isn't running: step %d/%d", c.step, c.procStep)
	}
	const text = "Hello from the host, café\n"
	host.copy(text)
	m.frames(50)
	if c.lastText != text {
		t.Fatalf("the helper didn't take the host's text (last %q)", c.lastText)
	}

	// Forget it on both sides: the host's clipboard changes to something
	// that isn't text, and the helper looks at the Amiga's clip afresh.
	host.text, host.has = "", false
	c.lastID, c.lastText = 0xFFFFFFFF, ""
	m.frames(50)
	if host.text != text {
		t.Errorf("the host got %q back from the Amiga's clip, want %q", host.text, text)
	}
}

// usRawKeys are the raw key codes of the US keymap's letters, space and
// Return, for typing in tests.
var usRawKeys = map[rune]byte{
	'q': 0x10, 'w': 0x11, 'e': 0x12, 'r': 0x13, 't': 0x14, 'y': 0x15, 'u': 0x16, 'i': 0x17, 'o': 0x18, 'p': 0x19,
	'a': 0x20, 's': 0x21, 'd': 0x22, 'f': 0x23, 'g': 0x24, 'h': 0x25, 'j': 0x26, 'k': 0x27, 'l': 0x28,
	'z': 0x31, 'x': 0x32, 'c': 0x33, 'v': 0x34, 'b': 0x35, 'n': 0x36, 'm': 0x37, ' ': 0x40, '\n': 0x44,
}

// typeText types lowercase text on the Amiga's keyboard.
func typeText(m *whdMachine, s string) {
	for _, r := range s {
		k := usRawKeys[r]
		m.a.Keyboard.EnqueueKey(k, true)
		m.frames(2)
		m.a.Keyboard.EnqueueKey(k, false)
		m.frames(2)
	}
}

// Text copied in a Shell on the Amiga (marked with the mouse, Right
// Amiga+C: ConClip puts it in clipboard.device) reaches the host, and text
// copied on the host can be pasted in the Shell (Right Amiga+V).
func TestClipboardShellCopy(t *testing.T) {
	host := &fakeClipboard{}
	a, boot := clipMachine(t, host)
	ss := filepath.Join(boot, "S", "Startup-Sequence")
	b, _ := os.ReadFile(ss)
	os.WriteFile(ss, []byte(strings.Replace(string(b), "LoadWB", "LoadWB\nNewShell CON:0/40/640/100/Clip", 1)), 0o644)
	m := &whdMachine{a: a}
	m.frames(700)
	typeText(m, "echo goamiga")
	m.frames(20)
	savePNG(t, a, "/tmp/clip_shell.png")
	// The prompt "3> " then the text, in topaz 8 (8 pixels a character),
	// on the window's first line.
	d := newRTGDesk(t, a)
	y := 40 + 11 + 3
	d.drag(false, 4+3*8+1, y, 4+(3+12)*8-2, y)
	a.Keyboard.EnqueueKey(0x67, true) // Right Amiga
	m.frames(2)
	a.Keyboard.EnqueueKey(0x33, true) // C
	m.frames(2)
	a.Keyboard.EnqueueKey(0x33, false)
	a.Keyboard.EnqueueKey(0x67, false)
	m.frames(60)
	savePNG(t, a, "/tmp/clip_shell2.png")
	if host.text != "echo goamiga" {
		t.Errorf("the host's clipboard holds %q, want the text copied in the Shell", host.text)
	}

	// And the other way: a command copied on the host, pasted in the Shell
	// (Right Amiga+V) and run.
	typeText(m, "\n")
	host.copy("echo pasted >SYS:pasted")
	m.frames(30)
	a.Keyboard.EnqueueKey(0x67, true)
	m.frames(2)
	a.Keyboard.EnqueueKey(0x34, true) // V
	m.frames(2)
	a.Keyboard.EnqueueKey(0x34, false)
	a.Keyboard.EnqueueKey(0x67, false)
	m.frames(20)
	typeText(m, "\n")
	m.frames(60)
	if b, err := os.ReadFile(filepath.Join(boot, "pasted")); err != nil || string(b) != "pasted\n" {
		savePNG(t, a, "/tmp/clip_shell3.png")
		t.Errorf("the pasted command didn't run: %q, %v", b, err)
	}
}

// With no hard disk or folder of its own, a Workbench floppy still gets
// clipboard sharing: goAmiga's volume alone carries the board the helper
// lives on, and the floppy still boots.
func TestClipboardFloppyWorkbench(t *testing.T) {
	rom, err := LoadROM("../ROMS/kick32a1200.rom")
	if err != nil {
		t.Skip(err)
	}
	// The Workbench disk, made bootable with the Install disk's standard
	// bootblock (this copy has none).
	wb := readDisk(t, "../Workbench/Workbench3.2.adf")
	copy(wb[:1024], readDisk(t, "../Workbench/Install3.2.adf")[:1024])
	img, err := diskImageFrom(wb)
	if err != nil {
		t.Fatal(err)
	}
	vol := filepath.Join(t.TempDir(), "goAmiga")
	for name, b := range AmigaFiles() {
		p := filepath.Join(vol, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, b, 0o644)
	}
	host := &fakeClipboard{}
	a, _ := New(rom, nil)
	a.SetCPU("68020", 0)
	a.SetChipset("aga")
	a.SetChipRAM(2048)
	a.HostClipboard = host
	a.AttachDirectory(vol, "goAmiga", true)
	a.Disk.DF(0).InsertImage(img)
	a.Video.DisplayActive = true
	a.Reset()
	m := &whdMachine{a: a}
	m.frames(1500) // a floppy boot takes a while
	c := &a.HardDisk.clip
	if c.procStep < clipProcDelay || c.procStep == clipProcDead { // past opening the device
		savePNG(t, a, "/tmp/clip_floppy.png")
		t.Fatalf("the helper isn't running: step %d/%d", c.step, c.procStep)
	}
	host.copy("from the host")
	for i := 0; i < 20 && c.lastText != "from the host"; i++ {
		m.frames(25)
	}
	if c.lastText != "from the host" {
		savePNG(t, a, "/tmp/clip_floppy.png")
		t.Fatalf("the helper didn't take the host's text (step %d)", c.procStep)
	}
	m.frames(50)
	c.lastID, c.lastText = 0xFFFFFFFF, ""
	host.text, host.has = "", false
	for i := 0; i < 20 && host.text == ""; i++ {
		m.frames(25)
	}
	if host.text != "from the host" {
		t.Errorf("round trip through the floppy Workbench's clipboard.device gave %q", host.text)
	}
}
