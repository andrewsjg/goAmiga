package amiga

import (
	"fmt"
	"io"

	"github.com/andrewsjg/goamiga/m68k"
	"github.com/andrewsjg/goamiga/m68k/m68kmem"
)

// An RTG graphics card for Picasso96, the one emulators use: the uaegfx
// board, driven by Picasso96's own uaegfx.card.
//
// uaegfx.card is a thin driver. Its FindCard looks for a Zorro board from
// manufacturer 2011 with product 96, and most of its entry points just
// call the emulator: they push a function number and JSR to $F0FF60, the
// "uaelib" entry in UAE's boot ROM area. Here that is a small ROM at
// $F00000 whose routine writes the trap register, so Go handles the call
// (rtgCall) with the CPU's registers in hand and returns its answer in
// D0. The blitter calls draw on the card's memory (rtg_blit.go); any
// they can't do answer 0, and Picasso96 then draws in software.
//
// The board is the card's video memory: on Zorro III with the A3000 or
// A4000 motherboard, else on Zorro II (4 MB, which leaves no room for
// Zorro II fast RAM). While Picasso96 has switched the display to the card
// (SetSwitch), the window shows the framebuffer SetPanning names, in the
// mode SetGC set, rather than the chipset's picture (rtgFrame).

const (
	rtgManufacturer = 2011
	rtgProduct      = 96

	rtAreaBase = 0xF00000 // UAE's boot ROM area
	rtAreaEnd  = 0xF0FFFF
	rtAreaTrap = 0xFF00 // word write: an RTG call
	rtAreaLib  = 0xFF60 // uaelib: JSR here with the function number pushed
)

// The calls uaegfx.card makes (UAE's uaelib function numbers).
const (
	rtgFindCard               = 16
	rtgFillRect               = 17
	rtgSetSwitch              = 18
	rtgSetColorArray          = 19
	rtgSetDAC                 = 20
	rtgSetGC                  = 21
	rtgSetPanning             = 22
	rtgCalculateBytesPerRow   = 23
	rtgBlitPlanar2Chunky      = 24
	rtgBlitRect               = 25
	rtgSetDisplay             = 26
	rtgBlitTemplate           = 27
	rtgBlitRectNoMaskComplete = 28
	rtgInitCard               = 29
	rtgBlitPattern            = 30
	rtgInvertRect             = 31
	rtgBlitPlanar2Direct      = 32
	rtgWaitVerticalSync       = 34
)

// Picasso96's BoardInfo, the structure rtg.library and the card share (the
// offsets uaegfx.card itself uses agree).
const (
	biRegisterBase     = 0x000
	biMemoryBase       = 0x004
	biMemoryIOBase     = 0x008
	biMemorySize       = 0x00C
	biBoardType        = 0x0AA
	biBitsPerCannon    = 0x0B8
	biFlags            = 0x0BA
	biSoftSpriteFlags  = 0x0BE
	biRGBFormats       = 0x0C8
	biMaxHorValue      = 0x0CA // [5] words, by format class
	biMaxVerValue      = 0x0D4
	biMaxHorResolution = 0x0DE
	biMaxVerResolution = 0x0E8
	biMaxMemorySize    = 0x0F2
	biMaxChunkSize     = 0x0F6
	biMemoryClock      = 0x0FA
)

// ModeInfo, as SetGC passes it.
const (
	miWidth  = 18
	miHeight = 20
	miDepth  = 22
)

// RGBFTYPE: Picasso96's pixel formats.
const (
	rgbfCLUT     = 1
	rgbfR8G8B8   = 2
	rgbfB8G8R8   = 3
	rgbfR5G6B5PC = 4
	rgbfR5G5B5PC = 5
	rgbfA8R8G8B8 = 6
	rgbfA8B8G8R8 = 7
	rgbfR8G8B8A8 = 8
	rgbfB8G8R8A8 = 9
	rgbfR5G6B5   = 10
	rgbfR5G5B5   = 11
	rgbfB5G6R5PC = 12
	rgbfB5G5R5PC = 13
)

