#!/usr/bin/env python3
"""
Find exec function table (type 2 = relative offsets) by examining init code,
then locate the IntServer chain dispatcher.
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
def s16(off):
    return struct.unpack(">h", rom[off:off+2])[0]
def r32(off):
    return struct.unpack(">I", rom[off:off+4])[0]

def disasm_at(addr, nbytes):
    off = addr - ROM_BASE
    if off < 0 or off + nbytes > len(rom):
        return []
    code = rom[off:off+nbytes]
    return list(md.disasm(code, addr))

# ═══════════════════════════════════════════════════════════════════
# 1. Exec init is at $FC00D2 — disassemble to understand structure
# ═══════════════════════════════════════════════════════════════════
print("=" * 80)
print("Exec init code at $FC00D2")
print("=" * 80)
print()

insns = disasm_at(0xFC00D2, 200)
for ins in insns[:50]:
    print(f"  ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")

# ═══════════════════════════════════════════════════════════════════
# 2. Find type-2 function tables (relative offset, 0xFFFE marker)
# ═══════════════════════════════════════════════════════════════════
print()
print("=" * 80)
print("Searching for type-2 function tables (0xFFFE marker)")
print("=" * 80)
print()

marker_bytes = bytes([0xFF, 0xFE])
pos = 0
while True:
    pos = rom.find(marker_bytes, pos)
    if pos < 0:
        break
    
    # Check if this looks like a function table:
    # After 0xFFFE, there should be a series of 16-bit relative offsets
    # Each offset when added to its own position gives a valid ROM address
    # Table ends with 0xFFFF
    
    valid_entries = 0
    tpos = pos + 2
    entries = []
    while tpos + 2 <= len(rom):
        val = r16(tpos)
        if val == 0xFFFF:  # end marker
            valid_entries += 1  # count the end marker as valid
            break
        # It's a 16-bit signed offset from the table base
        sval = s16(tpos)
        target = ROM_BASE + pos + sval  # relative to table start
        target_off = target - ROM_BASE
        if 0 <= target_off < len(rom):
            entries.append((tpos - pos, sval, target))
            valid_entries += 1
        else:
            break
        tpos += 2
        if valid_entries > 150:
            break
    
    if valid_entries >= 10:  # At least 10 function entries
        table_addr = ROM_BASE + pos
        print(f"  Type-2 function table at ${table_addr:08X} ({valid_entries} entries)")
        
        # Check if entry 5 (ExitIntr) points to $FC0C88
        if len(entries) > 5:
            _, sval, target = entries[5]
            if target == 0xFC0C88:
                print(f"  *** Entry 5 = ExitIntr ($FC0C88) - THIS IS THE EXEC TABLE! ***")
            print(f"      Entry 5: offset={sval:+d} → ${target:08X}")
        
        # Print all entries if this looks like exec's table
        lvo_names = {
            0: "Open", 1: "Close", 2: "Expunge", 3: "ExtFunc",
            4: "Supervisor", 5: "ExitIntr", 6: "Schedule",
            7: "Reschedule", 8: "Switch", 9: "Dispatch",
            10: "Exception", 11: "InitCode", 12: "InitStruct",
            13: "MakeLibrary", 14: "MakeFunctions", 15: "FindResident",
            16: "InitResident", 17: "Alert", 18: "Debug",
            19: "Disable", 20: "Enable", 21: "Forbid",
            22: "Permit", 23: "SetSR", 24: "SuperState",
            25: "UserState", 26: "SetIntVector", 27: "AddIntServer",
            28: "RemIntServer", 29: "Cause", 30: "Allocate",
            31: "Deallocate", 32: "AllocMem", 33: "AllocAbs",
            34: "FreeMem", 35: "AvailMem", 36: "AllocEntry",
            37: "FreeEntry", 38: "Insert", 39: "AddHead",
            40: "AddTail", 41: "Remove", 42: "RemHead",
            43: "RemTail", 44: "Enqueue", 45: "FindName",
            46: "SetTaskPri", 47: "SetSignal", 48: "SetExcept",
            49: "FindTask", 50: "AllocSignal",
        }
        
        if len(entries) > 5 and entries[5][2] == 0xFC0C88:
            print()
            for i, (off, sval, target) in enumerate(entries[:60]):
                lvo = -(i + 1) * 6
                name = lvo_names.get(i, f"func_{i}")
                print(f"    [{i:3d}] LVO {lvo:5d}: ${target:08X}  {name}")
            
            print()
            
            # ── Disassemble key functions ──
            
            # AddIntServer (entry 27)
            if len(entries) > 27:
                _, _, ais_addr = entries[27]
                print(f"  AddIntServer at ${ais_addr:08X}:")
                ais_insns = disasm_at(ais_addr, 150)
                for ins in ais_insns[:40]:
                    print(f"    ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
                print()
            
            # SetIntVector (entry 26)
            if len(entries) > 26:
                _, _, siv_addr = entries[26]
                print(f"  SetIntVector at ${siv_addr:08X}:")
                siv_insns = disasm_at(siv_addr, 150)
                for ins in siv_insns[:40]:
                    print(f"    ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
                print()
                
            # Schedule (entry 6, ExecBase-$2A)
            if len(entries) > 6:
                _, _, sched_addr = entries[6]
                print(f"  Schedule at ${sched_addr:08X} (ExecBase-$2A):")
                sched_insns = disasm_at(sched_addr, 80)
                for ins in sched_insns[:20]:
                    print(f"    ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
                print()
        print()
    
    pos += 2

print("Done.")
