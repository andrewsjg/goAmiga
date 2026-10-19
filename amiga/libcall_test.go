package amiga

import "testing"

// callLib calls a library function from the host, as whichever task is
// running in user mode: it waits for a user-mode instruction, sets D0-D1
// and A0-A1 and A6 (the base), and calls base+lvo with the return
// address at a marker in chip RAM. When the call returns there it reads
// D0 and puts back every register. A diagnostic: the task it borrows must
// be one that can make the call (most library functions don't mind).
func callLib(t *testing.T, a *Amiga, base uint32, lvo int, d, aregs [2]uint32) uint32 {
	t.Helper()
	const marker = 0x100 // two NOPs in the (unused) vector area's top
	for i := 0; i < 50_000_000 && a.CPU.SR&0x2000 != 0; i++ {
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}
	if a.CPU.SR&0x2000 != 0 {
		t.Fatal("callLib: the CPU never ran in user mode")
	}
	saved := a.CPU.State()
	a.poke16(marker, 0x4E71)
	a.poke16(marker+2, 0x4E71)
	c := a.CPU
	c.D[0], c.D[1], c.A[0], c.A[1], c.A[6] = d[0], d[1], aregs[0], aregs[1], base
	c.A[7] -= 4
	a.poke32(c.A[7], marker)
	c.PC = base + uint32(lvo)
	for i := 0; i < 50_000_000 && c.PC != marker; i++ {
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}
	if c.PC != marker {
		t.Fatalf("callLib: $%X%+d didn't return", base, lvo)
	}
	r := c.D[0]
	c.SetState(saved)
	return r
}