// rgbfBytes is the bytes per pixel of each format.
var rgbfBytes = [14]int{0, 1, 3, 3, 2, 2, 4, 4, 4, 4, 2, 2, 2, 2}

// rtgBoard is the card: its video memory on Zorro II or III, and the state
// the calls set.
type rtgBoard struct {
	autoconfigState
	amiga *Amiga
	vram  *ram
	z3    bool

	on         bool // SetSwitch: the window shows the card
	display    bool // SetDisplay
	width      int  // SetGC: the mode
	height     int
	format     int    // SetDAC / SetPanning
	fb         uint32 // SetPanning: the framebuffer's Amiga address
	rowPixels  int    // and its width in pixels
	xoff, yoff int
	palette    [256][3]byte // SetColorArray
	pix        []byte       // the picture, RGBA (render)

	boardInfo uint32 // rtg.library's BoardInfo for the card (FindCard)
	area      *rtArea

	// Verify checks the blitter against Picasso96's own drawing (tests):
	// Verified calls, and Mismatches among them.
	Verify               bool
	NoBlitter            bool // leave all drawing to Picasso96 (comparisons)
	Verified, Mismatches int
	pending              []*rtgCheck
	Calls                [64]int // calls by function number

	Trace io.Writer
}

func (r *rtgBoard) tracef(format string, args ...any) {
	if r.Trace != nil {
		fmt.Fprintf(r.Trace, "rtg: "+format+"\n", args...)
	}
}

func (r *rtgBoard) zorro3() bool { return r.z3 }

func (r *rtgBoard) configByte(reg int) byte {
	switch reg {
	case 0x00: // er_Type: no memory list (it's video memory), size
		if r.z3 {
			return 0x80 | z3SizeCode(len(r.vram.b))
		}
		return 0xC0 | sizeCode(len(r.vram.b))
	case 0x04:
		return rtgProduct
	case 0x08: // er_Flags
		if r.z3 {
			return 0x30 // extended sizes, Zorro III
		}
		return 0x00
	case 0x10:
		return rtgManufacturer >> 8
	case 0x14:
		return rtgManufacturer & 0xFF
	}
	return 0
}

func (r *rtgBoard) space() m68kmem.Memory { return r.vram }
func (r *rtgBoard) size() int             { return len(r.vram.b) }
func (r *rtgBoard) configured(uint32)     {}

// EnableRTG fits the uaegfx card with mb megabytes of video memory (0 for
// none): on Zorro III (16 to 1024 MB in powers of two) with the A3000 or
// A4000 motherboard, else on Zorro II (1, 2 or 4 MB). Picasso96 drives it
// with its uaegfx.card. Call it before Reset.
func (a *Amiga) EnableRTG(mb int) error {
	if mb == 0 {
		a.RTG = nil
		a.wireMemoryMap(a.overlay)
		return nil
	}
	r := &rtgBoard{amiga: a, z3: a.Motherboard != nil}
	switch {
	case r.z3 && (mb < 16 || mb > 1024 || mb&(mb-1) != 0):
		return fmt.Errorf("RTG memory on Zorro III must be a power of two from 16 to 1024 MB, not %d", mb)
	case !r.z3 && mb != 1 && mb != 2 && mb != 4:
		return fmt.Errorf("RTG memory on Zorro II must be 1, 2 or 4 MB, not %d (16 MB and more on Zorro III, with --motherboard)", mb)
	}
	if r.z3 && a.CPU.Model < m68k.Model68030 {
		return fmt.Errorf("Zorro III needs the A3000 or A4000 (a 68030 or 68040 and its motherboard)")
	}
	r.vram = &ram{b: make([]byte, mb<<20)}
	r.area = newRTArea(a)
	a.RTG = r
	a.wireMemoryMap(a.overlay)
	return nil
}

