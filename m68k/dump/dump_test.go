package dump_test

import (
	"io"
	"os"

	m68k "github.com/andrewsjg/goamiga/m68k"
	"github.com/andrewsjg/goamiga/m68k/dump"
	"github.com/andrewsjg/goamiga/m68k/sim"
	"github.com/andrewsjg/goamiga/m68k/srec"
)

func loadSim(path string) *sim.Simulator {
	s, err := sim.New()
	if err != nil {
		panic(err)
	}
	s.Writer = io.Discard
	s.Processor.SR = m68k.StatusSupervisor

	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	recs, err := srec.Read(f)
	if err != nil {
		panic(err)
	}
	for _, rec := range recs {
		if rec.IsData() {
			if _, err := s.Processor.M.Write(rec.Address(), rec.Data()); err != nil {
				panic(err)
			}
		}
	}
	return s
}

func ExampleProcessor() {
	s := loadSim("../testdata/test-sim.h68")
	s.Processor.Run()
	dump.Processor(os.Stdout, s.Processor)

	// output:
	// PC: 00001014 SR: 00002700
	// D0: 00000006 A0: 00001023
	// D1: 00000000 A1: 00000000
	// D2: 00000000 A2: 00000000
	// D3: 00000000 A3: 00000000
	// D4: 00000000 A4: 00000000
	// D5: 00000000 A5: 00000000
	// D6: 00000000 A6: 00000000
	// D7: 00000000 A7: 00000000
}

func ExampleMemory() {
	s := loadSim("../testdata/test-sim.h68")
	s.Processor.Run()
	dump.Memory(os.Stdout, s.Processor.M)

	// output:
	// *
	// 00001000  41 FA 00 12 12 18 67 08  10 3C 00 06 4E 4F 60 F4  |A.....g..<..NO`.|
	// 00001010  4E 72 27 00 48 65 6C 6C  6F 20 77 6F 72 6C 64 21  |Nr'.Hello world!|
	// 00001020  0D 0A 00 00 00 00 00 00  00 00 00 00 00 00 00 00  |................|
	// *
}
