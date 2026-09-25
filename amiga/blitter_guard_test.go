package amiga

import (
	"testing"

	"github.com/cavaliercoder/go-m68k/m68kmem"
)

func TestBlitterWriteGuardVectors(t *testing.T) {
	a := &Amiga{ChipRAM: m68kmem.NewRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	a.Blitter.GuardWrites = true

	// Poison vectors, then attempt a blit that would smash them
	a.ChipRAM.Write(0x0000, []byte{0xDE, 0xAD, 0xBE, 0xEF})
	a.Blitter.con0 = 0x01FF // USED only, D = $FFFF
	a.Blitter.con1 = 0
	a.Blitter.adat = 0xFFFF
	a.Blitter.afwm = 0xFFFF
	a.Blitter.alwm = 0xFFFF
	a.Blitter.dptH, a.Blitter.dptL = 0, 0x0000
	a.Blitter.dmod = 0
	a.Blitter.Execute(1<<6 | 2) // 1 row, 2 words → $0000,$0002

	got := make([]byte, 4)
	a.ChipRAM.Read(0x0000, got)
	if got[0] != 0xDE || got[1] != 0xAD {
		t.Fatalf("vector page smashed: %02X %02X", got[0], got[1])
	}
}

func TestBlitterWriteGuardAllowsLowTemp(t *testing.T) {
	a := &Amiga{ChipRAM: m68kmem.NewRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	a.Blitter.GuardWrites = true

	// Intentional temp at $0400 — must succeed (damage-repair style)
	// USED, minterm A ($F0), USEA=0 → A from ADAT
	a.Blitter.con0 = 0x01F0
	a.Blitter.con1 = 0
	a.Blitter.adat = 0xA5A5
	a.Blitter.afwm = 0xFFFF
	a.Blitter.alwm = 0xFFFF
	a.Blitter.dptH, a.Blitter.dptL = 0, 0x0400
	a.Blitter.dmod = 0
	a.Blitter.Execute(1<<6 | 2)

	got := make([]byte, 4)
	a.ChipRAM.Read(0x0400, got)
	w0 := uint16(got[0])<<8 | uint16(got[1])
	if w0 != 0xA5A5 {
		t.Fatalf("low temp not written: got $%04X want $A5A5", w0)
	}
}

func TestBlitterWriteGuardBlocksWrapIntoLow(t *testing.T) {
	a := &Amiga{ChipRAM: m68kmem.NewRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	a.Blitter.GuardWrites = true

	b := a.Blitter
	b.lastSize = 256<<6 | 40
	b.dmod = 0
	b.cmod = 0
	// High-start (≥$8000) must not smash tasks/stacks below $8000
	if b.canWriteD(0x0200, 0xD510) {
		t.Fatal("high-start blit must not write $200")
	}
	if b.canWriteD(0x3342, 0xD510) {
		t.Fatal("high-start blit must not write task $3342")
	}
	if b.canWriteD(0x4348, 0xD510) {
		t.Fatal("high-start blit must not write input.device stack SP ~$4348")
	}
	if b.canWriteD(0x7FFE, 0xD510) {
		t.Fatal("high-start blit must not write just below $8000")
	}
	// Mid-chip start must not walk into input.device task/stack either
	if b.canWriteD(0x4348, 0x2000) {
		t.Fatal("mid-chip start must not smash input.device stack")
	}
	if b.canWriteD(0x3342, 0x6000) {
		t.Fatal("mid-chip start must not smash input.device task")
	}
	// Boot buffers below $2C00 remain writable from low starts
	if !b.canWriteD(0x1800, 0x1000) {
		t.Fatal("boot/trackdisk buffers below $2C00 must stay writable")
	}
	// Planes (WB and insert-disk) must remain writable — including across $D000
	b.lastSize = 256<<6 | 40
	b.dmod, b.cmod = 0, 0
	if !b.canWriteD(0xD510, 0xD510) {
		t.Fatal("high-start must allow plane addresses")
	}
	if !b.canWriteD(0xD510+80*100, 0xD510) {
		t.Fatal("high-start must allow within-plane rows")
	}
	if !b.canWriteD(0xA572, 0xA572) {
		t.Fatal("insert-disk plane start must allow itself")
	}
	if !b.canWriteD(0xC4B2, 0xA572) {
		t.Fatal("insert-disk plane2 region must be writable from plane1 start")
	}
	if !b.canWriteD(0xDC00, 0xC4B2) {
		t.Fatal("insert-disk plane2 must write past $D000")
	}
	// Heap past display band must be blocked for plane-start blits
	if b.canWriteD(0x18000, 0xD510) {
		t.Fatal("plane blit must not write heap at $18000")
	}
	if b.canWriteD(0x20000, 0xA572) {
		t.Fatal("insert-disk blit must not write heap at $20000")
	}
	// Runaway: far from start — blocked by geometric span
	if b.canWriteD(0xD510+0x18000, 0xD510) {
		t.Fatal("must block writes more than maxSpan from start")
	}
	// Heap: tiny blit must NOT get a multi-KB paint window (old 4KB floor).
	b.lastSize = 1<<6 | 1 // 1 row, 1 word
	b.dmod = 0
	b.cmod = 0
	if b.canWriteD(0x22028, 0x20000) {
		t.Fatal("1-word heap blit must not reach task-sized offset ~8KB away")
	}
	if !b.canWriteD(0x20000, 0x20000) {
		t.Fatal("1-word heap blit must still write its own DPT")
	}
	if !b.canWriteD(0x20002, 0x20000) {
		t.Fatal("1-word heap blit may write the immediate next word within slack")
	}
	// Mid-chip: same — no 4KB floor
	if b.canWriteD(0x6000, 0x4000) {
		t.Fatal("1-word mid-chip blit must not reach +$2000")
	}
	// Low temps still work outside ExecBase band
	b.lastSize = 32<<6 | 16
	b.dmod = 0
	if !b.canWriteD(0x0200, 0x0180) {
		t.Fatal("low-start blit must be allowed to write $200")
	}
	if b.canWriteD(0x0004, 0x0400) {
		t.Fatal("vector page must never be writable")
	}
	// ExecBase / ThisTask band always denied
	if b.canWriteD(0x078A, 0x0400) {
		t.Fatal("must not blit ExecBase.ThisTask")
	}
	if b.canWriteD(0x0676, 0xD510) {
		t.Fatal("must not blit ExecBase from plane start")
	}
}

// Tasks on Exec lists (and their stacks) must be denied.
func TestBlitterWriteGuardDeniesTasks(t *testing.T) {
	a := &Amiga{ChipRAM: m68kmem.NewRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	a.Blitter.GuardWrites = true
	put32 := func(addr int, v uint32) {
		a.ChipRAM.Write(addr, []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
	}
	const (
		eb     = 0x400
		task   = 0x22028
		stackL = 0x23000
		stackU = 0x24000
	)
	put32(4, eb)
	// Empty MemList
	put32(eb+0x142, eb+0x146) // lh_Head = &lh_Tail
	put32(eb+0x146, 0)
	put32(eb+0x14A, eb+0x142)
	// TaskReady: one task
	put32(eb+0x196, task)
	put32(eb+0x19A, 0)
	put32(eb+0x19E, task)
	// Task node: succ = &TaskReady.lh_Tail
	put32(task+0, eb+0x19A)
	put32(task+4, eb+0x196)
	a.ChipRAM.Write(task+8, []byte{1}) // NT_TASK
	put32(task+0x3A, stackL)
	put32(task+0x3E, stackU)
	// SP slightly outside [lo,hi] (e.g. interrupt) — stack must still be denied.
	put32(task+0x36, stackL-16)
	put32(eb+0x114, task) // ThisTask

	b := a.Blitter
	b.lastSize = 256<<6 | 40
	b.buildDeny()
	if b.canWriteD(task, 0xD510) {
		t.Fatal("must not blit over Task struct at $22028")
	}
	if b.canWriteD(stackL+0x100, 0xD510) {
		t.Fatal("must not blit over task stack (SP slightly out of range)")
	}
	if !b.canWriteD(0xD510, 0xD510) {
		t.Fatal("plane must stay writable")
	}
}

// graphics.library base must be denied — OwnBlitter lock smash freezes the OS.
func TestBlitterWriteGuardDeniesLibraries(t *testing.T) {
	a := &Amiga{ChipRAM: m68kmem.NewRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	a.Blitter.GuardWrites = true
	put32 := func(addr int, v uint32) {
		a.ChipRAM.Write(addr, []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
	}
	const (
		eb   = 0x400
		lib  = 0x5000
		name = 0x5100
	)
	put32(4, eb)
	put32(eb+0x142, eb+0x146)
	put32(eb+0x146, 0)
	put32(eb+0x14A, eb+0x142)
	put32(eb+0x196, eb+0x19A)
	put32(eb+0x19A, 0)
	put32(eb+0x19E, eb+0x196)
	put32(eb+0x1A4, eb+0x1A8)
	put32(eb+0x1A8, 0)
	put32(eb+0x1AC, eb+0x1A4)
	put32(eb+0x17A, lib)
	put32(eb+0x17E, 0)
	put32(eb+0x182, lib)
	put32(lib+0, eb+0x17E)
	put32(lib+4, eb+0x17A)
	// type 0 on purpose — live WB boot has type=0 for graphics.library
	a.ChipRAM.Write(lib+8, []byte{0})
	put32(lib+10, name)
	a.ChipRAM.Write(name, []byte("graphics.library\x00"))
	// lib_NegSize=$60, lib_PosSize=$80 — LVO table below base
	a.ChipRAM.Write(lib+0x10, []byte{0x00, 0x60})
	a.ChipRAM.Write(lib+0x12, []byte{0x00, 0x80})

	b := a.Blitter
	b.lastSize = 256<<6 | 40
	b.buildDeny()
	if b.canWriteD(lib+0x20, 0x20000) {
		t.Fatal("must not blit over graphics.library data head")
	}
	if b.canWriteD(lib-0x30, 0x20000) {
		t.Fatal("must not blit over graphics.library LVO jump table (negative offsets)")
	}
	// start in display band cannot write low LVO even without deny
	if b.canWriteD(lib-0x30, 0xD510) {
		t.Fatal("display-start blit must not write low LVO")
	}
	if b.canWriteD(0x4348, 0xD510) {
		t.Fatal("input.device stack margin still denied")
	}
	if b.canWriteD(0x2C68, 0x20000) {
		t.Fatal("keyboard.device LVO band must be denied")
	}
	if b.canWriteD(0x2F18, 0x20000) {
		t.Fatal("timer.device LVO band must be denied")
	}
	// Full hi-res plane strip on heap must stay within span (not truncated at 16KB)
	b.lastSize = 256<<6 | 40 // 256 rows × 40 words
	b.dmod = 0
	b.cmod = 0
	start := uint32(0x20000)
	end := 0x20000 + 256*80 - 2
	if !b.canWriteD(end, start) {
		t.Fatalf("heap plane blit must allow end of 256×80 strip @$%X", end)
	}
	if !b.canWriteD(0xD510, 0xD510) {
		t.Fatal("plane must stay writable")
	}
}

// Freelist MemChunk headers must be denied even when geometrically "in range".
func TestBlitterWriteGuardFreelistHeaders(t *testing.T) {
	a := &Amiga{ChipRAM: m68kmem.NewRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	a.Blitter.GuardWrites = true
	// Synthetic ExecBase + MemHeader + one free chunk abutting a fake plane.
	// ExecBase at $400, MemList at $400+$142 = $542.
	// MemHeader at $600, free chunk at $C960 (as on real WB boot).
	put32 := func(addr int, v uint32) {
		a.ChipRAM.Write(addr, []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
	}
	const (
		eb    = 0x400
		mh    = 0x600
		chunk = 0xC960
	)
	put32(4, eb)
	// MemList: lh_Head=mh, lh_Tail=0, lh_TailPred=mh
	put32(eb+0x142, mh)
	put32(eb+0x146, 0)
	put32(eb+0x14A, mh)
	// MemHeader node: succ=&lh_Tail (eb+0x146), pred=list
	put32(mh+0, eb+0x146)
	put32(mh+4, eb+0x142)
	put32(mh+0x10, chunk)   // mh_First
	put32(mh+0x14, 0x100)   // mh_Lower
	put32(mh+0x18, 0x7E800) // mh_Upper
	put32(mh+0x1C, 0x1000)  // mh_Free
	// Free chunk at $C960: next=0, size=$BB0 (gap before $D510)
	put32(chunk, 0)
	put32(chunk+4, 0xBB0)

	b := a.Blitter
	b.lastSize = 256<<6 | 40
	b.dmod = 0
	b.buildDeny()
	if len(b.denyLo) < 2 {
		t.Fatalf("expected deny ranges for mh_First and chunk, got %d", len(b.denyLo))
	}
	// Entire free chunk must be denied (header + body)
	if b.canWriteD(chunk, 0xD510) {
		t.Fatal("must not blit over freelist chunk header at $C960")
	}
	if b.canWriteD(chunk+4, 0xD510) {
		t.Fatal("must not blit over freelist chunk size at $C964")
	}
	if b.canWriteD(chunk+0x100, 0xD510) {
		t.Fatal("must not blit over freelist chunk body (later AllocMem poison)")
	}
	// Plane itself still OK
	if !b.canWriteD(0xD510, 0xD510) {
		t.Fatal("plane body must remain writable")
	}
}
