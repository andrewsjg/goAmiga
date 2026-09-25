package amiga

import "testing"

// BenchmarkDrawLine measures one hires 2-plane line with a sprite. The
// plane data is dense pseudo-random bytes, a worst case: real screens are
// mostly empty words, which fillPlayfield skips. For whole-frame speed use
// TestTimingProfile.
func BenchmarkDrawLine(b *testing.B) {
	a := newTestAmigaVideo()
	a.Sprites = NewSpriteEngine(a)
	a.Custom.WriteReg16(0x100, 0xA200)
	a.Custom.WriteReg16(0x092, 0x003C)
	a.Custom.WriteReg16(0x094, 0x00D4)
	a.Custom.WriteReg16(0x104, 0x0024)
	for i := 0; i < 160; i++ {
		a.ChipRAM.Write(0x1000+i, []byte{byte(i * 37)})
	}
	a.Sprites.sprites[0] = Sprite{state: sprActive, pos: 0x0060, ctl: 0x0001, data: 0xF0F0, datb: 0x3C3C}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Video.bplpt[0], a.Video.bplpt[1] = 0x1000, 0x1050
		a.Video.RenderLine(DisplayTop)
	}
}
