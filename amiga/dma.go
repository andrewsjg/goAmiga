package amiga

import (
	m68k "github.com/andrewsjg/goamiga/m68k"
	"github.com/andrewsjg/goamiga/m68k/m68kmem"
)

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
// Modelled: bitplane and Copper DMA against CPU and blitter; the fixed
// slots (refresh, disk, audio when Paula asks for a word, sprites) against
// the blitter; blitter against CPU (nasty mode, and the "CPU gets every
// 4th cycle" rule when the blit uses every slot). CIA accesses are timed
// separately (ciaAccessWait).

// busCounter counts the CPU's accesses (one per word) to a device. It sits
// between the CPU's Mapper and chip RAM, the custom registers and the CIAs;
// DMA channels and emulator code call those devices directly and are not
// counted.
//
// On the A1200 (AGA with a 68020) chip RAM is 32 bits wide, so a longword
// is one access, and instruction fetches that hit the 68020's cache never
// reach the bus. The cache isn't modelled: with it on, every fetch counts
// as a hit, which suits the loops that matter (a 256-byte cache holds
// them) and flatters straight-line code.
type busCounter struct {
	m        m68kmem.Memory
	accesses *int
	a        *Amiga // for the CPU's cache and the bus width (chip RAM only)
}

func (c *busCounter) count(n int) int {
	if a := c.a; a != nil && a.CPU.Model >= m68k.Model68020 {
		if a.CPU.M.Fetch && a.CPU.ICacheEnabled() {
			return 0
		}
		if a.AGA {
			return (n + 3) / 4
		}
	}
	return (n + 1) / 2
}

func (c *busCounter) Read(addr int, p []byte) (int, error) {
	*c.accesses += c.count(len(p))
	return c.m.Read(addr, p)
}

func (c *busCounter) Write(addr int, p []byte) (int, error) {
	*c.accesses += c.count(len(p))
	return c.m.Write(addr, p)
}

func (c *busCounter) Reset() error { return c.m.Reset() }

// Bitplane fetch order within one fetch unit (HRM "DMA time slot
// allocation"); 0 is a slot bitplanes leave free. A plane uses its slot only
// if it is enabled. Unit positions that are even colour clocks are the ones
// the CPU wants, so lores planes 5-6 and hires planes 3-4 steal from it.
var (
	loresFetchOrder = [8]int{8, 4, 6, 2, 7, 3, 5, 1} // planes 7-8 on AGA only
	hiresFetchOrder = [4]int{4, 2, 3, 1}
	shresFetchOrder = [2]int{2, 1} // ECS super-hires: two planes, every slot
)

// bitplaneFetch describes the bitplane DMA slots of one scanline.
type bitplaneFetch struct {
	planes     int
	hires      bool
	shres      bool
	factor     int // AGA FMODE fetch width: 1, 2 or 4 words per fetch
	unit       int // colour clocks per fetch unit when factor > 1
	start, end int // fetch window in colour clocks, [start, end)
}

// bitplaneDMA returns the bitplane fetch for line vpos, under the same
// conditions as Video.RenderLine (bitplaneFetchLine).
func (a *Amiga) bitplaneDMA(vpos int) bitplaneFetch {
	if a.Video == nil || !a.Video.bitplaneFetchLine(vpos) {
		return bitplaneFetch{}
	}
	bplcon0 := a.Custom.ReadReg16(0x100)
	f := bitplaneFetch{planes: planesOf(bplcon0, a.AGA), hires: bplcon0&0x8000 != 0}
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
	if a.ECSAgnus && a.ECSDenise && !f.hires && bplcon0&0x0040 != 0 {
		// Super-hires: the hires fetch window, filled twice as densely.
		f.shres, f.planes = true, min(f.planes, 2)
		f.end = f.start + 4*fetchWords(true, ddfstrt, ddfstop)
	}
	// AGA wide fetches: each plane fetches factor words once per (longer)
	// unit; the planes' slots come at the start of each unit.
	if factor := a.Video.fetchFactor(); factor > 1 {
		f.factor = factor
		f.unit = fetchUnitCCK(f.hires, f.shres, factor)
		words := fetchWordsFactor(f.hires, ddfstrt, ddfstop, factor)
		f.end = f.start + f.unit*(words/factor)
	}
	return f
}