// reset gives the display back to the chipset: Picasso96 sets the card up
// again as it starts.
func (r *rtgBoard) reset() {
	r.on, r.display = false, false
	r.width, r.height, r.format, r.fb, r.rowPixels, r.xoff, r.yoff = 0, 0, 0, 0, 0, 0, 0
	r.boardInfo = 0
}

// state saves or loads the card: its configuration, the mode and
// framebuffer the calls set, the palette and the video memory.
func (r *rtgBoard) state(s *stateCodec) {
	r.autoconfigState.state(s)
	s.bool(&r.on)
	s.bool(&r.display)
	for _, p := range []*int{&r.width, &r.height, &r.format, &r.rowPixels, &r.xoff, &r.yoff} {
		num(s, p)
	}
	num(s, &r.fb)
	num(s, &r.boardInfo)
	for i := range r.palette {
		s.fixed(r.palette[i][:])
	}
	s.fixed(r.vram.b)
}

// rtArea is UAE's boot ROM area at $F00000, as uaegfx.card expects it:
// the uaelib entry, which writes the trap register and returns, and RAM
// below it for the structures the card builds (the resolutions). Its first
// word stays 0: $1111 there would mean an extended ROM to Kickstart.
type rtArea struct {
	a   *Amiga
	mem [rtAreaEnd - rtAreaBase + 1]byte
}

// rtAreaCode is the routine at $F0FF60: MOVE.W #1,$F0FF00; RTS.
var rtAreaCode = []byte{0x33, 0xFC, 0x00, 0x01, 0x00, 0xF0, 0xFF, 0x00, 0x4E, 0x75}

// rtAreaVerify is where a verified call's software drawing returns
// (Verify): MOVE.W #1,$F0FF02; MOVE.L $F0FF80,-(SP); RTS, the longword at
// $F0FF80 being the caller's return address.
const (
	rtAreaVerify     = 0xFF70
	rtAreaVerifyTrap = 0xFF02
	rtAreaVerifyRet  = 0xFF80
)

var rtAreaVerifyCode = []byte{
	0x33, 0xFC, 0x00, 0x01, 0x00, 0xF0, 0xFF, 0x02,
	0x2F, 0x39, 0x00, 0xF0, 0xFF, 0x80,
	0x4E, 0x75,
}

// The RAM for the card's structures.
const (
	rtAreaRAM    = 0x0100
	rtAreaRAMEnd = 0xF000
)

func newRTArea(a *Amiga) *rtArea {
	m := &rtArea{a: a}
	copy(m.mem[rtAreaLib:], rtAreaCode)
	copy(m.mem[rtAreaVerify:], rtAreaVerifyCode)
	return m
}

func (m *rtArea) Read(addr int, p []byte) (int, error) {
	return copy(p, m.mem[addr:]), nil
}

func (m *rtArea) Write(addr int, p []byte) (int, error) {
	switch {
	case addr == rtAreaTrap:
		if m.a.RTG != nil {
			m.a.RTG.call()
		}
	case addr == rtAreaVerifyTrap:
		if m.a.RTG != nil {
			m.a.RTG.checkPending()
		}
	case addr >= rtAreaRAM && addr+len(p) <= rtAreaRAMEnd:
		copy(m.mem[addr:], p)
	}
	return len(p), nil
}

func (m *rtArea) Reset() error { return nil }

