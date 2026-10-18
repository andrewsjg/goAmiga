package m68kmem

import (
	"fmt"
	"testing"
)

func ExampleMapper() {
	// create a memory mapper
	m := NewMapper()

	// map a ROM device to 0x00
	dev1 := NewROM([]byte("This is device one.............\n"))
	m.Map(dev1, 0, 0x1F)

	// map a ROM device to 0x20
	dev2 := NewROM([]byte("This is device two.............\n"))
	m.Map(dev2, 0x20, 0x3F)

	// map a ROM device to 0x40
	dev3 := NewROM([]byte("This is device three...........\n"))
	m.Map(dev3, 0x40, 0x5F)

	// read 96 bytes from the memory mapper
	b := make([]byte, 0x60)
	m.Read(0, b)
	fmt.Print(string(b))

	// Output:
	// This is device one.............
	// This is device two.............
	// This is device three...........
}

func TestMapperOpenBus(t *testing.T) {
	m := NewMapper()
	m.Map(NewROM([]byte{1, 2}), 0x10, 0x11)
	m.Map(NewRAM(2), 0, 1)

	// strict mode: unmapped access faults
	if _, err := m.Read(0x08, make([]byte, 2)); err == nil {
		t.Errorf("strict read of unmapped address: expected error")
	}
	if _, err := m.Write(0x10, []byte{9}); err == nil {
		t.Errorf("strict write to ROM: expected error")
	}

	m.OpenBus = true

	// read spanning gap → device → gap floats unmapped bytes to zero
	b := []byte{0xAA, 0xAA, 0xAA, 0xAA}
	if n, err := m.Read(0x0F, b); err != nil || n != 4 {
		t.Fatalf("open-bus read: n=%d err=%v", n, err)
	}
	if b[0] != 0 || b[1] != 1 || b[2] != 2 || b[3] != 0 {
		t.Errorf("open-bus read: got % X, want 00 01 02 00", b)
	}

	// writes to ROM and unmapped space are ignored, not faulted
	if n, err := m.Write(0x10, []byte{9, 9, 9}); err != nil || n != 3 {
		t.Errorf("open-bus write: n=%d err=%v", n, err)
	}

	// a read crossing the top of the address space wraps to $000000
	m.Write(0, []byte{0x55})
	b = make([]byte, 2)
	if _, err := m.Read(int(MaxAddress), b); err != nil || b[1] != 0x55 {
		t.Errorf("wrap read: got % X err=%v, want xx 55", b, err)
	}
}

// A wide (32-bit) Mapper maps above 16 MB and doesn't wrap there; the
// default one wraps at 16 MB.
func TestMapperWide(t *testing.T) {
	m := NewMapper()
	m.Wide = true
	m.OpenBus = true
	hi := NewRAM(0x100)
	lo := NewRAM(0x100)
	if err := m.Map(hi, 0x07000000, 0x070000FF); err != nil {
		t.Fatal(err)
	}
	if err := m.Map(lo, 0x000000, 0x0000FF); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Write(0x07000010, []byte{0xAB}); err != nil {
		t.Fatal(err)
	}
	b := []byte{0}
	m.Read(0x10, b)
	if b[0] != 0 {
		t.Errorf("$07000010 aliased to $10")
	}
	m.Read(0x07000010, b)
	if b[0] != 0xAB {
		t.Errorf("read $07000010 = $%02X, want $AB", b[0])
	}
	// Unmapped space up high floats.
	b2 := []byte{1, 2, 3, 4}
	if _, err := m.Read(0x40000000, b2); err != nil || b2[0]|b2[1]|b2[2]|b2[3] != 0 {
		t.Errorf("open bus at $40000000 = %v, %v", b2, err)
	}
	// The top of the 4 GB space is reachable.
	top := NewRAM(0x10)
	if err := m.Map(top, 0xFFFFFFF0, 0xFFFFFFFF); err != nil {
		t.Fatal(err)
	}
	m.Write(0xFFFFFFFF, []byte{0x5A})
	top.Read(0xF, b)
	if b[0] != 0x5A {
		t.Errorf("write $FFFFFFFF missed the top RAM")
	}

	n := NewMapper()
	if err := n.Map(hi, 0x07000000, 0x070000FF); err == nil {
		t.Errorf("24-bit Mapper mapped $07000000")
	}
}

// The Decoder hands the Mapper 32-bit addresses: a sign-extended
// absolute word ($FFFF8000 as int -32768) is $FFFF8000, which a 24-bit
// bus sees as $FF8000.
func TestDecoderNegativeAddress(t *testing.T) {
	m := NewMapper()
	r := NewRAM(0x10000)
	if err := m.Map(r, 0xFF0000, 0xFFFFFF); err != nil {
		t.Fatal(err)
	}
	r.Write(0x8000, []byte{0x12, 0x34})
	d := NewDecoder(m)
	w, err := d.Word(-0x8000)
	if err != nil || w != 0x1234 {
		t.Errorf("Word(-$8000) = $%04X, %v; want $1234", w, err)
	}
}
