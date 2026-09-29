package amiga

import (
	"bytes"
	"fmt"
	"os"
	"testing"
)

// machineFingerprint is what must match after identical runs.
type machineFingerprint struct {
	pixels []byte
	chip   []byte
	cpu    string
	beam   [2]int
	cycles uint64
}

func fingerprint(a *Amiga) machineFingerprint {
	chip := make([]byte, a.chipSize)
	a.ChipRAM.Read(0, chip)
	st := a.CPU.State()
	return machineFingerprint{
		pixels: append([]byte(nil), a.Video.Pixels...),
		chip:   chip,
		cpu:    fmtAny(st),
		beam:   [2]int{a.hpos, a.vpos},
		cycles: a.cycles,
	}
}

func compareFingerprints(t *testing.T, a, b machineFingerprint) {
	t.Helper()
	if a.cpu != b.cpu {
		t.Errorf("CPU differs:\n saved run  %s\n loaded run %s", a.cpu, b.cpu)
	}
	if a.beam != b.beam || a.cycles != b.cycles {
		t.Errorf("beam/cycles differ: %v %d vs %v %d", a.beam, a.cycles, b.beam, b.cycles)
	}
	if !bytes.Equal(a.chip, b.chip) {
		n := 0
		for i := range a.chip {
			if a.chip[i] != b.chip[i] {
				n++
			}
		}
		t.Errorf("chip RAM differs in %d bytes", n)
	}
	if !bytes.Equal(a.pixels, b.pixels) {
		t.Error("screens differ")
	}
}

// Save, run on; load the state into a new machine and run the same: the
// two must end identical. Any state the save misses shows up here.
func TestSaveStateResumesExactly(t *testing.T) {
	cases := []struct {
		name, rom, floppy, hdf string
		bootFrames             int
	}{
		// Mid-boot from floppy: disk DMA, drive motor, trackdisk.
		{"KS1.3 floppy", "../ROMS/Kick13.rom", "../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf", "", 150},
		// Workbench 3.2 from a hardfile, with fast RAM and the controller.
		{"KS3.2 hardfile", "../ROMS/Kick32.rom", "", "../disks/Install3.2.adf", 350},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rom, err := LoadROM(c.rom)
			if err != nil {
				t.Skip(err)
			}
			var hdfPath string
			var hdfBytes []byte
			if c.hdf != "" {
				hdfPath = hardfileFromADF(t, c.hdf)
			}
			newMachine := func() *Amiga {
				a, _ := New(rom, nil)
				a.SetChipset("ecs-agnus")
				a.SetChipRAM(1024)
				a.ConfigureMemory(0, 2048)
				if hdfPath != "" {
					if err := a.AttachHardfile(hdfPath, false); err != nil {
						t.Fatal(err)
					}
				}
				a.Video.DisplayActive = true
				return a
			}

			a := newMachine()
			if c.floppy != "" {
				data, err := os.ReadFile(c.floppy)
				if err != nil {
					t.Skip(err)
				}
				a.Disk.InsertDisk(data)
			}
			a.Reset()
			for i := 0; i < c.bootFrames; i++ {
				a.RunFrame()
			}
			var state bytes.Buffer
			if err := a.SaveState(&state); err != nil {
				t.Fatal(err)
			}
			t.Logf("state: %d KB", state.Len()/1024)
			if hdfPath != "" {
				hdfBytes, _ = os.ReadFile(hdfPath)
			}
			for i := 0; i < 300; i++ {
				a.RunFrame()
			}
			want := fingerprint(a)

			if hdfPath != "" {
				os.WriteFile(hdfPath, hdfBytes, 0o644) // hard disks aren't in the state
			}
			b := newMachine()
			b.Reset()
			if err := b.LoadState(bytes.NewReader(state.Bytes())); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 300; i++ {
				b.RunFrame()
			}
			compareFingerprints(t, want, fingerprint(b))
		})
	}
}

// Loading refuses a state from a different ROM, and garbage.
func TestLoadStateChecks(t *testing.T) {
	rom13, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	rom32, err := LoadROM("../ROMS/Kick32.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom13, nil)
	a.Reset()
	var state bytes.Buffer
	if err := a.SaveState(&state); err != nil {
		t.Fatal(err)
	}
	b, _ := New(rom32, nil)
	b.Reset()
	if err := b.LoadState(bytes.NewReader(state.Bytes())); err == nil {
		t.Error("loaded a Kickstart 1.3 state into a Kickstart 3.2 machine")
	} else {
		t.Log(err)
	}
	if err := b.LoadState(bytes.NewReader([]byte("not a state"))); err == nil {
		t.Error("loaded garbage")
	}
}

func fmtAny(v interface{}) string { return fmt.Sprintf("%+v", v) }

// Sound playing across a save: the loaded machine produces the same
// samples (Paula's channels, filters and sample phase are all in the
// state).
func TestSaveStateResumesSound(t *testing.T) {
	newMachine := func() *Amiga {
		a := createTestAmiga(t)
		a.wireMemoryMap(false)
		a.Paula.Output = NewAudioBuffer(48000)
		return a
	}
	a := newMachine()
	wave := make([]byte, 64)
	for i := range wave {
		wave[i] = byte(int8(i*8 - 128))
	}
	a.ChipRAM.Write(0x8000, wave)
	for reg, v := range map[int]uint16{
		0x0A0: 0x0000, 0x0A2: 0x8000, // AUD0LC
		0x0A4: 32,  // AUD0LEN (words)
		0x0A6: 200, // AUD0PER
		0x0A8: 64,  // AUD0VOL
	} {
		writeCustom16(a, reg, v)
	}
	writeCustom16(a, 0x096, 0x8201) // DMAEN + AUD0EN
	for i := 0; i < 7; i++ {
		a.RunFrame()
	}
	a.advanceBeamPerCycle(12345) // mid-frame, mid-sample
	var state bytes.Buffer
	if err := a.SaveState(&state); err != nil {
		t.Fatal(err)
	}
	record := func(m *Amiga) []float32 {
		m.Paula.Output.SetMuted(false)
		for i := 0; i < 5; i++ {
			m.RunFrame()
		}
		out := make([]float32, 2*m.Paula.Output.Frames())
		m.Paula.Output.ReadFrames(out)
		return out
	}
	want := record(a)
	b := newMachine()
	if err := b.LoadState(bytes.NewReader(state.Bytes())); err != nil {
		t.Fatal(err)
	}
	got := record(b)
	if len(want) == 0 {
		t.Fatal("no sound was produced")
	}
	if len(got) != len(want) {
		t.Fatalf("%d samples after load, want %d", len(got)/2, len(want)/2)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d = %v after load, want %v", i/2, got[i], want[i])
		}
	}
}