// call is a call from uaegfx.card: the function number is on the stack
// above the uaelib routine's return address; the answer goes in D0.
func (r *rtgBoard) call() {
	a := r.amiga
	c := a.CPU
	fn := a.peek32(c.A[7] + 4)
	bi := c.A[0]
	var res uint32
	if fn < uint32(len(r.Calls)) {
		r.Calls[fn]++
	}
	switch fn {
	case rtgFindCard:
		r.boardInfo = bi
		a.poke32(bi+biMemoryBase, r.base)
		a.poke32(bi+biMemorySize, uint32(len(r.vram.b)))
		a.poke32(bi+biRegisterBase, rtAreaBase)
		r.tracef("FindCard: memory at $%08X, %d KB", r.base, len(r.vram.b)>>10)
		res = 0xFFFFFFFF
	case rtgInitCard:
		r.initCard(bi)
		res = 0xFFFFFFFF
	case rtgSetSwitch:
		if r.on {
			res = 1
		}
		r.on = c.D[0]&0xFFFF != 0
		r.tracef("SetSwitch %v", r.on)
	case rtgSetColorArray:
		start, count := int(c.D[0]&0xFFFF), int(c.D[1]&0xFFFF)
		r.setColors(bi, start, count)
	case rtgSetDAC:
		r.format = int(c.D[7])
		r.tracef("SetDAC format %d", r.format)
	case rtgSetGC:
		mi := c.A[1]
		r.width, r.height = int(a.peek16(mi+miWidth)), int(a.peek16(mi+miHeight))
		r.tracef("SetGC %dx%d depth %d", r.width, r.height, a.peek8(mi+miDepth))
	case rtgSetPanning:
		r.fb, r.rowPixels = c.A[1], int(c.D[0]&0xFFFF)
		r.xoff, r.yoff = int(int16(c.D[1])), int(int16(c.D[2]))
		r.format = int(c.D[7])
		r.tracef("SetPanning $%08X width %d offset %d,%d format %d", r.fb, r.rowPixels, r.xoff, r.yoff, r.format)
	case rtgCalculateBytesPerRow:
		f := int(c.D[7])
		bpp := 1
		if f > 0 && f < len(rgbfBytes) {
			bpp = rgbfBytes[f]
		}
		res = uint32(int(c.D[0]&0xFFFF) * bpp)
	case rtgSetDisplay:
		if r.display {
			res = 1
		}
		r.display = c.D[0]&0xFFFF != 0
	case rtgWaitVerticalSync:
		// The frame comes when it comes: nothing to wait for here.
	default:
		// The blitter calls; 0 sends Picasso96 to its own routines.
		switch {
		case r.NoBlitter:
		case r.Verify:
			r.verifyCall(fn)
		case r.blit(fn):
			res = 1
		}
	}
	c.D[0] = res
}

// initCard tells rtg.library what the card can do: every pixel format,
// 8 bits per colour gun, modes up to 2048×2048, and a software mouse
// pointer. The modes offered are the settings file's (Picasso96Mode makes
// them): Picasso96 2.0 takes its resolutions from there, for the board
// type in its STHD chunk, which must be uaegfx's (1).
func (r *rtgBoard) initCard(bi uint32) {
	a := r.amiga
	formats := uint16(0)
	for f := rgbfCLUT; f <= rgbfB5G5R5PC; f++ {
		formats |= 1 << f
	}
	a.poke16(bi+biRGBFormats, formats)
	a.poke16(bi+biSoftSpriteFlags, formats)
	a.poke16(bi+biBitsPerCannon, 8)
	for i := 0; i < 5; i++ {
		a.poke16(bi+biMaxHorValue+uint32(2*i), 4095)
		a.poke16(bi+biMaxVerValue+uint32(2*i), 4095)
		a.poke16(bi+biMaxHorResolution+uint32(2*i), 2048)
		a.poke16(bi+biMaxVerResolution+uint32(2*i), 2048)
	}
	a.poke32(bi+biMaxMemorySize, uint32(len(r.vram.b)))
	a.poke32(bi+biMaxChunkSize, uint32(len(r.vram.b)))
	a.poke32(bi+biMemoryClock, 100000000)
	r.tracef("InitCard: formats $%04X", formats)
}

// rtgCLUT is BoardInfo's palette: 256 entries of red, green and blue
// bytes, which rtg.library fills before calling SetColorArray. (Found by
// looking for Workbench's colours there with its screen open on the card.)
const rtgCLUT = 0x242

func (r *rtgBoard) setColors(bi uint32, start, count int) {
	var b [3]byte
	for i := start; i < start+count && i < 256; i++ {
		r.amiga.peekBytes(bi+rtgCLUT+uint32(3*i), b[:])
		r.palette[i] = b
	}
	r.tracef("SetColorArray %d+%d", start, count)
}
