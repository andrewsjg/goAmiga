package amiga

import (
	"encoding/binary"
	"math"
	"sync"
)

// Paula audio: four 8-bit DMA sound channels.
//
// Each channel plays signed 8-bit samples from chip RAM, two per word (high
// byte first), each held for AUDxPER colour clocks, scaled by AUDxVOL
// (0-64). With DMA on (DMACON DMAEN + AUDxEN) the channel copies AUDxLC and
// AUDxLEN (words) into its own counters, fetches words until the length
// runs out, then reloads them and starts again. It raises its interrupt
// (INTREQ bit 7+n, level 4) each time it starts a buffer, which is when
// software may write the next buffer's AUDxLC/AUDxLEN. Without DMA, the CPU
// can write AUDxDAT directly; the interrupt then asks for the next word.
//
// Channels 0 and 3 are on the left, 1 and 2 on the right. ADKCON can make a
// channel modulate the next one's volume and/or period instead of sounding.
//
// Channels are event driven in colour clocks rather than ticked every
// cycle. Output is integrated (box filtered) into host samples at
// AudioSampleRate, then passed through the A500's fixed low-pass filter and
// the LED-switched filter.

const (
	PaulaClock      = 3546895 // PAL colour clock, Hz
	AudioSampleRate = 48000

	audioMinPeriod = 16 // guards against runaway event rates with tiny periods
)

type audioChannel struct {
	// Registers.
	lc       uint32 // AUDxLC
	len, per uint16 // AUDxLEN (words, 0 = 65536), AUDxPER (colour clocks, 0 = 65536)
	vol      uint16 // AUDxVOL
	dat      uint16 // AUDxDAT, written by the CPU in manual mode
	datFull  bool   // manual mode: AUDxDAT holds a word not yet played

	// State.
	dma        bool   // running under DMA
	manual     bool   // playing CPU-written words
	ptr        uint32 // DMA pointer
	count      int    // words left in the current buffer
	word       uint16 // word being played
	lowByte    bool   // playing the second sample of word
	periodLeft int    // colour clocks left of the current sample
	sample     int8   // current output sample
	modWord    int    // modulation: words applied (alternates volume/period)
}

func (c *audioChannel) period() int {
	p := int(c.per)
	if p == 0 {
		p = 65536
	}
	if p < audioMinPeriod {
		p = audioMinPeriod
	}
	return p
}

func (c *audioChannel) volume() int {
	v := int(c.vol & 0x7F)
	if v > 64 {
		v = 64
	}
	return v
}

// Paula is the audio part of the Paula chip.
type Paula struct {
	amiga *Amiga
	ch    [4]audioChannel

	// Output receives mixed samples; nil skips mixing (the channels, DMA
	// and interrupts still run).
	Output *AudioBuffer

	// Interrupts counts audio interrupts raised per channel.
	Interrupts [4]int

	cpuRem  int     // CPU cycles not yet turned into colour clocks
	hostAcc int     // host-sample phase, in units of colour clocks × AudioSampleRate
	accL    float64 // left/right level × colour clocks for the current host sample
	accR    float64
	accCCK  int

	// Output filters (see filter).
	fixedL, fixedR onePole
	ledL, ledR     biquad
}

// NewPaula creates the audio side of Paula.
func NewPaula(a *Amiga) *Paula {
	p := &Paula{amiga: a}
	p.fixedL = newOnePole(4420)
	p.fixedR = newOnePole(4420)
	p.ledL = newLowpassBiquad(3275, 0.707)
	p.ledR = newLowpassBiquad(3275, 0.707)
	return p
}

// Reset silences all channels and clears their registers.
func (p *Paula) Reset() {
	p.ch = [4]audioChannel{}
	p.cpuRem, p.hostAcc = 0, 0
	p.accL, p.accR, p.accCCK = 0, 0, 0
}

// WriteReg handles a write to an audio register ($0A0-$0DF).
func (p *Paula) WriteReg(offset int, val uint16) {
	n := (offset - 0x0A0) >> 4
	if n < 0 || n > 3 {
		return
	}
	c := &p.ch[n]
	switch offset & 0xF {
	case 0x0: // AUDxLCH
		c.lc = c.lc&0xFFFF | uint32(val&0x7)<<16
	case 0x2: // AUDxLCL
		c.lc = c.lc&^0xFFFF | uint32(val&0xFFFE)
	case 0x4:
		c.len = val
	case 0x6:
		c.per = val
	case 0x8:
		c.vol = val
	case 0xA: // AUDxDAT
		c.dat, c.datFull = val, true
		if !c.dma && !c.manual {
			p.startManual(n)
		}
	}
}

// irq raises channel n's audio interrupt.
func (p *Paula) irq(n int) {
	p.Interrupts[n]++
	if p.amiga != nil && p.amiga.Custom != nil {
		p.amiga.Custom.SetIntreq(1 << (7 + n))
	}
}