// stealsCPUSlots reports whether this fetch takes any even slots.
func (f bitplaneFetch) stealsCPUSlots() bool {
	if f.factor > 1 {
		return f.planes >= 5 // planes 5-8 take the even slots of the order
	}
	if f.shres {
		return f.planes >= 2
	}
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
	if f.factor > 1 {
		i := (cck - f.start) % f.unit
		if i >= 8 {
			return false
		}
		plane = loresFetchOrder[i]
	} else if f.shres {
		plane = shresFetchOrder[(cck-f.start)&1]
	} else if f.hires {
		plane = hiresFetchOrder[(cck-f.start)&3]
	} else {
		plane = loresFetchOrder[(cck-f.start)&7]
	}
	return plane != 0 && plane <= f.planes
}

// fixedDMA says which fixed DMA channels are running: they take odd slots
// near the start of each line (HRM "DMA time slot allocation"), so they
// only affect the blitter, never the CPU.
//
// An audio channel takes its slot only when Paula has asked for a word:
// after every two samples it plays (one, when it modulates another
// channel), so at most once a line and usually less. audioDue is how many
// colour clocks from now its next request comes (-1: no DMA), audioEvery
// the time between requests.
type fixedDMA struct {
	disk       bool
	audioDue   [4]int
	audioEvery [4]int
	sprite     bool
}

func (a *Amiga) fixedDMAState() fixedDMA {
	dmacon := a.Custom.ReadReg16(0x096)
	var f fixedDMA
	for i := range f.audioDue {
		f.audioDue[i] = -1
	}
	if dmacon&0x0200 == 0 {
		return f
	}
	f.disk = dmacon&0x0010 != 0 && a.Disk != nil && a.Disk.xfer != nil
	for i := range f.audioDue {
		if dmacon&(1<<i) != 0 {
			f.audioDue[i], f.audioEvery[i] = a.Paula.nextRequest(i)
		}
	}
	f.sprite = dmacon&0x0020 != 0
	return f
}

// fixedSlot reports whether refresh, disk, audio or sprite DMA takes
// colour clock cck, elapsed colour clocks from now: refresh $01-$07, disk
// $09-$0D, audio $0F-$15 (one per channel, when its request is due),
// sprites $17-$35 (two words each for eight sprites).
func fixedSlot(cck, elapsed int, f *fixedDMA) bool {
	if cck&1 == 0 {
		return false
	}
	switch {
	case cck <= 0x07:
		return true
	case cck <= 0x0D:
		return f.disk
	case cck <= 0x15:
		n := (cck - 0x0F) / 2
		if f.audioDue[n] < 0 || elapsed < f.audioDue[n] {
			return false
		}
		f.audioDue[n] = max(f.audioDue[n]+f.audioEvery[n], elapsed+1)
		return true
	case cck <= 0x35:
		return f.sprite
	}
	return false
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
	copperWords := a.Copper.busyWords()
	if copperWords == 0 && !f.stealsCPUSlots() && !a.bitplaneDMA(vpos+1).stealsCPUSlots() {
		return wait
	}
	// Colour clock of the first access, rounded up to an even slot.
	t := (a.hpos + wait + 1) / CPUCyclesPerColorClock
	t += t & 1
	start := t
	for i := 0; i < n; i++ {
		for {
			if lcck := a.lineCycles / CPUCyclesPerColorClock; t >= lcck {
				t -= lcck
				start -= lcck
				vpos++
				f = a.bitplaneDMA(vpos)
				t += t & 1
			}
			if !f.uses(t) {
				if copperWords == 0 {
					break
				}
				copperWords-- // a running Copper outranks the CPU
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
	fixed := a.fixedDMAState()
	copperWords := a.Copper.busyWords()
	vpos := a.vpos
	cck := a.hpos / CPUCyclesPerColorClock
	f := a.bitplaneDMA(vpos)
	free := 0
	for i := 0; i < n; i++ {
		if cck >= a.lineCycles/CPUCyclesPerColorClock {
			cck = 0
			vpos++
			if vpos >= a.linesThisFrame() {
				vpos = 0
			}
			f = a.bitplaneDMA(vpos)
		}
		if !f.uses(cck) && !fixedSlot(cck, i, &fixed) {
			if cck&1 == 0 && copperWords > 0 {
				copperWords-- // the Copper outranks the blitter
			} else {
				free++
			}
		}
		cck++
	}
	return free
}
