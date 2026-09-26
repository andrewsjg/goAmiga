package amiga

import (
	"math"
	"testing"
)

// paulaAmiga returns an Amiga with channel n set up to play words from
// $10000 at period per and full volume (DMA not yet enabled).
func paulaAmiga(t *testing.T, n int, per uint16, samples ...byte) *Amiga {
	t.Helper()
	a := createTestAmiga(t)
	a.ChipRAM.Write(0x10000, samples)
	base := 0x0A0 + 0x10*n
	a.Paula.WriteReg(base+0x0, 0x0001) // LCH
	a.Paula.WriteReg(base+0x2, 0x0000) // LCL
	a.Paula.WriteReg(base+0x4, uint16(len(samples)/2))
	a.Paula.WriteReg(base+0x6, per)
	a.Paula.WriteReg(base+0x8, 64)
	return a
}

func enableAudioDMA(a *Amiga, bits uint16) {
	a.Custom.Write(0x096, []byte{0x82, byte(bits)}) // SET | DMAEN | AUDxEN
}

func TestAudioDMAPlayback(t *testing.T) {
	a := paulaAmiga(t, 0, 200, 0x10, 0x20, 0x30, 0x40)
	enableAudioDMA(a, 0x01)
	a.Paula.Tick(0)
	if a.Paula.Interrupts[0] != 1 || a.Custom.ReadReg16(0x09C)&0x0080 == 0 {
		t.Fatal("no AUD0 interrupt when the buffer started")
	}
	// Samples in order, each held 200 colour clocks, then the buffer
	// repeats with a second interrupt.
	for i, want := range []int8{0x10, 0x20, 0x30, 0x40, 0x10} {
		if got := a.Paula.ch[0].sample; got != want {
			t.Errorf("sample %d = $%02X, want $%02X", i, got, want)
		}
		if i == 3 && a.Paula.Interrupts[0] != 1 {
			t.Errorf("interrupt before the buffer ended: %d", a.Paula.Interrupts[0])
		}
		a.Paula.Tick(200 * CPUCyclesPerColorClock)
	}
	if a.Paula.Interrupts[0] != 2 {
		t.Errorf("%d interrupts after one buffer, want 2", a.Paula.Interrupts[0])
	}

	// New location/length written after the interrupt take effect at the
	// next buffer.
	a.ChipRAM.Write(0x12000, []byte{0x55, 0x66})
	a.Paula.WriteReg(0x0A0, 0x0001)
	a.Paula.WriteReg(0x0A2, 0x2000)
	a.Paula.WriteReg(0x0A4, 1)
	a.Paula.Tick(3 * 200 * CPUCyclesPerColorClock) // the old buffer's last 3 samples
	if got := a.Paula.ch[0].sample; got != 0x55 {
		t.Errorf("after reload sample = $%02X, want $55 from the new buffer", got)
	}

	a.Custom.Write(0x096, []byte{0x00, 0x01}) // clear AUD0EN
	a.Paula.Tick(2)
	if a.Paula.ch[0].dma || a.Paula.ch[0].sample != 0 {
		t.Error("channel still playing after AUD0EN cleared")
	}
}

// Without DMA the CPU feeds AUDxDAT; each word loaded raises the interrupt
// asking for the next.
func TestAudioManualMode(t *testing.T) {
	a := createTestAmiga(t)
	a.Paula.WriteReg(0x0B6, 150) // AUD1PER
	a.Paula.WriteReg(0x0BA, 0x7F81)
	if a.Paula.Interrupts[1] != 1 || a.Paula.ch[1].sample != 0x7F {
		t.Fatalf("manual start: %d interrupts, sample $%02X", a.Paula.Interrupts[1], a.Paula.ch[1].sample)
	}
	a.Paula.WriteReg(0x0BA, 0x1122) // next word, queued
	a.Paula.Tick(150 * CPUCyclesPerColorClock)
	if a.Paula.ch[1].sample != -0x7F {
		t.Errorf("second sample = %d, want -127", a.Paula.ch[1].sample)
	}
	a.Paula.Tick(150 * CPUCyclesPerColorClock)
	if a.Paula.ch[1].sample != 0x11 || a.Paula.Interrupts[1] != 2 {
		t.Errorf("queued word: sample $%02X, %d interrupts; want $11, 2", a.Paula.ch[1].sample, a.Paula.Interrupts[1])
	}
	a.Paula.Tick(2 * 150 * CPUCyclesPerColorClock)
	if a.Paula.ch[1].manual {
		t.Error("channel still playing with no word queued")
	}
}

// Mixed output: rate, volume, and channels 0/3 left, 1/2 right.
func TestAudioMixOutput(t *testing.T) {
	a := paulaAmiga(t, 1, 400, 0x7F, 0x7F, 0x7F, 0x7F) // constant +127 on the right
	a.Paula.Output = NewAudioBuffer(1 << 16)
	a.Paula.WriteReg(0x0B8, 32) // half volume
	enableAudioDMA(a, 0x02)
	const seconds = 0.1
	a.Paula.Tick(int(seconds * PaulaClock * CPUCyclesPerColorClock))

	frames := a.Paula.Output.Frames()
	if want := int(seconds * AudioSampleRate); frames < want-1 || frames > want+1 {
		t.Errorf("%d frames for %.1f s, want %d", frames, seconds, want)
	}
	buf := make([]float32, 2*frames)
	a.Paula.Output.ReadFrames(buf)
	l, r := buf[len(buf)-2], buf[len(buf)-1] // after the filters settle
	// One channel of two at 127/128 and half volume: 0.25 of full scale.
	if math.Abs(float64(r)-0.248) > 0.005 || math.Abs(float64(l)) > 1e-6 {
		t.Errorf("settled output L=%.4f R=%.4f, want L=0, R≈0.248", l, r)
	}
}

// ADKCON USE0V1: channel 0's words set channel 1's volume instead of
// sounding.
func TestAudioVolumeModulation(t *testing.T) {
	a := paulaAmiga(t, 0, 300, 0x00, 0x10, 0x00, 0x30)
	a.Disk.WriteADKCON(0x8001) // SET | USE0V1
	enableAudioDMA(a, 0x01)
	a.Paula.Tick(0)
	if a.Paula.ch[1].vol != 0x10 || a.Paula.ch[0].sample != 0 {
		t.Fatalf("after first word: AUD1VOL=%d, ch0 sample %d; want 16, silent", a.Paula.ch[1].vol, a.Paula.ch[0].sample)
	}
	a.Paula.Tick(300 * CPUCyclesPerColorClock) // one word per period
	if a.Paula.ch[1].vol != 0x30 {
		t.Errorf("after second word: AUD1VOL=%d, want 48", a.Paula.ch[1].vol)
	}
}

// Fast-idle must wake for audio interrupts, so the prediction has to be
// exact.
func TestAudioNextInterrupt(t *testing.T) {
	a := paulaAmiga(t, 2, 250, make([]byte, 8)...) // 4 words
	enableAudioDMA(a, 0x04)
	a.Paula.Tick(0)
	want := 8 * 250 * CPUCyclesPerColorClock // 8 samples until the reload
	if got := a.Paula.CyclesToNextInterrupt(); got != want {
		t.Fatalf("next interrupt in %d cycles, want %d", got, want)
	}
	a.Paula.Tick(want - 2)
	if a.Paula.Interrupts[2] != 1 {
		t.Fatal("interrupt early")
	}
	a.Paula.Tick(2)
	if a.Paula.Interrupts[2] != 2 {
		t.Fatal("interrupt not raised when predicted")
	}
}
