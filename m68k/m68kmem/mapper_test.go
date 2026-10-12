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
