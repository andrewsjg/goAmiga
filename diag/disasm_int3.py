#!/usr/bin/env python3
"""
Disassemble the Kickstart 1.3 ROM INT3 (Level 3) interrupt handler at $FC0D14.

INT3 covers VERTB (bit 5), BLIT (bit 6), and COPER (bit 4) interrupts.
The handler reads INTENAR/INTREQR, finds active interrupts, dispatches
to sub-handlers via the Exec IntVects table, returning through ExitIntr.
"""

import struct
from capstone import Cs, CS_ARCH_M68K, CS_MODE_M68K_000

ROM_PATH = "/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom"
ROM_BASE = 0xFC0000

with open(ROM_PATH, "rb") as f:
    rom = f.read()

def r16(off):
    return struct.unpack(">H", rom[off:off+2])[0]

def r32(off):
    return struct.unpack(">I", rom[off:off+4])[0]

# ── Capstone setup ──────────────────────────────────────────────────
md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)
md.detail = True

# ── ExecBase IntVects mapping ──────────────────────────────────────
# Each IntVect is 12 bytes: iv_Data(4) + iv_Code(4) + iv_Node(4)
# IntVects start at ExecBase+$60
# MOVEM.L offset(A6),A1/A5 loads iv_Data→A1, iv_Code→A5
INTVECT_OFFSETS = {
    0x60: "IntVect[0]  TBE    (bit 0)",
    0x6C: "IntVect[1]  DSKBLK (bit 1)",
    0x78: "IntVect[2]  SOFT   (bit 2)",
    0x84: "IntVect[3]  PORTS  (bit 3)  — or COPER for INT3",
    0x90: "IntVect[4]  COPER  (bit 4)  — or VERTB for INT3",
    0x9C: "IntVect[5]  VERTB  (bit 5)  — or BLIT for INT3",
    0xA8: "IntVect[6]  BLIT   (bit 6)  — or AUD0 for INT4",
    0xB4: "IntVect[7]  AUD0   (bit 7)  — or AUD1",
    0xC0: "IntVect[8]  AUD1   (bit 8)  — or AUD2",
    0xCC: "IntVect[9]  AUD2   (bit 9)  — or AUD3",
    0xD8: "IntVect[10] AUD3   (bit 10) — or RBF",
    0xE4: "IntVect[11] DSKSYN (bit 11) — or DSKSYN",
    0xF0: "IntVect[12] RBF    (bit 12) — or EXTER",
    0xFC: "IntVect[13] EXTER  (bit 13) — or NMI",
    0x108: "IntVect[14] NMI   (bit 14)",
}

# ── Annotated disassembly ──────────────────────────────────────────
# For each address, provide a manual annotation
ANNOTATIONS = {
    # ── INT3 handler ($FC0D14) ──
    0xFC0D14: "INT3 ENTRY: save D0-D1/A0-A1/A5-A6",
    0xFC0D18: "A0 = $DFF000 (custom chip base)",
    0xFC0D1E: "A6 = ExecBase (from location $4)",
    0xFC0D22: "D1 = INTENAR ($DFF01C) — enabled interrupt mask",
    0xFC0D26: "test bit 14 = master interrupt enable",
    0xFC0D2A: "if master disabled → exit at $FC0C88",
    0xFC0D2E: "D1 &= INTREQR ($DFF01E) — D1 = enabled AND pending",
    0xFC0D32: "test bit 6 = BLIT (blitter finished)",
    0xFC0D36: "if BLIT not active → skip to VERTB check",
    0xFC0D38: "A1=iv_Data, A5=iv_Code from ExecBase+$9C (BLIT IntVect)",
    0xFC0D3E: "push _LVOExitIntr(A6) as RTS return addr",
    0xFC0D42: "JMP to BLIT interrupt server → server RTS → ExitIntr",
    # ── VERTB dispatch ──
    0xFC0D44: "test bit 5 = VERTB (vertical blank)",
    0xFC0D48: "if VERTB not active → skip to COPER check",
    0xFC0D4A: "A1=iv_Data, A5=iv_Code from ExecBase+$90 (VERTB IntVect)",
    0xFC0D50: "push _LVOExitIntr(A6) as RTS return addr",
    0xFC0D54: "JMP to VERTB interrupt server → server RTS → ExitIntr",
    # ── COPER dispatch ──
    0xFC0D56: "test bit 4 = COPER (copper)",
    0xFC0D5A: "if COPER not active → skip to fallthrough exit",
    0xFC0D5C: "A1=iv_Data, A5=iv_Code from ExecBase+$84 (COPER IntVect)",
    0xFC0D62: "push _LVOExitIntr(A6) as RTS return addr",
    0xFC0D66: "JMP to COPER interrupt server → server RTS → ExitIntr",
    # ── fallthrough ──
    0xFC0D68: "no INT3 bits matched → exit at $FC0C88",
    # ── common exit ($FC0C88) ──
    0xFC0C88: "COMMON EXIT: restore D0-D1/A0-A1/A5-A6",
    0xFC0C8C: "RTE — return from exception",
}

