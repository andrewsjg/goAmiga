package m68k_test

import (
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	m68k "github.com/andrewsjg/goamiga/m68k"
	"github.com/andrewsjg/goamiga/m68k/m68kmem"
)

// SingleStepTests (github.com/SingleStepTests/680x0, formerly Tom Harte's
// ProcessorTests): about 8,000 randomly generated single-instruction tests
// per 68000 operation, each recording full register and memory state before
// and after, captured from an implementation verified against real
// hardware. Download the 68000/v1/*.json.gz files into testdata/680x0/
// (git-ignored), then:
//
//	go test -run TestSingleStep -v .                  # all files
//	SINGLESTEP=ADD.b,MOVE.w go test -run TestSingleStep -v .
//	SINGLESTEP_STRICT=1 ...                           # fail on any mismatch
//
// By default it reports a pass rate per file (and cycle-count agreement) and
// only fails if a file has more failures, or more cycle-count mismatches,
// than recorded for it in testdata/singlestep_baseline.txt.

type ssState struct {
	D0       uint32      `json:"d0"`
	D1       uint32      `json:"d1"`
	D2       uint32      `json:"d2"`
	D3       uint32      `json:"d3"`
	D4       uint32      `json:"d4"`
	D5       uint32      `json:"d5"`
	D6       uint32      `json:"d6"`
	D7       uint32      `json:"d7"`
	A0       uint32      `json:"a0"`
	A1       uint32      `json:"a1"`
	A2       uint32      `json:"a2"`
	A3       uint32      `json:"a3"`
	A4       uint32      `json:"a4"`
	A5       uint32      `json:"a5"`
	A6       uint32      `json:"a6"`
	USP      uint32      `json:"usp"`
	SSP      uint32      `json:"ssp"`
	SR       uint32      `json:"sr"`
	PC       uint32      `json:"pc"`
	Prefetch []uint32    `json:"prefetch"`
	RAM      [][2]uint32 `json:"ram"`
}

func (s *ssState) d() [8]uint32 {
	return [8]uint32{s.D0, s.D1, s.D2, s.D3, s.D4, s.D5, s.D6, s.D7}
}

func (s *ssState) a() [7]uint32 {
	return [7]uint32{s.A0, s.A1, s.A2, s.A3, s.A4, s.A5, s.A6}
}

type ssTest struct {
	Name    string  `json:"name"`
	Initial ssState `json:"initial"`
	Final   ssState `json:"final"`
	Length  int     `json:"length"`
}

// sparseMem is a 24-bit address space holding only the bytes a test uses.
type sparseMem map[uint32]byte

func (m sparseMem) Read(addr int, p []byte) (int, error) {
	for i := range p {
		p[i] = m[uint32(addr+i)&0xFFFFFF]
	}
	return len(p), nil
}

func (m sparseMem) Write(addr int, p []byte) (int, error) {
	for i, v := range p {
		m[uint32(addr+i)&0xFFFFFF] = v
	}
	return len(p), nil
}

func (m sparseMem) Reset() error { return nil }

// runSingleStep runs one test and returns a description of each mismatch,
// and whether the cycle count matched.
func runSingleStep(tc *ssTest) (diffs []string, cyclesOK bool) {
	diffs, cyclesOK, _ = runSingleStepCycles(tc)
	return
}

func runSingleStepCycles(tc *ssTest) (diffs []string, cyclesOK bool, cycles int) {
	in, out := &tc.Initial, &tc.Final
	mem := sparseMem{}
	for _, b := range in.RAM {
		mem[b[0]&0xFFFFFF] = byte(b[1])
	}
	// The prefetch queue holds the instruction's first two words, which
	// live at PC and PC+2.
	var w [2]byte
	for i, v := range in.Prefetch {
		binary.BigEndian.PutUint16(w[:], uint16(v))
		mem.Write(int(in.PC)+2*i, w[:])
	}

	p := &m68k.Processor{M: m68kmem.NewDecoder(mem)}
	p.D = in.d()
	a := in.a()
	copy(p.A[:7], a[:])
	p.SR = in.SR
	p.USP, p.SSP = in.USP, in.SSP
	if in.SR&m68k.StatusSupervisor != 0 {
		p.A[7] = in.SSP
	} else {
		p.A[7] = in.USP
	}
	p.PC = in.PC

	if err := p.Step(); err != nil {
		diffs = append(diffs, fmt.Sprintf("error: %v", err))
	}

	d := out.d()
	for i := range d {
		if p.D[i] != d[i] {
			diffs = append(diffs, fmt.Sprintf("D%d=$%08X want $%08X", i, p.D[i], d[i]))
		}
	}
	fa := out.a()
	for i := range fa {
		if p.A[i] != fa[i] {
			diffs = append(diffs, fmt.Sprintf("A%d=$%08X want $%08X", i, p.A[i], fa[i]))
		}
	}
	usp, ssp := p.USP, p.SSP
	if p.SR&m68k.StatusSupervisor != 0 {
		ssp = p.A[7]
	} else {
		usp = p.A[7]
	}
	if usp != out.USP {
		diffs = append(diffs, fmt.Sprintf("USP=$%08X want $%08X", usp, out.USP))
	}
	if ssp != out.SSP {
		diffs = append(diffs, fmt.Sprintf("SSP=$%08X want $%08X", ssp, out.SSP))
	}
	if p.SR&0xFFFF != out.SR {
		diffs = append(diffs, fmt.Sprintf("SR=$%04X want $%04X", p.SR&0xFFFF, out.SR))
	}
	if p.PC&0xFFFFFF != out.PC&0xFFFFFF {
		diffs = append(diffs, fmt.Sprintf("PC=$%06X want $%06X", p.PC&0xFFFFFF, out.PC&0xFFFFFF))
	}
	nram := 0
	for _, b := range out.RAM {
		if got := mem[b[0]&0xFFFFFF]; got != byte(b[1]) {
			if nram < 4 {
				diffs = append(diffs, fmt.Sprintf("[$%06X]=$%02X want $%02X", b[0], got, b[1]))
			}
			nram++
		}
	}
	if nram > 4 {
		diffs = append(diffs, fmt.Sprintf("... %d RAM bytes differ", nram))
	}
	return diffs, p.LastCycles == tc.Length, p.LastCycles
}

