package m68k_test

import (
	"testing"

	m68k "github.com/andrewsjg/goamiga/m68k"
	"github.com/andrewsjg/goamiga/m68k/m68ktest"
)

func TestReset(t *testing.T) {
	// init dirty processor
	p := m68ktest.NewProcessor(t)
	for i := 0; i < 8; i++ {
		p.A[i], p.D[i] = 0xFFFFFFFF, 0xFFFFFFFF
	}
	p.SR = 0xFFFFFFFF
	p.PC = 0xFFFFFFFF
	p.M.Write(0x1000, []byte{0xFF, 0xFF, 0xFF, 0xFF})

	// reset and test
	p.Reset()
	for i := 0; i < 8; i++ {
		if p.A[i] != 0 {
			t.Errorf("Address register %d was not reset", i)
		}
		if p.D[i] != 0 {
			t.Errorf("Data register %d was not reset", i)
		}
	}
	if p.SR != 0 {
		t.Error("Condition code register was not reset")
	}
	if p.PC != 0 {
		t.Error("Program counter not reset")
	}
	b := make([]byte, 4)
	_, err := p.M.Read(0x1000, b)
	if err != nil {
		panic(err)
	}
	for i := 0; i < len(b); i++ {
		if b[i] != 0 {
			t.Error("Memory was not zeroed")
			break
		}
	}
}

func BenchmarkHelloWorld(b *testing.B) {
	progHelloWorld := m68ktest.LoadBytes(nil, []byte{
		0x41, 0xFA, 0x00, 0x12, 0x12, 0x18, 0x67, 0x08, 0x10, 0x3C, 0x00, 0x06, 0x4E,
		0x4F, 0x60, 0xF4, 0x4E, 0x72, 0x27, 0x00, 0x48, 0x65, 0x6C, 0x6C, 0x6F, 0x20,
		0x77, 0x6F, 0x72, 0x6C, 0x64, 0x21, 0x0D, 0x0A, 0x00,
	})
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		progHelloWorld.PC = 0x1000
		progHelloWorld.Run()
	}
}

// STOP that lowers the mask below an already-pending interrupt takes the
// interrupt at once, and the processor must then be running the handler,
// not reporting itself stopped.
func TestStopWithPendingInterrupt(t *testing.T) {
	p := m68ktest.NewProcessor(t)
	p.SR = m68k.StatusSupervisor | m68k.StatusIPLMask // mask all
	p.A[7] = 0x3000
	p.M.Write(0x1000, []byte{0x4E, 0x72, 0x20, 0x00}) // STOP #$2000
	p.M.Write(0x6C, []byte{0x00, 0x00, 0x20, 0x00})   // level 3 autovector → $2000
	p.SetInterruptLevel(3)

	if err := p.Step(); err != nil {
		t.Fatal(err)
	}
	if p.PC != 0x2000 {
		t.Fatalf("PC = $%X, want level 3 handler $2000", p.PC)
	}
	if p.RunState() == m68k.RunStateStopped {
		t.Fatal("processor still stopped after taking the interrupt")
	}

	// With nothing pending, STOP does stop.
	p.PC = 0x1000
	p.SR = m68k.StatusSupervisor
	if err := p.Step(); err != nil {
		t.Fatal(err)
	}
	if p.RunState() != m68k.RunStateStopped {
		t.Fatal("STOP with no pending interrupt did not stop")
	}
}
