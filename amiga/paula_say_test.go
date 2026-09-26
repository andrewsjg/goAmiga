package amiga

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"testing"
)

// writeWAV saves stereo float samples as 16-bit PCM.
func writeWAV(path string, samples []float32) error {
	var b bytes.Buffer
	put := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	put(uint32(36 + 2*len(samples)))
	b.WriteString("WAVEfmt ")
	put(uint32(16))
	put(uint16(1)) // PCM
	put(uint16(2)) // stereo
	put(uint32(AudioSampleRate))
	put(uint32(AudioSampleRate * 4))
	put(uint16(4))
	put(uint16(16))
	b.WriteString("data")
	put(uint32(2 * len(samples)))
	for _, s := range samples {
		put(int16(math.Max(-1, math.Min(1, float64(s))) * 32767))
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// The Workbench disk's Say command speaks through narrator.device and the
// ROM's audio.device: audio DMA, AUDx interrupts and all. The startup-
// sequence's first echo is patched to `sys:Utilities/Say hello`. If audio
// interrupts were missing, Say would never finish and Workbench would not
// start. With GOAMIGA_DIAG=1 the sound is saved to /tmp/goamiga_say.wav.
func TestSayHello(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	adf, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	b := adf[1184*512 : 1185*512] // s/startup-sequence, first data block
	old := []byte(`echo "Amiga Workbench Disk. Release 1.3.3 version 34.34"`)
	i := bytes.Index(b, old)
	if i < 0 {
		t.Skip("startup-sequence not where expected on this ADF")
	}
	cmd := []byte(`sys:Utilities/Say hello`)
	copy(b[i:], append(cmd, bytes.Repeat([]byte(" "), len(old)-len(cmd))...))
	ofsFixChecksum(b)

	a, _ := New(rom, nil)
	a.Disk.InsertDisk(adf)
	a.Paula.Output = NewAudioBuffer(80 * AudioSampleRate)
	a.Reset()
	booted := false
	periods := map[uint16]int{}
	for i := 0; i < 30_000_000 && !booted; i++ {
		if c := a.Paula.ch[0]; c.dma && i%64 == 0 {
			periods[c.per]++
		}
		if a.CPU.PC == 0x055C {
			np, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			booted = readCStrMem(a, uint32(np), 24) == "workbench.task"
		}
		if err := a.Step(); err != nil {
			t.Fatalf("boot: %v", err)
		}
	}
	t.Logf("audio interrupts per channel: %v; AUD0PER while playing: %v", a.Paula.Interrupts, periods)
	if !booted {
		t.Fatal("Workbench did not start: Say never finished")
	}

	samples := make([]float32, 2*a.Paula.Output.Frames())
	a.Paula.Output.ReadFrames(samples)
	// Speech: 10 ms windows with RMS above 1% of full scale. "hello" at
	// narrator's default rate is about 0.4 s long.
	const win = AudioSampleRate / 100
	active, peak, first, last := 0, 0.0, -1, -1
	for w := 0; (w+1)*win*2 <= len(samples); w++ {
		var sum float64
		for _, s := range samples[w*win*2 : (w+1)*win*2] {
			sum += float64(s) * float64(s)
			peak = math.Max(peak, math.Abs(float64(s)))
		}
		if math.Sqrt(sum/(2*win)) > 0.01 {
			active++
			if first < 0 {
				first = w
			}
			last = w
		}
	}
	t.Logf("%.1f s of audio; speech %.2f s starting at %.2f s, peak %.2f",
		float64(len(samples)/2)/AudioSampleRate, float64(active)/100, float64(first)/100, peak)
	if active < 20 || peak < 0.2 {
		t.Errorf("no speech in the output (%d active windows, peak %.2f)", active, peak)
	}
	total := 0
	for _, n := range a.Paula.Interrupts {
		total += n
	}
	if total < 10 {
		t.Errorf("only %d audio interrupts", total)
	}
	if os.Getenv("GOAMIGA_DIAG") != "" {
		// Just the speech, with half a second either side.
		from := max(0, (first-50)*win*2)
		to := min(len(samples), (last+51)*win*2)
		if err := writeWAV("/tmp/goamiga_say.wav", samples[from:to]); err == nil {
			t.Logf("wrote /tmp/goamiga_say.wav (%.1f s)", float64((to-from)/2)/AudioSampleRate)
		}
	}
}