# ── Regions to disassemble ──────────────────────────────────────────

def disasm_region(start_addr, max_bytes, stop_at_rte=False, stop_at_bra=False, stop_addr=None):
    """Disassemble a region, return list of (addr, hex, mnem, ops) tuples."""
    off = start_addr - ROM_BASE
    code = rom[off : off + max_bytes]
    result = []
    for insn in md.disasm(code, start_addr):
        result.append(insn)
        m = insn.mnemonic.lower()
        if stop_at_rte and m == "rte":
            break
        if stop_at_bra and m in ("bra", "bra.w", "bra.b", "bra.l"):
            break
        if stop_addr and insn.address >= stop_addr:
            break
    return result

# ── Disassemble common exit at $FC0C88 ──────────────────────────────
exit_insns = disasm_region(0xFC0C88, 0x20, stop_at_rte=True)

# ── Disassemble INT3 handler $FC0D14 until $FC0D6C ──────────────────
int3_insns = disasm_region(0xFC0D14, 0x58, stop_addr=0xFC0D6B)

# ── Disassemble the ExitIntr equivalent (JMP -$24(A6)) ──────────────
# This is in ROM. Let's find where -$24(A6) points.
# ExitIntr is _LVOExitIntr = -36 = -$24 from ExecBase.
# Since this is a jump table entry, it vectors into ROM code.
# Let's find the actual ExitIntr code by looking up the ROM's
# exec.library jump table.

# ── Print complete annotated disassembly ────────────────────────────

def print_insn(insn, annotation=None, label=None):
    """Pretty-print one instruction."""
    raw = " ".join(f"{b:02X}" for b in insn.bytes)
    ann = annotation or ANNOTATIONS.get(insn.address, "")
    lbl = ""
    if label:
        lbl = f"\n{label}:\n"
    comment = f"  ; {ann}" if ann else ""
    print(f"{lbl}  ${insn.address:08X}:  {raw:<24s}  {insn.mnemonic:<10s} {insn.op_str}{comment}")


print("=" * 95)
print("  Kickstart 1.3 — INT3 (Level 3 Autovector) Interrupt Handler")
print("  Entry: $FC0D14    Covers: COPER (bit 4), VERTB (bit 5), BLIT (bit 6)")
print("=" * 95)
print()

# ── 1. Common exit (referenced by BEQ/BRA as $FC0C88) ──────────────
print("─── Common Interrupt Exit ($FC0C88) ── referenced by all handlers ────────")
print()
for insn in exit_insns:
    ann = ""
    m = insn.mnemonic.lower()
    if "movem" in m and ("(a7)+" in insn.op_str.lower() or "(sp)+" in insn.op_str.lower()):
        ann = "restore saved registers"
    elif m == "rte":
        ann = "Return from Exception — back to interrupted code"
    print_insn(insn, ann)
print()

# ── 2. INT3 handler ────────────────────────────────────────────────
print("─── INT3 Handler Entry ($FC0D14) ────────────────────────────────────────")
print()

