#!/usr/bin/env python3
"""Find all AddIntServer and SetIntVector calls in ROM, and trace the chain dispatcher."""

import struct
from capstone import Cs, CS_ARCH_M68K, CS_MODE_M68K_000

with open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb') as f:
    rom = f.read()

md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)
BASE = 0xFC0000

def disasm(addr, n):
    off = addr - BASE
    if off < 0 or off + n > len(rom):
        return []
    return list(md.disasm(rom[off:off+n], addr))

def r16(off):
    return struct.unpack(">H", rom[off:off+2])[0]
def r32(off):
    return struct.unpack(">I", rom[off:off+4])[0]

# _LVOAddIntServer = -168 = -$A8 → JSR -$A8(A6) = 4EAE FF58
add_pat = bytes([0x4E, 0xAE, 0xFF, 0x58])

# _LVOSetIntVector = -162 = -$A2 → JSR -$A2(A6) = 4EAE FF5E
set_pat = bytes([0x4E, 0xAE, 0xFF, 0x5E])

print("=" * 80)
print("All calls to _LVOAddIntServer (JSR -$A8(A6))")
print("=" * 80)
pos = 0
while True:
    pos = rom.find(add_pat, pos)
    if pos < 0:
        break
    addr = BASE + pos
    start = max(BASE, addr - 20)
    insns = disasm(start, 50)
    print(f"\n  Call at ${addr:08X}:")
    for ins in insns[:15]:
        marker = ">>>" if ins.address == addr else "   "
        print(f"  {marker} ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
    pos += 2

print()
print("=" * 80)
print("All calls to _LVOSetIntVector (JSR -$A2(A6))")
print("=" * 80)
pos = 0
while True:
    pos = rom.find(set_pat, pos)
    if pos < 0:
        break
    addr = BASE + pos
    start = max(BASE, addr - 20)
    insns = disasm(start, 50)
    print(f"\n  Call at ${addr:08X}:")
    for ins in insns[:15]:
        marker = ">>>" if ins.address == addr else "   "
        print(f"  {marker} ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")
    pos += 2

# Now disassemble the AddIntServer implementation
# We need to find AddIntServer LVO. Looking for MOVEQ #5,D0 (= 7005) near AddIntServer
# Actually, let me find AddIntServer by looking at the jump table entry
# Since we know _LVOExitIntr is at -36, and AddIntServer is at -168,
# the JMP table at ExecBase-168 would contain JMP xxx
# But we can't read ExecBase RAM. 
# 
# Instead, let me follow the Exec init to find function table refs.
# From the Exec init at $FC00D2, it branches to $FC3100, then $FC014C.
# Let's look at $FC014C.

print()
print("=" * 80)
print("Exec warm-start/cold-start code at $FC014C")
print("=" * 80)
print()
for ins in disasm(0xFC014C, 300)[:70]:
    print(f"  ${ins.address:08X}: {ins.mnemonic:<10s} {ins.op_str}")

# Also look for references to the ExitIntr code at $FC0C88
# In the jump table, there would be JMP $FC0C88 = 4EF9 00FC 0C88
jmp_exitintr = bytes([0x4E, 0xF9, 0x00, 0xFC, 0x0C, 0x88])
print()
print("=" * 80)
print("JMP $FC0C88 (ExitIntr) references in ROM")
print("=" * 80)
pos = 0
while True:
    pos = rom.find(jmp_exitintr, pos)
    if pos < 0:
        break
    addr = BASE + pos
    print(f"  JMP $FC0C88 at ${addr:08X}")
    # Show surrounding JMP instructions (likely function table entries)
    for off_delta in range(-18, 24, 6):
        ea = addr + off_delta
        ea_off = ea - BASE
        if 0 <= ea_off < len(rom) - 6:
            op = r16(ea_off)
            if op == 0x4EF9:
                target = r32(ea_off + 2)
                marker = ">>>" if off_delta == 0 else "   "
                entry = off_delta // 6
                print(f"  {marker} ${ea:08X}: JMP ${target:08X}  (delta={entry})")
    pos += 2

print()
print("Done.")

# Write output to file as well
import sys
if __name__ == "__main__":
    pass  # output already goes to stdout
