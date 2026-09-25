#!/usr/bin/env python3
"""
Find the Exec interrupt server chain dispatcher in Kick 1.3 ROM.
This is the code that iv_Code points to for chained interrupts (VERTB etc).
It walks the IntServer list, calls each server, then clears INTREQ.
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
    code = rom[off:off+nbytes]
    return list(md.disasm(code, addr))

# ── Strategy 1: Find the exec server chain dispatcher ──────────────
# The chain dispatcher pattern involves:
#   MOVEA.L (A1),A1  ->  2251 (follow ln_Succ to first node)
#   ... TST.L (A1)   ->  4A91
#   ... JSR (A5)     ->  4E95
#   ... then write to INTREQ
#
# Let's search for the typical pattern: 2251 ... 4A91
# which is "MOVEA.L (A1),A1" followed soon by "TST.L (A1)"

print("=" * 80)
print("Searching for Exec Interrupt Server Chain Dispatcher")
print("=" * 80)
print()

# Search for MOVEA.L (A1),A1 = 0x2251
pattern_movea = bytes([0x22, 0x51])
pos = 0
candidates = []
while True:
    pos = rom.find(pattern_movea, pos)
    if pos < 0:
        break
    # Check if TST.L (A1) = 0x4A91 follows within 20 bytes
    region = rom[pos:pos+30]
    if b'\x4a\x91' in region:
        # Also check for JSR (A5) = 0x4E95 nearby
        wider = rom[pos:pos+60]
        if b'\x4e\x95' in wider:
            candidates.append(ROM_BASE + pos)
    pos += 2

print(f"Found {len(candidates)} candidate chain dispatcher locations:")
print()

for cand in candidates:
    # Disassemble 40 instructions from a few bytes before
    start = cand - 4
    insns = disasm_at(start, 100)
    
    # Check if this looks like a real dispatcher (has INTREQ write nearby)
    has_intreq = False
    has_rts = False
    for ins in insns:
        if "$9c" in ins.op_str.lower() and "a0" in ins.op_str.lower():
            has_intreq = True
        if ins.mnemonic.lower() == "rts":
            has_rts = True
    
    # Only show candidates that write to INTREQ offset
    if has_intreq or True:  # show all for now
        print(f"  Candidate at ${cand:08X} (intreq_write={has_intreq}, has_rts={has_rts}):")
        for ins in insns[:25]:
            marker = ">>>" if ins.address == cand else "   "
            print(f"    {marker} ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
        print()

# ── Strategy 2: Look for the specific pattern of the Exec chain walker ──
# In standard Exec 34.2 (Kick 1.3), the server chain dispatcher is:
#
#   _IntServer:
#     MOVEA.L (A1),A1        ; first node
#   .loop:  
#     MOVE.L  (A1),D0        ; d0 = ln_Succ (test end)
#     BEQ.S   .exit
#     MOVE.L  A1,-(SP)       ; save node ptr
#     MOVEA.L $0E(A1),A5     ; IS_CODE
#     MOVEA.L $0A(A1),A1     ; IS_DATA  
#     JSR     (A5)
#     MOVEA.L (SP)+,A1       ; restore node ptr
#     MOVEA.L (A1),A1        ; next ln_Succ
#     BRA.S   .loop
#   .exit:
#     ...clear INTREQ...
#     RTS

# Actually in Exec, IntServer struct offsets are:
# Node: 0=ln_Succ, 4=ln_Pred, 8=ln_Type, 9=ln_Pri, A=ln_Name
# IntServer: +$E = is_Data, +$12 = is_Code
# But the compact version uses IS offsets from the typical Interrupt struct

# Let me search for MOVE.L (A1),D0 (0x2011) near MOVEA.L (A1),A1 (0x2251)
print()
print("─" * 80)
print("Looking for MOVE.L (A1),D0 near chain walk pattern...")
print()

pattern_move_d0 = bytes([0x20, 0x11])  # MOVE.L (A1),D0
pos = 0
while True:
    pos = rom.find(pattern_move_d0, pos)
    if pos < 0:
        break
    # Check nearby for the chain walk indicators
    region = rom[max(0,pos-10):pos+50]
    # MOVEA.L $0E(A1),A5 = 2A69 000E OR is_Code at $12 = 2A69 0012
    if (b'\x2a\x69' in region or b'\x4e\x95' in region):
        addr = ROM_BASE + pos
        # Disassemble context
        start = max(ROM_BASE, addr - 10)
        insns = disasm_at(start, 80)
        
        # Filter: must have JSR (A5) and some list-walking
        has_jsr_a5 = any(i.mnemonic.lower() == "jsr" and "(a5)" in i.op_str for i in insns)
        has_movea_a5 = any("a5" in i.op_str and "$e" in i.op_str.lower() or 
                          "a5" in i.op_str and "$12" in i.op_str.lower() 
                          for i in insns)
        
        if has_jsr_a5:
            print(f"  Chain walker candidate at ${addr:08X}:")
            for ins in insns[:20]:
                marker = ">>>" if ins.address == addr else "   "
                print(f"    {marker} ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
            print()
    pos += 2

# ── Strategy 3: Search for the known Exec IntServer dispatcher directly ──
# In many Kick 1.3 disassemblies, the VERTB dispatcher at the iv_Code
# location is the _IntServer routine. Let me search for its signature:
# 2251 2011 670E 2F09 2A69 000E 2269 000A 4E95 225F 2251 60EC
print()
print("─" * 80)
print("Searching for known Exec _IntServer pattern...")
print()

# _IntServer pattern (typical exec 34.2):
# MOVEA.L (A1),A1     = 2251
# MOVE.L (A1),D0      = 2011
# BEQ.S +xx           = 67xx
# Try partial: 2251 2011 67
for pat_name, pat in [
    ("2251 2011 67", bytes([0x22, 0x51, 0x20, 0x11, 0x67])),
    ("2251 4A91 67", bytes([0x22, 0x51, 0x4A, 0x91, 0x67])),
]:
    pos = 0
    while True:
        pos = rom.find(pat, pos)
        if pos < 0:
            break
        addr = ROM_BASE + pos
        insns = disasm_at(addr, 80)
        print(f"  Pattern '{pat_name}' at ${addr:08X}:")
        for ins in insns[:20]:
            print(f"    ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
        print()
        # Also disassemble backwards a bit
        if pos >= 6:
            pre = disasm_at(addr - 6, 6)
            if pre:
                print(f"  Instructions before:")
                for ins in pre:
                    print(f"    ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
                print()
        pos += 2

# ── Strategy 4: Find what address is actually stored at ExecBase+$94 ──
# We can't read ExecBase RAM from the ROM file alone, but we can find
# where the ROM INSTALLS the VERTB IntVect. Search for writes to 
# ExecBase+$90 area during initialization.
print()
print("─" * 80)
print("Searching for default VERTB IntVect setup in ROM...")
print()

# The ROM exec init code sets up IntVects during ColdStart.
# It typically does: MOVE.L #handler,offset(A6) 
# For VERTB iv_Code at ExecBase+$94:
# LEA intserver(PC),Ax; MOVE.L Ax,$94(A6)  
# Or: MOVE.L #intserver,$94(A6)

# Search for 0094 in context of A6 stores
# 2D7C xxxx xxxx 0094 = MOVE.L #imm,$94(A6)
pat_store_94 = bytes([0x00, 0x94])
pos = 0
while True:
    pos = rom.find(pat_store_94, pos)
    if pos < 0:
        break
    # Check if this is part of a MOVE.L #imm,d16(A6) where d16=$0094
    # 2D7C = MOVE.L #imm,d16(A6), total 8 bytes: 2D7C iiii iiii 0094
    if pos >= 6:
        opword = r16(pos - 6)
        if opword == 0x2D7C:
            imm = r32(pos - 4)
            addr = ROM_BASE + pos - 6
            print(f"  MOVE.L #${imm:08X},$94(A6) at ${addr:08X}")
            # Disassemble the immediate value target
            if ROM_BASE <= imm < ROM_BASE + len(rom):
                print(f"  → Points to ROM address ${imm:08X}, disassembly:")
                target_insns = disasm_at(imm, 60)
                for ins in target_insns[:20]:
                    print(f"    ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
            print()
    # Also check MOVE.L An,d16(A6) pattern
    # 2D4x 0094 where x is register
    if pos >= 2:
        opword = r16(pos - 2)
        if (opword & 0xFFF8) == 0x2D48:  # MOVE.L An,$94(A6)
            reg = opword & 7
            addr = ROM_BASE + pos - 2
            print(f"  MOVE.L A{reg},$94(A6) at ${addr:08X}")
            ctx = disasm_at(max(ROM_BASE, addr - 20), 50)
            for ins in ctx[:15]:
                marker = ">>>" if ins.address == addr else "   "
                print(f"    {marker} ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
            print()
    pos += 2

# ── Strategy 5: Look at $0090 stores too (iv_Data for VERTB list head) ──
print()
print("─" * 80)
print("Searching for IntVect setup at ExecBase offsets $84-$A4...")
print()

for offset, name in [(0x84, "COPER iv_Data"), (0x88, "COPER iv_Code"),
                      (0x90, "VERTB iv_Data"), (0x94, "VERTB iv_Code"),
                      (0x9C, "BLIT iv_Data"),  (0xA0, "BLIT iv_Code")]:
    off_bytes = struct.pack(">H", offset)
    pos = 0
    while True:
        pos = rom.find(off_bytes, pos)
        if pos < 0:
            break
        # Check for MOVE.L #imm,offset(A6) = 2D7C xxxx xxxx OFOF
        if pos >= 6:
            opword = r16(pos - 6)
            if opword == 0x2D7C:
                imm = r32(pos - 4)
                addr = ROM_BASE + pos - 6
                print(f"  ${addr:08X}: MOVE.L #${imm:08X},${offset:02X}(A6)  ; {name}")
        # Check for MOVE.L An,offset(A6)
        if pos >= 2:
            opword = r16(pos - 2)
            if (opword & 0xFFF8) == 0x2D48:
                reg = opword & 7
                addr = ROM_BASE + pos - 2
                print(f"  ${addr:08X}: MOVE.L A{reg},${offset:02X}(A6)  ; {name}")
        pos += 2

print()
print("Done.")
