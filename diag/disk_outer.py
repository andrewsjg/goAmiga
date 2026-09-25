#!/usr/bin/env python3
import capstone, struct

with open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb') as f:
    r = f.read()

BASE = 0xFC0000
md = capstone.Cs(capstone.CS_ARCH_M68K, capstone.CS_MODE_M68K_000)

# Context from FC48D6 through the full drive init before FC4984
print("=== FC48D4-FC4985: drive detection outer code ===")
data = r[0x48D4:0x48D4+200]
for ins in md.disasm(data, BASE+0x48D4):
    print(f"  ${ins.address:08X}: {ins.mnemonic:16} {ins.op_str}")
    if ins.mnemonic == 'rts' and ins.address > BASE+0x48E0:
        break

print()
# Also look at what called FC48D6 — look at FC4820-FC48D6
print("=== FC4820-FC48C0: before drive init ===")
data2 = r[0x4820:0x4820+180]
for ins in md.disasm(data2, BASE+0x4820):
    print(f"  ${ins.address:08X}: {ins.mnemonic:16} {ins.op_str}")

print()
# Disassemble FC4B00 area — another PRB access site
print("=== FC4A80-FC4B40: disk device access area ===")
data3 = r[0x4A80:0x4A80+200]
for ins in md.disasm(data3, BASE+0x4A80):
    print(f"  ${ins.address:08X}: {ins.mnemonic:16} {ins.op_str}")
    if ins.mnemonic == 'rts' and ins.address > BASE+0x4A90:
        break
