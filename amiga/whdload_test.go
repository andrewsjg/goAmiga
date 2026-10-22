package amiga

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewsjg/goamiga/internal/whdload"
)

// WHDLoad (whdload.de, free) and game installs (whdload.de/games, each a
// slave and an imager for the original disks) live in ../WHDLoad.
const whdDir = "../WHDLoad"

// unlha unpacks an LhA archive into a new temporary folder.
func unlha(t *testing.T, archive string) string {
	t.Helper()
	if _, err := os.Stat(archive); err != nil {
		t.Skip(err)
	}
	lha, err := exec.LookPath("lha")
	if err != nil {
		t.Skip("lha not installed")
	}
	dir := t.TempDir()
	abs, _ := filepath.Abs(archive)
	// lha exits 1 here even when it unpacks everything.
	exec.Command(lha, "xqw="+dir, abs).Run()
	return dir
}

// copyTree copies a host folder into another.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
}

// whdMachine is an A1200 with 8 MB of fast RAM booting from a folder made
// of the Install 3.2 disk, WHDLoad's commands in C: and the game's install
// in SYS:Game, with the original disk (an ADF or IPF, or a zip of one) in
// DF0:. The Startup-Sequence runs
// script (in SYS:Game) instead of Workbench, each command's output going
// to a log in SYS:.
type whdMachine struct {
	a    *Amiga
	boot string
}

func newWHDMachine(t *testing.T, game, disk string, script []string) *whdMachine {
	t.Helper()
	usr := unlha(t, filepath.Join(whdDir, "WHDLoad_usr.lha"))
	inst := unlha(t, filepath.Join(whdDir, "installs", game+".lha"))
	rom, err := LoadROM("../ROMS/kick32a1200.rom")
	if err != nil {
		t.Skip(err)
	}
	img, err := ReadDiskImage(disk)
	if err != nil {
		t.Skip(err)
	}

	boot := filepath.Join(t.TempDir(), "Boot")
	os.MkdirAll(boot, 0o755)
	adfExtract(t, readDisk(t, "../Workbench/Install3.2.adf"), boot)
	for _, c := range []string{"WHDLoad", "RawDIC", "DIC", "Patcher"} {
		b, err := os.ReadFile(filepath.Join(usr, "WHDLoad", "C", c))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(boot, "C", c), b, 0o755)
	}
	// The install's folder (LemmingsHD, TurricanInstall ...) is the game's.
	entries, _ := os.ReadDir(inst)
	for _, e := range entries {
		if e.IsDir() {
			copyTree(t, filepath.Join(inst, e.Name()), filepath.Join(boot, "Game"))
		}
	}
	// Kickstart images for the slaves that run the game on the OS
	// (kickemu): the user's own ROMs, by the names WHDLoad looks for, and
	// the relocation tables WHDLoad needs with them (Aminet
	// util/boot/skick346.lha).
	os.MkdirAll(filepath.Join(boot, "Devs", "Kickstarts"), 0o755)
	for from, to := range map[string]string{"../ROMS/Kick13.rom": "kick34005.A500"} {
		if b, err := os.ReadFile(from); err == nil {
			os.WriteFile(filepath.Join(boot, "Devs", "Kickstarts", to), b, 0o644)
		}
	}
	if _, err := os.Stat(filepath.Join(whdDir, "skick346.lha")); err == nil {
		rtb := unlha(t, filepath.Join(whdDir, "skick346.lha"))
		files, _ := filepath.Glob(filepath.Join(rtb, "Kickstarts", "*.RTB"))
		for _, f := range files {
			b, _ := os.ReadFile(f)
			os.WriteFile(filepath.Join(boot, "Devs", "Kickstarts", filepath.Base(f)), b, 0o644)
		}
	}
	ss := []string{
		"FixRomLibs 47", "SetPatch >NIL:",
		"MakeDir RAM:T RAM:ENV", "Assign ENV: RAM:ENV", "Assign T: RAM:T",
		"CD SYS:Game",
		"Wait 5",
	}
	ss = append(ss, script...)
	os.WriteFile(filepath.Join(boot, "S", "Startup-Sequence"), []byte(strings.Join(ss, "\n")+"\n"), 0o644)

	a, _ := New(rom, nil)
	a.SetCPU("68020", 0)
	a.SetChipset("aga")
	a.SetChipRAM(2048)
	a.ConfigureMemory(0, 8192)
	if err := a.AttachDirectory(boot, "", false); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("HOSTFS_TRACE") != "" {
		a.HardDisk.Trace = logWriter{t}
	}
	a.Video.DisplayActive = true
	a.Reset()
	// The disk goes in once Kickstart has booted from the folder (a
	// bootable floppy in DF0: would boot instead); the script waits for it.
	m := &whdMachine{a, boot}
	m.frames(150)
	if err := a.Disk.DF(0).InsertImage(img); err != nil {
		t.Fatal(err)
	}
	return m
}

// log returns a log file the script wrote in SYS:.
func (m *whdMachine) log(name string) string {
	b, _ := os.ReadFile(filepath.Join(m.boot, name))
	return string(b)
}

func (m *whdMachine) frames(n int) {
	for i := range n {
		if err := m.a.RunFrame(); err != nil {
			panic(fmt.Sprintf("frame %d: %v (PC $%08X)", i, err, m.a.CPU.PC))
		}
	}
}

