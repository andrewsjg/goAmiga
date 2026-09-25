#!/usr/bin/env python3
"""
Comprehensive search for INTREQ writes that clear VERTB (bit 5 = $0020)
in Kickstart 1.3 ROM. Also find the Exec IntServer chain dispatcher.
"""

import struct
from capstone import Cs, CS_ARCH_M68K, CS_MODE_M68K_000

ROM_PATH = "/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom"
ROM_BASE = 0xFC0000

with open(ROM_PATH, "rb") as f:
    rom = f.read()

md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)
md.detail = True

def r16(off):
    return struct.unpack(">H", rom[off:off+2])[0]
def r32(off):
    return struct.unpack(">I", rom[off:off+4])[0]

def disasm_at(addr, nbytes):
    off = addr - ROM_BASE
    if off < 0 or off + nbytes > len(rom):
        return []
    code = rom[off:off+nbytes]
    return list(md.disasm(code, addr))

# ═══════════════════════════════════════════════════════════════════
# 1. Find ALL writes to $DFF09C (INTREQ) — absolute addressing
# ═══════════════════════════════════════════════════════════════════
print("=" * 80)
print("ALL writes to INTREQ ($DFF09C) in ROM — absolute address")
print("=" * 80)
print()

# MOVE.W #imm,$DFF09C.L = 33FC xxxx 00DF F09C
# Search for the signature 00DF F09C
sig = bytes([0x00, 0xDF, 0xF0, 0x9C])
pos = 0
while True:
    pos = rom.find(sig, pos)
    if pos < 0:
        break
    # Back up to find the instruction start
    # Likely 33FC (MOVE.W #imm,abs.l) at pos-4
    addr = ROM_BASE + pos
    if pos >= 4:
        op = r16(pos - 4)
        if op == 0x33FC:
            imm = r16(pos - 2)
            instr_addr = ROM_BASE + pos -4
            bits = []
            for b in range(16):
                if imm & (1 << b):
                    names = {0:"TBE",1:"DSKBLK",2:"SOFT",3:"PORTS",4:"COPER",
                             5:"VERTB",6:"BLIT",7:"AUD0",8:"AUD1",9:"AUD2",
                             10:"AUD3",11:"RBF",12:"DSKSYN",13:"EXTER",14:"INTEN",15:"SET/CLR"}
                    bits.append(names.get(b, f"bit{b}"))
            print(f"  ${instr_addr:08X}: MOVE.W #${imm:04X},$DFF09C  ; {' | '.join(bits)}")
    pos += 2

# ═══════════════════════════════════════════════════════════════════
# 2. Find writes to $9C(A0) — offset from custom chip base
# ═══════════════════════════════════════════════════════════════════
print()
print("=" * 80)
print("ALL writes to $9C(A0) in ROM — INTREQ via offset from $DFF000")
print("=" * 80)
print()

# Patterns: MOVE.W Dn,$9C(A0) = 3140+n 009C
# MOVE.W #imm,$9C(A0) = 317C xxxx 009C
for n in range(8):
    opcode = 0x3140 + n
    pat = struct.pack(">HH", opcode, 0x009C)
    pos = 0
    reg = f"D{n}"
    while True:
        pos = rom.find(pat, pos)
        if pos < 0:
            break
        addr = ROM_BASE + pos
        # Get context
        ctx = disasm_at(max(ROM_BASE, addr - 12), 30)
        print(f"  ${addr:08X}: MOVE.W {reg},$9C(A0)")
        for ci in ctx:
            marker = " >>>" if ci.address == addr else "    "
            print(f"  {marker} ${ci.address:08X}: {ci.mnemonic} {ci.op_str}")
        print()
        pos += 2