// disputed reports tests whose expected result contradicts the 68000
// Programmer's Reference Manual, so a mismatch is not counted as a failure:
//
//   - ASR by a register count larger than the operand size, on a negative
//     value: the data expects C = X = 0, but the manual (and MAME) set them
//     from the last bit shifted out, which is the sign bit (1). At a count
//     equal to the size the data itself gives C = X = 1.
//   - Two ASL.b tests reported as wrong in the suite's issue #4.
func disputed(tc *ssTest) bool {
	// Reported as wrong in the suite's issue #4: the upper bytes of D2
	// change on a byte-sized shift.
	if tc.Name == "e502 [ASL.b Q, D2] 1583" || tc.Name == "e502 [ASL.b Q, D2] 1761" {
		return true
	}
	op := uint16(tc.Initial.Prefetch[0])
	if op&0xF138 == 0xE020 && op&0xC0 != 0xC0 { // ASR Dx,Dy
		sz := op >> 6 & 3
		bits := uint32(8 << sz)
		d := tc.Initial.d()
		count := d[op>>9&7] % 64
		v := d[op&7]
		if count > bits && v>>(bits-1)&1 != 0 {
			return true
		}
	}
	return false
}

func loadSingleStep(path string) ([]ssTest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	var tests []ssTest
	err = json.NewDecoder(gz).Decode(&tests)
	return tests, err
}

func TestSingleStep(t *testing.T) {
	files, _ := filepath.Glob("testdata/680x0/*.json.gz")
	if len(files) == 0 {
		t.Skip("no SingleStepTests data in testdata/680x0 (see singlestep_test.go)")
	}
	if sel := os.Getenv("SINGLESTEP"); sel != "" {
		want := map[string]bool{}
		for _, s := range strings.Split(sel, ",") {
			want[s] = true
		}
		var keep []string
		for _, f := range files {
			if want[strings.TrimSuffix(filepath.Base(f), ".json.gz")] {
				keep = append(keep, f)
			}
		}
		files = keep
	}
	baseline := readBaseline("testdata/singlestep_baseline.txt")
	strict := os.Getenv("SINGLESTEP_STRICT") != ""

	type result struct {
		name                          string
		pass, total, cycles, disputed int
		emuCycles, realCycles         int
	}
	var results []result
	totalPass, total := 0, 0
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".json.gz")
		tests, err := loadSingleStep(file)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		r := result{name: name, total: len(tests)}
		shown, cycShown := 0, 0
		for i := range tests {
			diffs, cyc := func() (d []string, c bool) {
				defer func() {
					if e := recover(); e != nil {
						d = []string{fmt.Sprintf("panic: %v", e)}
					}
				}()
				var n int
				d, c, n = runSingleStepCycles(&tests[i])
				r.emuCycles += n
				r.realCycles += tests[i].Length
				if !c && cycShown < 6 && os.Getenv("SINGLESTEP_CYCLES") != "" {
					t.Logf("  cycles %s: %d want %d", tests[i].Name, n, tests[i].Length)
					cycShown++
				}
				return d, c
			}()
			if cyc {
				r.cycles++
			}
			if len(diffs) == 0 {
				r.pass++
				continue
			}
			if disputed(&tests[i]) {
				r.pass++
				r.disputed++
				continue
			}
			if shown < 3 && (testing.Verbose() || strict) {
				t.Logf("  %s: %s", tests[i].Name, strings.Join(diffs, "; "))
				shown++
			}
		}
		results = append(results, r)
		totalPass += r.pass
		total += r.total
		pct := 100 * float64(r.pass) / float64(r.total)
		note := ""
		if r.disputed > 0 {
			note = fmt.Sprintf(" (%d disputed)", r.disputed)
		}
		t.Logf("%-14s %5d/%5d pass (%5.1f%%), cycles match %5.1f%% [%d off] (emulated/real %.2f)%s", name, r.pass, r.total, pct,
			100*float64(r.cycles)/float64(r.total), r.total-r.cycles, float64(r.emuCycles)/float64(max(r.realCycles, 1)), note)
		if strict && r.pass != r.total {
			t.Errorf("%s: %d failures", name, r.total-r.pass)
		}
		if limit, ok := baseline[name]; ok {
			if r.total-r.pass > limit[0] {
				t.Errorf("%s: %d failures, baseline allows %d", name, r.total-r.pass, limit[0])
			}
			if r.total-r.cycles > limit[1] {
				t.Errorf("%s: %d cycle-count mismatches, baseline allows %d", name, r.total-r.cycles, limit[1])
			}
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return float64(results[i].pass)/float64(results[i].total) < float64(results[j].pass)/float64(results[j].total)
	})
	t.Logf("TOTAL %d/%d pass (%.2f%%)", totalPass, total, 100*float64(totalPass)/float64(max(total, 1)))
}

// readBaseline reads "NAME failures cycleMismatches" lines: the most of
// each allowed per file.
func readBaseline(path string) map[string][2]int {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string][2]int{}
	for _, line := range strings.Split(string(b), "\n") {
		var name string
		var n, c int
		if k, _ := fmt.Sscanf(line, "%s %d %d", &name, &n, &c); k == 3 {
			out[name] = [2]int{n, c}
		}
	}
	return out
}