// modulates reports which of channel n's ADKCON modulation bits are set:
// volume (USEnVn+1) and period (USEnPn+1) of channel n+1.
func (p *Paula) modulates(n int) (vol, per bool) {
	if p.amiga == nil || p.amiga.Disk == nil || n == 3 {
		return false, false // channel 3 has no channel to modulate
	}
	adk := p.amiga.Disk.ReadADKCONR()
	return adk>>n&1 != 0, adk>>(n+4)&1 != 0
}

// fetch loads the next DMA word for channel n, reloading pointer and length
// (and raising the interrupt) when the buffer is used up.
func (p *Paula) fetch(n int) {
	c := &p.ch[n]
	if c.count == 0 {
		c.ptr = c.lc
		c.count = int(c.len)
		if c.count == 0 {
			c.count = 65536
		}
		p.irq(n)
	}
	var buf [2]byte
	if p.amiga != nil && p.amiga.ChipRAM != nil {
		p.amiga.ChipRAM.Read(int(c.ptr), buf[:])
	}
	c.word = binary.BigEndian.Uint16(buf[:])
	c.ptr += 2
	c.count--
}

// startWord begins playing c.word (or applying it, when modulating).
func (p *Paula) startWord(n int) {
	c := &p.ch[n]
	c.periodLeft = c.period()
	c.lowByte = false
	mv, mp := p.modulates(n)
	if !mv && !mp {
		c.sample = int8(c.word >> 8)
		return
	}
	// A modulating channel is silent; each word sets the next channel's
	// volume or period, alternating when both are selected.
	c.sample = 0
	next := &p.ch[n+1]
	switch {
	case mv && mp:
		if c.modWord%2 == 0 {
			next.vol = c.word
		} else {
			next.per = c.word
		}
	case mv:
		next.vol = c.word
	default:
		next.per = c.word
	}
	c.modWord++
	c.lowByte = true // one word per period: no second sample
}

// endSample moves channel n on when its current sample has played.
func (p *Paula) endSample(n int) {
	c := &p.ch[n]
	if !c.lowByte {
		c.lowByte = true
		c.sample = int8(c.word)
		c.periodLeft = c.period()
		return
	}
	switch {
	case c.dma:
		p.fetch(n)
		p.startWord(n)
	case c.manual && c.datFull:
		c.word, c.datFull = c.dat, false
		p.startWord(n)
		p.irq(n)
	default:
		c.manual = false
		c.sample = 0
	}
}

// startManual starts playing a CPU-written AUDxDAT word on an idle channel
// and asks for the next one.
func (p *Paula) startManual(n int) {
	c := &p.ch[n]
	c.manual = true
	c.word, c.datFull = c.dat, false
	p.startWord(n)
	p.irq(n)
}

// checkDMA starts or stops channel DMA to match DMACON.
func (p *Paula) checkDMA() {
	var dmacon uint16
	if p.amiga != nil && p.amiga.Custom != nil {
		dmacon = p.amiga.Custom.ReadReg16(0x096)
	}
	for n := range p.ch {
		c := &p.ch[n]
		on := dmacon&0x0200 != 0 && dmacon&(1<<n) != 0
		switch {
		case on && !c.dma:
			c.dma, c.manual = true, false
			c.count = 0 // first fetch loads LC/LEN and interrupts
			c.modWord = 0
			p.fetch(n)
			p.startWord(n)
		case !on && c.dma:
			c.dma = false
			c.sample = 0
		}
	}
}

// Tick advances audio by cpuCycles CPU cycles (two per colour clock).
func (p *Paula) Tick(cpuCycles int) {
	p.checkDMA()
	p.cpuRem += cpuCycles
	cck := p.cpuRem / CPUCyclesPerColorClock
	p.cpuRem %= CPUCyclesPerColorClock
	p.advance(cck)
}

// advance runs the channels for cck colour clocks, splitting time at
// channel events and host sample boundaries.
func (p *Paula) advance(cck int) {
	for cck > 0 {
		step := cck
		for n := range p.ch {
			c := &p.ch[n]
			if (c.dma || c.manual) && c.periodLeft < step {
				step = c.periodLeft
			}
		}
		if p.Output != nil {
			// Colour clocks until the next host sample is due.
			if due := (PaulaClock - p.hostAcc + AudioSampleRate - 1) / AudioSampleRate; due < step {
				step = due
			}
			l := int(p.ch[0].sample)*p.ch[0].volume() + int(p.ch[3].sample)*p.ch[3].volume()
			r := int(p.ch[1].sample)*p.ch[1].volume() + int(p.ch[2].sample)*p.ch[2].volume()
			p.accL += float64(l * step)
			p.accR += float64(r * step)
			p.accCCK += step
			p.hostAcc += step * AudioSampleRate
			if p.hostAcc >= PaulaClock {
				p.hostAcc -= PaulaClock
				p.emit()
			}
		}
		cck -= step
		for n := range p.ch {
			c := &p.ch[n]
			if !c.dma && !c.manual {
				continue
			}
			c.periodLeft -= step
			if c.periodLeft <= 0 {
				p.endSample(n)
			}
		}
	}
}