# MOVE.W #imm,$9C(A0) = 317C xxxx 009C 
pat = bytes([0x31, 0x7C])
pos = 0
while True:
    pos = rom.find(pat, pos)
    if pos < 0:
        break
    if pos + 6 <= len(rom):
        imm = r16(pos + 2)
        off_val = r16(pos + 4)
        if off_val == 0x009C:
            addr = ROM_BASE + pos
            bits = []
            for b in range(16):
                if imm & (1 << b):
                    names = {0:"TBE",1:"DSKBLK",2:"SOFT",3:"PORTS",4:"COPER",
                             5:"VERTB",6:"BLIT",7:"AUD0",8:"AUD1",9:"AUD2",
                             10:"AUD3",11:"RBF",12:"DSKSYN",13:"EXTER",14:"INTEN",15:"SET/CLR"}
                    bits.append(names.get(b, f"bit{b}"))
            print(f"  ${addr:08X}: MOVE.W #${imm:04X},$9C(A0)  ; {' | '.join(bits)}")
    pos += 2

# ═══════════════════════════════════════════════════════════════════
# 3. Find all exec LVO table initialization (function table)
# ═══════════════════════════════════════════════════════════════════
print()
print("=" * 80)
print("Exec library init: looking for MakeFunctions/MakeLibrary patterns")
print("=" * 80)
print()

# The exec library function table is built by MakeFunctions.
# The function vector table in ROM is typically a list of offsets/absolutes.
# For exec 34.2, the function table starts somewhere in ROM.
# Let's find it by looking for the ExitIntr implementation.

# ExitIntr code at $FC0C88: 4CDF 6303 4E73
# This is the implementation. Let's verify:
exitintr_pat = bytes([0x4C, 0xDF, 0x63, 0x03, 0x4E, 0x73])
pos = rom.find(exitintr_pat)
if pos >= 0:
    addr = ROM_BASE + pos
    print(f"  ExitIntr implementation at ${addr:08X}")
    insns = disasm_at(addr, 10)
    for ins in insns:
        print(f"    ${ins.address:08X}: {ins.mnemonic} {ins.op_str}")

# ═══════════════════════════════════════════════════════════════════
# 4. Disassemble what's at ExecBase-$2A in the ROM function table
# ═══════════════════════════════════════════════════════════════════
print()
print("=" * 80)
print("Looking for the ROM routine that would be installed at _LVO -$2A")
print("=" * 80)
print()

# In exec's MakeFunctions, the jump table is built from a funcTable.
# Entries in the funcTable are absolute addresses (32-bit).
# The table is terminated by -1.
# Let's find references to ExitIntr ($FC0C88) in the ROM — 
# they would be in the function table.
exitintr_addr_bytes = struct.pack(">I", 0x00FC0C88)
pos = 0
while True:
    pos = rom.find(exitintr_addr_bytes, pos)
    if pos < 0:
        break
    addr = ROM_BASE + pos
    print(f"  Reference to $FC0C88 at ROM offset ${addr:08X}")
    # Show surrounding data as potential function table entries
    for i in range(-8, 12, 4):
        off = pos + i
        if 0 <= off <= len(rom) - 4:
            val = r32(off)
            marker = " >>>" if i == 0 else "    "
            faddr = ROM_BASE + off
            label = ""
            if val == 0x00FC0C88:
                label = " ← ExitIntr"
            elif ROM_BASE <= val < ROM_BASE + len(rom):
                label = f" → ROM code"
            print(f"  {marker} ${faddr:08X}: ${val:08X}{label}")
    print()
    pos += 4

# ═══════════════════════════════════════════════════════════════════
# 5. Find the VBlank server itself
# ═══════════════════════════════════════════════════════════════════
print()
print("=" * 80)
print("Disassembling around VERTB-related functions")
print("=" * 80)
print()

# The key pattern for the VERTB server chain dispatcher in exec:
# It would have a loop with JSR(A5) and then clear VERTB bit.
# Let's look for all JSR (A5) = 4E95 near a write to $9C(A0) or $DFF09C
jsr_a5 = bytes([0x4E, 0x95])
pos = 0
candidates = []
while True:
    pos = rom.find(jsr_a5, pos)
    if pos < 0:
        break
    # Check ±40 bytes for an INTREQ write
    region = rom[max(0,pos-40):pos+40]
    # Look for 009C pattern (INTREQ offset) or DFF09C
    if b'\x00\x9C' in region or b'\xF0\x9C' in region:
        candidates.append(ROM_BASE + pos)
    pos += 2

