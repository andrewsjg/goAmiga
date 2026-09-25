#!/usr/bin/env python3
"""
Find how VERTB is actually cleared by tracing the AddIntServer path
and the exec IntServer chain dispatcher in Kickstart 1.3 ROM.
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
def s16(off):
    return struct.unpack(">h", rom[off:off+2])[0]

def disasm_at(addr, nbytes):
    off = addr - ROM_BASE
    if off < 0 or off + nbytes > len(rom):
        return []
    code = rom[off:off+nbytes]
    return list(md.disasm(code, addr))

# ═══════════════════════════════════════════════════════════════════
# 1. Find the exec.library Resident structure
# ═══════════════════════════════════════════════════════════════════
print("=" * 80)
print("Finding exec.library Resident tag")
print("=" * 80)
print()

# Resident matchword: 0x4AFC (ILLEGAL instruction used as marker)
# Followed by pointer to itself
matchword = bytes([0x4A, 0xFC])
pos = 0
exec_resident = None
while True:
    pos = rom.find(matchword, pos)
    if pos < 0:
        break
    # Check if rt_MatchWord is followed by rt_MatchTag pointing to self
    if pos + 26 <= len(rom):
        match_tag = r32(pos + 2)
        end_skip = r32(pos + 6)
        flags = rom[pos + 10]
        version = rom[pos + 11]
        pri = struct.unpack(">b", bytes([rom[pos + 13]]))[0]
        name_ptr = r32(pos + 14)
        
        addr = ROM_BASE + pos
        if match_tag == addr:  # rt_MatchTag points to itself
            # Try to read the name
            if ROM_BASE <= name_ptr < ROM_BASE + len(rom):
                name_off = name_ptr - ROM_BASE
                name = ""
                for i in range(50):
                    if name_off + i < len(rom) and rom[name_off + i] != 0:
                        name += chr(rom[name_off + i])
                    else:
                        break
                
                if "exec" in name.lower():
                    exec_resident = addr
                    init_ptr = r32(pos + 22)
                    print(f"  exec.library Resident at ${addr:08X}")
                    print(f"    rt_EndSkip:  ${end_skip:08X}")
                    print(f"    rt_Flags:    ${flags:02X}")
                    print(f"    rt_Version:  {version}")
                    print(f"    rt_Pri:      {pri}")
                    print(f"    rt_Name:     {name}")
                    print(f"    rt_Init:     ${init_ptr:08X}")
                    print()
    pos += 2

# ═══════════════════════════════════════════════════════════════════
# 2. Find the exec function table
# ═══════════════════════════════════════════════════════════════════
print("=" * 80)
print("Finding exec function table")
print("=" * 80)
print()

# The exec.library init structure (for auto-init residents) contains:
# dataSize, funcTable, dataInit, initFunc
# For exec, since it's special, the init is handled differently.
# But the funcTable is typically referenced from the init code.

# Strategy: Search for ExitIntr ($FC0C88) in a table of ROM addresses
# ExitIntr is at LVO -36, which means it's entry index 5 (counting from 0)
# in the function table (entries 0-3 are Open/Close/Expunge/ExtFunc).

# A function table in exec can be either:
# Type 1: -1 terminated list of absolute addresses (32-bit each)
# Type 2: -2 terminated list of relative offsets (16-bit each)
# The marker word at the start: 0xFFFF = absolute, 0xFFFE = relative

# Search for $FC0C88 as a 32-bit value
target = struct.pack(">I", 0x00FC0C88)
pos = 0
while True:
    pos = rom.find(target, pos)
    if pos < 0:
        break
    addr = ROM_BASE + pos
    
    # Check if this is part of a function table
    # Look back for the table start marker (0xFFFF or 0xFFFE)
    # Function tables start with -1 (abs) or -2 (rel) marker word
    found_table = False
    
    # Check entries before this one (each 4 bytes for absolute table)
    # ExitIntr is entry 5, so table start is 20 bytes (5*4) before
    # Plus 2 bytes for the -1 marker
    table_start_offset = pos - 5 * 4 - 2
    if 0 <= table_start_offset < len(rom):
        marker = r16(table_start_offset)
        if marker == 0xFFFF:
            found_table = True
            print(f"  Found absolute function table at ${ROM_BASE + table_start_offset:08X}")
            print(f"  ExitIntr ($FC0C88) reference at ${addr:08X}")
            print()
            # Print the table
            tpos = table_start_offset + 2  # skip -1 marker
            entry = 0
            lvo_names = {0: "Open", 1: "Close", 2: "Expunge", 3: "ExtFunc",
                        4: "Supervisor", 5: "ExitIntr", 6: "Schedule",
                        7: "Reschedule", 8: "Switch", 9: "Dispatch",
                        10: "Exception", 11: "InitCode", 12: "InitStruct",
                        13: "MakeLibrary", 14: "MakeFunctions", 15: "FindResident",
                        16: "InitResident", 17: "Alert", 18: "Debug",
                        19: "Disable", 20: "Enable", 21: "Forbid",
                        22: "Permit", 23: "SetSR", 24: "SuperState",
                        25: "UserState", 26: "SetIntVector", 27: "AddIntServer",
                        28: "RemIntServer", 29: "Cause"}
            while tpos + 4 <= len(rom):
                val = r32(tpos)
                if val == 0xFFFFFFFF:
                    print(f"    [{entry:3d}] END OF TABLE")
                    break
                lvo = -(entry + 1) * 6
                name = lvo_names.get(entry, "???")
                print(f"    [{entry:3d}] LVO {lvo:5d} (${lvo & 0xFFFF:04X}): ${val:08X}  {name}")
                entry += 1
                tpos += 4
                if entry > 120:
                    print(f"    ... (stopping after {entry} entries)")
                    break
            print()
            
            # Extract key entries
            if entry > 27:
                # AddIntServer is entry 27
                add_int_server_off = table_start_offset + 2 + 27 * 4
                add_int_server = r32(add_int_server_off)
                print(f"  AddIntServer implementation: ${add_int_server:08X}")
                print()
                print("  Disassembly of AddIntServer:")
                insns = disasm_at(add_int_server, 120)
                for ins in insns[:35]:
                    print(f"    ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
                print()
                
            if entry > 26:
                # SetIntVector is entry 26
                set_int_vector_off = table_start_offset + 2 + 26 * 4
                set_int_vector = r32(set_int_vector_off)
                print(f"  SetIntVector implementation: ${set_int_vector:08X}")
                print()
                print("  Disassembly of SetIntVector:")
                insns = disasm_at(set_int_vector, 120)
                for ins in insns[:35]:
                    print(f"    ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
                print()
            
            if entry > 6:
                # Schedule is entry 6 (at ExecBase-$2A)
                schedule_off = table_start_offset + 2 + 6 * 4
                schedule = r32(schedule_off)
                print(f"  Schedule implementation (ExecBase-$2A): ${schedule:08X}")
                print()
                print("  Disassembly of Schedule:")
                insns = disasm_at(schedule, 80)
                for ins in insns[:20]:
                    print(f"    ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
                print()
    
    # Also check for relative (type 2) table
    # In relative tables, each entry is a 16-bit signed offset from table start
    # ExitIntr = entry 5, so 5*2 = 10 bytes in, plus 2 for marker
    rel_table_start = pos  # this doesn't apply for absolute match, skip
    
    pos += 4

# ═══════════════════════════════════════════════════════════════════
# 3. Search for the pattern where D1 is written to INTREQ
# ═══════════════════════════════════════════════════════════════════
print()
print("=" * 80)
print("Search for MOVE.W Dn,$DFF09C.L (register write to INTREQ)")
print("=" * 80)
print()

# MOVE.W Dn,$DFF09C.L = 33Cn 00DF F09C
for n in range(8):
    opcode = 0x33C0 + n
    pat = struct.pack(">H", opcode) + bytes([0x00, 0xDF, 0xF0, 0x9C])
    pos = 0
    while True:
        pos = rom.find(pat, pos)
        if pos < 0:
            break
        addr = ROM_BASE + pos
        ctx = disasm_at(max(ROM_BASE, addr - 20), 50)
        print(f"  MOVE.W D{n},$DFF09C at ${addr:08X}:")
        for ci in ctx:
            marker = ">>>" if ci.address == addr else "   "
            print(f"    {marker} ${ci.address:08X}: {ci.mnemonic} {ci.op_str}")
        print()
        pos += 2

print("Done.")
