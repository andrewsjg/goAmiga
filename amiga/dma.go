package amiga

import "github.com/cavaliercoder/go-m68k/m68kmem"

// Chip bus slot allocation (DMA cycle stealing).
//
// The chip bus has one slot per colour clock (227 per PAL line). Agnus hands
// them out by priority: fixed DMA (refresh, disk, audio, sprites) on odd
// slots at the start of the line, then bitplanes, Copper, blitter, and last
// the CPU. The 68000 needs 4 CPU cycles (2 colour clocks) per bus access and
// can only use even slots, so it only waits when a higher-priority channel
// takes an even slot it wants. Accesses to ROM, CIAs and unmapped space do
// not use the chip bus and never wait.
//
// Modelled: bitplane DMA against CPU and blitter; fixed refresh and sprite
// slots against the blitter; blitter against CPU (nasty mode, and the
// "CPU gets every 4th cycle" rule when the blit uses every slot).
// Not modelled: Copper DMA (it mostly sits in WAIT), audio and disk slots.
// CIA accesses are timed separately (ciaAccessWait).

// busCounter counts the CPU's accesses (one per word) to a device. It sits
// between the CPU's Mapper and chip RAM, the custom registers and the CIAs;
// DMA channels and emulator code call those devices directly and are not
// counted.
type busCounter struct {
	m        m68kmem.Memory
	accesses *int
}

func (c *busCounter) Read(addr int, p []byte) (int, error) {
	*c.accesses += (len(p) + 1) / 2
	return c.m.Read(addr, p)
}

func (c *busCounter) Write(addr int, p []byte) (int, error) {
	*c.accesses += (len(p) + 1) / 2
	return c.m.Write(addr, p)
}

func (c *busCounter) Reset() error { return c.m.Reset() }

// Bitplane fetch order within one fetch unit (HRM "DMA time slot
// allocation"); 0 is a slot bitplanes leave free. A plane uses its slot only
// if it is enabled. Unit positions that are even colour clocks are the ones
// the CPU wants, so lores planes 5-6 and hires planes 3-4 steal from it.
var (
	loresFetchOrder = [8]int{0, 4, 6, 2, 0, 3, 5, 1}
	hiresFetchOrder = [4]int{4, 2, 3, 1}
)

// bitplaneFetch describes the bitplane DMA slots of one scanline.
type bitplaneFetch struct {
	planes     int
	hires      bool
	start, end int // fetch window in colour clocks, [start, end)
}

// bitplaneDMA returns the bitplane fetch for line vpos, under the same
// conditions as Video.RenderLine (bitplaneFetchLine).
func (a *Amiga) bitplaneDMA(vpos int) bitplaneFetch {
	if a.Video == nil || !a.Video.bitplaneFetchLine(vpos) {
		return bitplaneFetch{}
	}
	bplcon0 := a.Custom.ReadReg16(0x100)
	f := bitplaneFetch{planes: int(bplcon0>>12) & 7, hires: bplcon0&0x8000 != 0}
	if f.planes == 0 {
		return bitplaneFetch{}
	}
	ddfstrt := a.Custom.ReadReg16(0x092)
	ddfstop := a.Custom.ReadReg16(0x094)
	unit := 8
	if f.hires {
		unit = 4
	}
	f.start = int(ddfstrt & 0xFC)
	if f.start == 0 {
		f.start = 0x38
	}
	f.end = f.start + unit*fetchWords(f.hires, ddfstrt, ddfstop)
	return f
}

// stealsCPUSlots reports whether this fetch takes any even slots.
func (f bitplaneFetch) stealsCPUSlots() bool {
	if f.hires {
		return f.planes >= 3
	}
	return f.planes >= 5
}

// uses reports whether bitplane DMA takes colour clock cck.
func (f bitplaneFetch) uses(cck int) bool {
	if f.planes == 0 || cck < f.start || cck >= f.end {
		return false
	}
	var plane int
	if f.hires {
		plane = hiresFetchOrder[(cck-f.start)&3]
	} else {
		plane = loresFetchOrder[(cck-f.start)&7]
	}
	return plane != 0 && plane <= f.planes
}

// fixedSlot reports whether refresh or sprite DMA takes colour clock cck.
// These are odd slots near the start of the line, so they only affect the
// blitter.
func fixedSlot(cck int, spriteDMA bool) bool {
	if cck&1 == 0 {
		return false
	}
	if cck <= 0x07 { // memory refresh
		return true
	}
	return spriteDMA && cck >= 0x15 && cck <= 0x33 // 8 sprites × 2 words
}

// chipBusWait returns the CPU cycles an instruction starting at the current
// beam position spends waiting for chip bus slots for n chip accesses.
// The accesses are taken as back-to-back bus cycles from the start of the
// instruction, which is how the 68000 issues a burst of them.
func (a *Amiga) chipBusWait(n int) int {
	if n == 0 {
		return 0
	}
	dmacon := a.Custom.ReadReg16(0x096)
	blit := a.Blitter != nil && a.Blitter.busy && dmacon&0x0240 == 0x0240

	// Nasty mode: the blitter never yields, so the first chip access waits
	// for the whole blit. The blitter's own contention with bitplanes
	// stretches it further; approximating with its remaining time.
	if blit && dmacon&0x0400 != 0 {
		return a.Blitter.completionCycles
	}

	wait := 0
	// A blit with no idle cycles in its pattern takes every slot; the CPU
	// gets the bus after being refused for 3 cycles, and that slot is lost
	// to the blitter.
	if blit && a.Blitter.hogsBus() {
		wait += n * 3 * CPUCyclesPerColorClock
		a.Blitter.completionCycles += n * CPUCyclesPerColorClock
	}

	vpos := a.vpos
	f := a.bitplaneDMA(vpos)
	if !f.stealsCPUSlots() && !a.bitplaneDMA(vpos+1).stealsCPUSlots() {
		return wait
	}
	// Colour clock of the first access, rounded up to an even slot.
	t := (a.hpos + wait + 1) / CPUCyclesPerColorClock
	t += t & 1
	start := t
	for i := 0; i < n; i++ {
		for {
			if t >= ColorClocksPerLine {
				t -= ColorClocksPerLine
				start -= ColorClocksPerLine
				vpos++
				f = a.bitplaneDMA(vpos)
				t += t & 1
			}
			if !f.uses(t) {
				break
			}
			t += 2
		}
		t += 2
	}
	return wait + ((t-start)-2*n)*CPUCyclesPerColorClock
}

// blitterSlots returns how many colour clocks in the next cpuCycles CPU
// cycles from the current beam position are free for the blitter.
func (a *Amiga) blitterSlots(cpuCycles int) int {
	n := cpuCycles / CPUCyclesPerColorClock
	spriteDMA := a.Custom.ReadReg16(0x096)&0x0220 == 0x0220
	vpos := a.vpos
	cck := a.hpos / CPUCyclesPerColorClock
	f := a.bitplaneDMA(vpos)
	free := 0
	for i := 0; i < n; i++ {
		if cck >= ColorClocksPerLine {
			cck = 0
			vpos++
			if vpos >= a.linesThisFrame() {
				vpos = 0
			}
			f = a.bitplaneDMA(vpos)
		}
		if !f.uses(cck) && !fixedSlot(cck, spriteDMA) {
			free++
		}
		cck++
	}
	return free
}