// Bubble Bobble (Taito, SPS 1342), installed from its original disk as
// WHDLoad's install does it: the disk is an AmigaDOS disk, whose files are
// copied to the game's folder. WHDLoad then runs the game from them.
func TestWHDLoadBubbleBobble(t *testing.T) {
	m := newWHDMachine(t, "BubbleBobble", "../ADF/Bubble Bobble (1988)(Taito)(US)[1342].zip", []string{
		`Copy "Bubble Bobble:~(c|s|Trashcan|.#?|#?.info)" SYS:Game/data ALL QUIET >SYS:copy.log`,
		"List SYS:Game/data >SYS:list.log",
		"WHDLoad BubbleBobble.slave PRELOAD >SYS:whdload.log",
		"Echo \"back $RC\" >SYS:after.log",
	})
	m.frames(3000)
	savePNG(t, m.a, "/tmp/whd_bubble.png")
	for _, l := range []string{"copy.log", "whdload.log"} {
		t.Logf("%s:\n%s", l, m.log(l))
	}
	if !strings.Contains(m.log("list.log"), "bubcode") {
		t.Fatalf("the game's files weren't copied from the disk:\n%s", m.log("list.log"))
	}
	if m.log("after.log") != "" {
		t.Fatalf("WHDLoad ended before F10: %q", m.log("after.log"))
	}
	// F10, WHDLoad's quit key, gives the machine back to AmigaDOS.
	m.a.Keyboard.EnqueueKey(0x59, true)
	m.frames(5)
	m.a.Keyboard.EnqueueKey(0x59, false)
	m.frames(300)
	if got := m.log("after.log"); !strings.Contains(got, "back") {
		savePNG(t, m.a, "/tmp/whd_bubble_quit.png")
		t.Fatalf("F10 didn't return to AmigaDOS (after.log %q)", got)
	}
	t.Logf("after F10: %q", m.log("after.log"))
}

// SDI (Mindscape, SPS 720), installed as its WHDLoad install does it: the
// pictures and prog files from the disk called "sdi" go in data.
func TestWHDLoadSDI(t *testing.T) {
	m := newWHDMachine(t, "SDI", "../ADF/SDI (1987)(Mindscape)[0720].zip", []string{
		`Copy "sdi:(pictures|prog)" SYS:Game/data ALL QUIET >SYS:copy.log`,
		"List SYS:Game/data >SYS:list.log",
		"WHDLoad SDI.slave PRELOAD >SYS:whdload.log",
	})
	m.frames(4500) // copying from the floppy takes most of a minute
	savePNG(t, m.a, "/tmp/whd_sdi.png")
	for _, l := range []string{"copy.log", "list.log", "whdload.log"} {
		t.Logf("%s:\n%s", l, m.log(l))
	}
}

// The WHDLoad booter (internal/whdload): a game's folder, with its icon,
// boots straight into the game on a machine with no Workbench at all,
// from a boot folder with WHDLoad and a Startup-Sequence for the slave the
// icon names.
func TestWHDLoadBooter(t *testing.T) {
	// The game's folder, installed from its original as before.
	inst := newWHDMachine(t, "BubbleBobble", "../ADF/Bubble Bobble (1988)(Taito)(US)[1342].zip", []string{
		`Copy "Bubble Bobble:~(c|s|Trashcan|.#?|#?.info)" SYS:Game/data ALL QUIET`,
		"Echo done >SYS:done.log",
	})
	for i := 0; i < 60 && inst.log("done.log") == ""; i++ {
		inst.frames(100)
	}
	game := filepath.Join(inst.boot, "Game")
	icon, _ := os.ReadFile(filepath.Join(game, "BubbleBobble.newicon"))
	os.WriteFile(filepath.Join(game, "BubbleBobble.info"), icon, 0o644)

	// WHDLoad's home, as the booter leaves it after its downloads.
	home := t.TempDir()
	usr := unlha(t, filepath.Join(whdDir, "WHDLoad_usr.lha"))
	copyTree(t, filepath.Join(usr, "WHDLoad", "C"), filepath.Join(home, "c"))
	os.Rename(filepath.Join(home, "c", "WHDLoad"), filepath.Join(home, "WHDLoad"))
	if _, err := os.Stat(filepath.Join(whdDir, "skick346.lha")); err == nil {
		copyTree(t, filepath.Join(unlha(t, filepath.Join(whdDir, "skick346.lha")), "Kickstarts"), filepath.Join(home, "Kickstarts"))
	}

	g, err := whdload.Prepare(home, game, []string{"../ROMS/Kick13.rom"}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	ss, _ := os.ReadFile(filepath.Join(g.Boot, "S", "Startup-Sequence"))
	t.Logf("slave %s %v, kickstarts %v, Startup-Sequence:\n%s", g.Slave, g.Args, g.Kicks, ss)
	if g.Slave != "BubbleBobble.slave" || len(g.Kicks) != 1 {
		t.Fatalf("slave %q, kickstarts %v", g.Slave, g.Kicks)
	}

	rom, _ := LoadROM("../ROMS/kick32a1200.rom")
	a, _ := New(rom, nil)
	a.SetCPU("68020", 0)
	a.SetChipset("aga")
	a.SetChipRAM(2048)
	a.ConfigureMemory(0, 8192)
	a.AttachDirectory(g.Boot, "WHDBoot", true)
	a.HardDisk.Dirs[0].BootPri = 10
	a.AttachDirectory(g.Dir, "Game", false)
	// A bootable floppy in DF0: doesn't get in the way.
	if img, err := ReadDiskImage("../Workbench/Install3.2.adf"); err == nil {
		a.Disk.DF(0).InsertImage(img)
	}
	a.Video.DisplayActive = true
	a.Reset()
	m := &whdMachine{a: a}
	m.frames(1500)
	savePNG(t, a, "/tmp/whd_booter.png")
	if a.CPU.PC>>16 == 0xF8 && a.cpuStopped {
		t.Error("the machine is idle in the ROM: the game didn't start")
	}
}