// emit finishes one host sample: average level, filters, output.
func (p *Paula) emit() {
	// Full scale per side: two channels × 128 × 64.
	const scale = 1.0 / (2 * 128 * 64)
	l := p.accL / float64(p.accCCK) * scale
	r := p.accR / float64(p.accCCK) * scale
	p.accL, p.accR, p.accCCK = 0, 0, 0

	l, r = p.fixedL.step(l), p.fixedR.step(r)
	if p.ledFilter() {
		l, r = p.ledL.step(l), p.ledR.step(r)
	}
	p.Output.Write(float32(l), float32(r))
}

// ledFilter reports whether the LED low-pass filter is on: CIA-A PRA bit 1
// low (power LED bright).
func (p *Paula) ledFilter() bool {
	return p.amiga != nil && p.amiga.CIAA != nil && p.amiga.CIAA.regs[CIARegPRA]&0x02 == 0
}

// CyclesToNextInterrupt returns the CPU cycles until the next audio
// interrupt a running channel will raise (the end of its buffer or, in
// manual mode, of its word), or -1.
func (p *Paula) CyclesToNextInterrupt() int {
	next := -1
	for n := range p.ch {
		c := &p.ch[n]
		var cck int
		switch {
		case c.dma:
			// The rest of this word, then every remaining word in the buffer.
			perWord := 2 * c.period()
			if mv, mp := p.modulates(n); mv || mp {
				perWord = c.period()
			}
			cck = c.periodLeft + c.count*perWord
			if !c.lowByte {
				cck += c.period()
			}
		case c.manual:
			cck = c.periodLeft
			if !c.lowByte {
				cck += c.period()
			}
		default:
			continue
		}
		cyc := cck*CPUCyclesPerColorClock - p.cpuRem
		if cyc < 1 {
			cyc = 1
		}
		if next < 0 || cyc < next {
			next = cyc
		}
	}
	return next
}

// AudioBuffer is a thread-safe ring of stereo float32 samples: Paula writes
// on the emulator goroutine and the host audio player reads (as 32-bit
// little-endian float, left then right) on its own.
type AudioBuffer struct {
	mu   sync.Mutex
	buf  []float32 // interleaved L, R
	r, n int       // read position and frames held
}

// NewAudioBuffer holds up to frames stereo frames; when full, the oldest
// are dropped.
func NewAudioBuffer(frames int) *AudioBuffer {
	return &AudioBuffer{buf: make([]float32, 2*frames)}
}

// Write appends one frame, dropping the oldest if the buffer is full.
func (b *AudioBuffer) Write(l, r float32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	frames := len(b.buf) / 2
	if b.n == frames {
		b.r = (b.r + 1) % frames
		b.n--
	}
	w := (b.r + b.n) % frames
	b.buf[2*w], b.buf[2*w+1] = l, r
	b.n++
}

// Frames returns how many frames are buffered.
func (b *AudioBuffer) Frames() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.n
}

// ReadFrames removes up to len(dst)/2 frames into dst and returns the
// number of frames read.
func (b *AudioBuffer) ReadFrames(dst []float32) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	frames := len(b.buf) / 2
	k := 0
	for ; k < len(dst)/2 && b.n > 0; k++ {
		dst[2*k], dst[2*k+1] = b.buf[2*b.r], b.buf[2*b.r+1]
		b.r = (b.r + 1) % frames
		b.n--
	}
	return k
}

// Read implements io.Reader for the host player: 32-bit float stereo. When
// the emulator has fallen behind it pads with silence rather than block.
func (b *AudioBuffer) Read(p []byte) (int, error) {
	frames := len(p) / 8
	tmp := make([]float32, 2*frames)
	b.ReadFrames(tmp)
	for i, v := range tmp {
		binary.LittleEndian.PutUint32(p[4*i:], math.Float32bits(v))
	}
	return 8 * frames, nil
}

// onePole is a first-order low-pass filter at AudioSampleRate.
type onePole struct{ a, y float64 }

func newOnePole(cutoff float64) onePole {
	return onePole{a: 1 - math.Exp(-2*math.Pi*cutoff/AudioSampleRate)}
}

func (f *onePole) step(x float64) float64 {
	f.y += f.a * (x - f.y)
	return f.y
}

// biquad is a second-order low-pass filter (RBJ cookbook) at
// AudioSampleRate; the A500's LED filter is a 12 dB/octave Butterworth.
type biquad struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

func newLowpassBiquad(cutoff, q float64) biquad {
	w := 2 * math.Pi * cutoff / AudioSampleRate
	alpha := math.Sin(w) / (2 * q)
	cw := math.Cos(w)
	a0 := 1 + alpha
	return biquad{
		b0: (1 - cw) / 2 / a0, b1: (1 - cw) / a0, b2: (1 - cw) / 2 / a0,
		a1: -2 * cw / a0, a2: (1 - alpha) / a0,
	}
}

func (f *biquad) step(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y
	return y
}