print(f"  Found {len(candidates)} JSR (A5) near INTREQ write patterns")
print()

# Show unique ones
seen = set()
for cand in candidates:
    # Group by 64-byte region
    region_key = cand & ~0x3F
    if region_key in seen:
        continue
    seen.add(region_key)
    
    start = max(ROM_BASE, cand - 30)
    insns = disasm_at(start, 80)
    
    # Check if this has a list-walking pattern (MOVEA.L (A1),A1 or similar)
    has_list_walk = any(
        ("(a1)" in i.op_str.lower() and "a1" in i.op_str.lower() 
         and i.mnemonic.lower().startswith("move"))
        for i in insns
    )
    
    if has_list_walk:
        print(f"  ── JSR (A5) + INTREQ + list walk near ${cand:08X} ──")
        for ins in insns[:25]:
            marker = ">>>" if ins.address == cand else "   "
            print(f"    {marker} ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
        print()

# ═══════════════════════════════════════════════════════════════════
# 6. Find the VERTB default handler by disassembling the Exec function
#    table entry that would be at LVO offset -$2A (-42)
# ═══════════════════════════════════════════════════════════════════
print()
print("=" * 80)
print("Finding exec function table to locate LVO -42 handler")
print("=" * 80)
print()

# The exec.library is initialized early in ColdStart.
# Its funcTable is a -1-terminated list of absolute ROM addresses.
# These get installed as JMP instructions growing downward from ExecBase.
# 
# Library base offsets:
#  -6  = Open      (entry 0)
#  -12 = Close     (entry 1)
#  -18 = Expunge   (entry 2)
#  -24 = ExtFunc   (entry 3)
#  -30 = Supervisor (entry 4) -- first exec-specific
#  -36 = ExitIntr  (entry 5)
#  -42 = Schedule  (entry 6)
#  -48 = Reschedule (entry 7)
#  -54 = Switch    (entry 8)
#  -60 = Dispatch  (entry 9)
#  -66 = Exception (entry 10)
#  ...
#
# We found ExitIntr ($FC0C88). If the function table is contiguous,
# the entry after ExitIntr in the table would be Schedule.

# Let's search around where we found the ExitIntr reference
# and see if it's part of a function table.

# Search for 00FC0C88 again and check the table
pos = 0
while True:
    pos = rom.find(exitintr_addr_bytes, pos)
    if pos < 0:
        break
    
    # Check if next entry (4 bytes later) is also a valid ROM address
    if pos + 8 <= len(rom):
        next_val = r32(pos + 4)
        if ROM_BASE <= next_val < ROM_BASE + len(rom):
            print(f"  Potential function table around ${ROM_BASE+pos:08X}:")
            # Show several entries
            for i in range(-16, 48, 4):
                off = pos + i
                if 0 <= off <= len(rom) - 4:
                    val = r32(off)
                    entry_num = (i // 4)
                    # Calculate LVO for this entry relative to ExitIntr at entry 5
                    # ExitIntr is at i=0, so entry 5
                    lvo_offset = -6 * (5 + entry_num)  # rough
                    faddr = ROM_BASE + off
                    label = ""
                    if val == 0x00FC0C88:
                        label = " ← ExitIntr (LVO -36)"
                    elif ROM_BASE <= val < ROM_BASE + len(rom):
                        label = f" → ROM ${val:08X}"
                    elif val == 0xFFFFFFFF:
                        label = " ← TABLE END"
                    elif val == 0x00000000:
                        label = " ← NULL"
                    
                    marker = " >>>" if i == 0 else "    "
                    print(f"  {marker} ${faddr:08X}: ${val:08X}{label}")
            print()
            
            # Now disassemble the entry AFTER ExitIntr (putative LVO -42)
            next_func = r32(pos + 4)
            if ROM_BASE <= next_func < ROM_BASE + len(rom):
                print(f"  Disassembly of putative LVO -42 handler at ${next_func:08X}:")
                insns = disasm_at(next_func, 60)
                for ins in insns[:20]:
                    print(f"    ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
                print()
    pos += 4

print("Done.")