# Group and label the sections
for insn in int3_insns:
    addr = insn.address
    
    # Insert section labels
    if addr == 0xFC0D44:
        print()
        print("  ; ─── VERTB check (bit 5) ───────────────────────────────────────────")
    elif addr == 0xFC0D56:
        print()
        print("  ; ─── COPER check (bit 4) ───────────────────────────────────────────")
    elif addr == 0xFC0D68:
        print()
        print("  ; ─── No INT3 bits active ────────────────────────────────────────────")
    
    # Build annotation
    ann = ANNOTATIONS.get(addr, "")
    
    # Extra annotations for MOVEM from IntVect
    m = insn.mnemonic.lower()
    ops = insn.op_str
    if "movem" in m and "(a6)" in ops:
        # Extract offset
        for off_val, desc in INTVECT_OFFSETS.items():
            if f"${off_val:x}" in ops or f"${off_val:02x}" in ops:
                ann = f"load {desc}"
                break
    
    print_insn(insn, ann)

print()

# ── 3. Dispatch mechanism explanation ──────────────────────────────
print("═" * 95)
print()
print("  DISPATCH MECHANISM EXPLAINED")
print("  " + "─" * 50)
print()
print("  For each active interrupt bit, the handler:")
print()
print("    1. MOVEM.L offset(A6), A1/A5")
print("       → Loads two longwords from the ExecBase IntVects table:")
print("         A1 = iv_Data  (interrupt server data pointer)")
print("         A5 = iv_Code  (interrupt server code pointer)")
print()
print("    2. PEA -$24(A6)")
print("       → Pushes the address of _LVOExitIntr (ExecBase - $24 = LVO -36)")
print("         onto the stack as a return address.")
print()
print("    3. JMP (A5)")
print("       → Jumps to the interrupt server code.")
print("         When the server executes RTS, it returns to ExitIntr.")
print()
print("  ExitIntr (_LVOExitIntr, offset -$24 from ExecBase):")
print("    → Restores registers (MOVEM.L (A7)+,D0-D1/A0-A1/A5-A6)")
print("    → Executes RTE to return from the exception")
print("    → This is the code at $FC0C88")
print()
print("  Priority order: BLIT (bit 6) > VERTB (bit 5) > COPER (bit 4)")
print("  Only ONE interrupt is dispatched per invocation.")
print()

# ── 4. ExecBase IntVect table layout ──────────────────────────────
print("  EXECBASE INTVECT TABLE (relevant entries)")
print("  " + "─" * 50)
print()
print("  Offset  Size  Field     Used by INT3")
print("  ------  ----  --------  ---------------------------------")
print("  $84     12    IntVect3  COPER: iv_Data=$84, iv_Code=$88")
print("  $90     12    IntVect4  VERTB: iv_Data=$90, iv_Code=$94")
print("  $9C     12    IntVect5  BLIT:  iv_Data=$9C, iv_Code=$A0")
print()
print("  Each IntVect is 12 bytes: iv_Data(4) + iv_Code(4) + iv_Node(4)")
print("  MOVEM.L $XX(A6),A1/A5 loads:")
print("    A1 ← [ExecBase+$XX]     = iv_Data")
print("    A5 ← [ExecBase+$XX+4]   = iv_Code")
print()

# ── 5. VERTB analysis ──────────────────────────────────────────────
print("═" * 95)
print()
print("  VERTB (bit 5) HANDLING — DETAILED ANALYSIS")
print("  " + "─" * 50)
print()
print("  The VERTB path at $FC0D44-$FC0D54:")
print()
print("    $FC0D44: BTST #5,D1          ; test VERTB bit in (INTENAR & INTREQR)")
print("    $FC0D48: BEQ $FC0D56         ; skip if VERTB not pending+enabled")
print("    $FC0D4A: MOVEM.L $90(A6),A1/A5  ; load VERTB IntVect from ExecBase")
print("    $FC0D50: PEA -$24(A6)        ; push ExitIntr return address")
print("    $FC0D54: JMP (A5)            ; jump to VERTB server chain")
print()
print("  CRITICAL FINDING: The INT3 dispatcher does NOT clear VERTB in INTREQ!")
print("  " + "─" * 70)
print()
print("  There is NO 'MOVE.W #$0020,$DFF09C' anywhere in the INT3 handler.")
print("  The dispatcher only reads INTENAR and INTREQR, checks bits, and")
print("  dispatches to the appropriate interrupt server via JMP (A5).")
print()
print("  WHO CLEARS VERTB?")
print("  The interrupt SERVER itself (pointed to by iv_Code at ExecBase+$94)")
print("  is responsible for clearing the VERTB bit by writing to INTREQ:")
print()
print("    MOVE.W  #$0020, $DFF09C    ; clear VERTB (bit 5)")
print("  or:")
print("    MOVE.W  D0, $DFF09C        ; clear via register with bit 5 set")
print()
print("  The default Exec VERTB server (installed during boot) walks the")
print("  interrupt server chain (IS_CODE/IS_DATA linked list at iv_Node).")
print("  After processing all servers, it clears VERTB in INTREQ.")
print()
print("  FLOW SUMMARY:")
print("    1. Hardware asserts VERTB → INTREQR bit 5 set")
print("    2. CPU takes Level 3 autovector → enters $FC0D14")
print("    3. Handler checks INTENAR & INTREQR, finds bit 5 active")
print("    4. Loads VERTB IntVect: A1=data, A5=code from ExecBase+$90")
print("    5. PEA ExitIntr; JMP (A5) → calls VERTB server")
print("    6. Server processes VBlank work")
print("    7. Server writes #$0020 to $DFF09C → clears VERTB in INTREQ")
print("    8. Server RTS → returns to ExitIntr at $FC0C88")
print("    9. ExitIntr: MOVEM.L (A7)+,regs; RTE")
print()

