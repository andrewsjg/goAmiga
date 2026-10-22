package whdload

import "encoding/binary"

// ToolTypes reads the tooltypes of an Amiga icon (a .info file: a
// DiskObject, its drawer data and images, its default tool, then its
// tooltypes); nil if it has none or isn't an icon.
func ToolTypes(b []byte) []string {
	be := binary.BigEndian
	if len(b) < 78 || be.Uint16(b) != 0xE310 {
		return nil
	}
	selectRender := be.Uint32(b[4+22:]) // the Gadget's SelectRender
	defaultTool := be.Uint32(b[50:])
	toolTypes := be.Uint32(b[54:])
	drawer := be.Uint32(b[66:])
	p := 78
	if drawer != 0 {
		p += 56 // DrawerData
	}
	// The images: an Image structure, then its planes.
	image := func() bool {
		if p+20 > len(b) {
			return false
		}
		w, h, depth := int(be.Uint16(b[p+4:])), int(be.Uint16(b[p+6:])), int(be.Uint16(b[p+8:]))
		hasData := be.Uint32(b[p+10:]) != 0
		p += 20
		if hasData {
			p += (w + 15) / 16 * 2 * h * depth
		}
		return p <= len(b)
	}
	if !image() {
		return nil
	}
	if selectRender != 0 && !image() {
		return nil
	}
	str := func() (string, bool) {
		if p+4 > len(b) {
			return "", false
		}
		n := int(be.Uint32(b[p:]))
		p += 4
		if n < 0 || p+n > len(b) {
			return "", false
		}
		s := b[p : p+n]
		p += n
		for i, c := range s {
			if c == 0 {
				s = s[:i]
				break
			}
		}
		return string(s), true
	}
	if defaultTool != 0 {
		if _, ok := str(); !ok {
			return nil
		}
	}
	if toolTypes == 0 || p+4 > len(b) {
		return nil
	}
	count := int(be.Uint32(b[p:]))/4 - 1
	p += 4
	var tts []string
	for i := 0; i < count; i++ {
		s, ok := str()
		if !ok {
			break
		}
		tts = append(tts, s)
	}
	return tts
}
