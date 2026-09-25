#!/usr/bin/env python3
"""Find the Exec IntServer chain dispatcher by searching for its signature patterns."""

import struct
from capstone import Cs, CS_ARCH_M68K, CS_MODE_M68K_000

with open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb') as f:
    rom = f.read()

md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)
BASE = 0xFC0000

def disasm(addr, n):
    off = addr - BASE
    return list(md.disasm(rom[off:off+n], addr))

def r16(off):
    return struct.unpack(">H", rom[off:off+2])[0]
def s16(off):
    return struct.unpack(">h", rom[off:off+2])[0]

# ── 1. Search for MOVEM.L $E(A1),A1/A5 ── the IS_DATA/IS_CODE load ──
# Encoding: 4CE9 2200 000E
pattern1 = bytes([0x4C, 0xE9, 0x22, 0x00, 0x00, 0x0E])
print("=" * 80)
print("Searching for MOVEM.L $E(A1),A1/A5 (IntServer IS_DATA/IS_CODE load)")
print("=" * 80)
pos = 0
while True:
    pos = rom.find(pattern1, pos)
    if pos < 0:
        break
    addr = BASE + pos
    start = max(BASE, addr - 20)
    insns = disasm(start, 100)
    print(f"\n  Found at ${addr:08X}:")
    for ins in insns[:30]:
        marker = ">>>" if ins.address == addr else "   "
        print(f"  {marker} ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
    pos += 2

# ── 2. Also search for separate loads: MOVEA.L $E(A1),A1 and MOVEA.L $12(A1),A5
# MOVEA.L $E(A1),A1 = 2269 000E
pattern2 = bytes([0x22, 0x69, 0x00, 0x0E])
print("\n" + "=" * 80)
print("Searching for MOVEA.L $E(A1),A1 (IS_DATA load)")
print("=" * 80)
pos = 0
while True:
    pos = rom.find(pattern2, pos)
    if pos < 0:
        break
    addr = BASE + pos
    start = max(BASE, addr - 20)
    insns = disasm(start, 80)
    print(f"\n  Found at ${addr:08X}:")
    for ins in insns[:25]:
        marker = ">>>" if ins.address == addr else "   "
        print(f"  {marker} ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
    pos += 2

# ── 3. Search for MOVEA.L $12(A1),A5 (IS_CODE load)
# MOVEA.L $12(A1),A5 = 2A69 0012
pattern3 = bytes([0x2A, 0x69, 0x00, 0x12])
print("\n" + "=" * 80)
print("Searching for MOVEA.L $12(A1),A5 (IS_CODE load)")
print("=" * 80)
pos = 0
while True:
    pos = rom.find(pattern3, pos)
    if pos < 0:
        break
    addr = BASE + pos
    start = max(BASE, addr - 20)
    insns = disasm(start, 80)
    print(f"\n  Found at ${addr:08X}:")
    for ins in insns[:25]:
        marker = ">>>" if ins.address == addr else "   "
        print(f"  {marker} ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
    pos += 2

# ── 4. Also try the combined MOVEM.L $E(A1),regs pattern with different reg masks
# Maybe it loads more registers or different ones
# MOVEM.L $E(A1),xxx = 4CE9 xxxx 000E
pattern4 = bytes([0x4C, 0xE9])
print("\n" + "=" * 80)
print("Searching for any MOVEM.L $E(A1),regs")
print("=" * 80)
pos = 0
while True:
    pos = rom.find(pattern4, pos)
    if pos < 0:
        break
    if pos + 6 <= len(rom):
        mask = r16(pos + 2)
        disp = s16(pos + 4)
        if disp == 0x0E:
            addr = BASE + pos
            # Decode mask
            names_d = ["D0","D1","D2","D3","D4","D5","D6","D7"]
            names_a = ["A0","A1","A2","A3","A4","A5","A6","SP"]
            regs = []
            for i in range(8):
                if mask & (1 << i):
                    regs.append(names_d[i])
            for i in range(8):
                if mask & (1 << (8+i)):
                    regs.append(names_a[i])
            regstr = "/".join(regs)
            start = max(BASE, addr - 16)
            insns = disasm(start, 80)
            print(f"\n  MOVEM.L $E(A1),{regstr} at ${addr:08X}:")
            for ins in insns[:20]:
                marker = ">>>" if ins.address == addr else "   "
                print(f"  {marker} ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
    pos += 2

print("\nDone.")