# ── 6. Now find and disassemble the default VERTB server ──────────
# The default VERTB server is typically the exec IntServer dispatcher.
# Let's search the ROM for the pattern that writes $0020 to $DFF09C
# near an interrupt chain walker.
print("  SEARCHING ROM FOR VERTB CLEAR PATTERNS...")
print("  " + "─" * 50)
print()

# Search for MOVE.W #$0020,$DFF09C in ROM
# Encoding: 33FC 0020 00DF F09C
target_pattern = bytes([0x33, 0xFC, 0x00, 0x20, 0x00, 0xDF, 0xF0, 0x9C])
pos = 0
count = 0
while True:
    pos = rom.find(target_pattern, pos)
    if pos < 0:
        break
    rom_addr = ROM_BASE + pos
    # Show some context
    ctx_start = max(0, pos - 16)
    ctx_insns = disasm_region(rom_addr - 16, 48)
    print(f"  Found MOVE.W #$0020,$DFF09C at ${rom_addr:08X}:")
    for ci in ctx_insns:
        marker = ">>>" if ci.address == rom_addr else "   "
        print(f"    {marker} ${ci.address:08X}: {ci.mnemonic} {ci.op_str}")
    print()
    count += 1
    pos += 2

if count == 0:
    # Try MOVE.W #$0020,offset(A0) where A0=$DFF000
    # Pattern: 3140 009C with prior MOVE.W #$0020,Dx or immediate
    print("  No direct MOVE.W #$0020,$DFF09C found.")
    print("  Searching for MOVE.W #$xxxx,$9C(A0) patterns (A0=$DFF000)...")
    print()
    # Look for 3140 009C (MOVE.W D0,$9C(A0))
    # and 3168 xxxx 009C (MOVE.W xx(A3),$9C(A0))
    # Common pattern: write Dn to $9C(A0)
    for pat_name, pat_bytes in [
        ("MOVE.W D0,$9C(A0)", bytes([0x31, 0x40, 0x00, 0x9C])),
        ("MOVE.W D1,$9C(A0)", bytes([0x31, 0x41, 0x00, 0x9C])),
        ("MOVE.W #imm,$9C(A0)", bytes([0x31, 0x7C])),  # followed by imm, 00 9C
    ]:
        pos2 = 0
        while True:
            pos2 = rom.find(pat_bytes, pos2)
            if pos2 < 0:
                break
            # For the immediate pattern, verify $9C follows
            if pat_name.startswith("MOVE.W #imm"):
                if pos2 + 6 <= len(rom):
                    if rom[pos2+4:pos2+6] != bytes([0x00, 0x9C]):
                        pos2 += 2
                        continue
            ra = ROM_BASE + pos2
            ctx = disasm_region(ra - 20, 56)
            print(f"  Found {pat_name} at ${ra:08X}:")
            for ci in ctx:
                marker = ">>>" if ci.address == ra else "   "
                print(f"    {marker} ${ci.address:08X}: {ci.mnemonic} {ci.op_str}")
            print()
            pos2 += 2

print()
print("═" * 95)
print("  END OF INT3 HANDLER DISASSEMBLY")
print("═" * 95)
