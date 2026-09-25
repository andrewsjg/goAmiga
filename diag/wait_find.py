#!/usr/bin/env python3
import capstone, struct

with open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb') as f:
    r = f.read()

BASE = 0xFC0000
md = capstone.Cs(capstone.CS_ARCH_M68K, capstone.CS_MODE_M68K_000)

# Disassemble around FC08E0-FC0940 to find actual Wait() entrypoint
print("=== FC08C0-FC0950: exec Wait/Signal area ===")
data = r[0x08C0:0x08C0+280]
for ins in md.disasm(data, BASE+0x08C0):
    print(f"  ${ins.address:08X}: {ins.mnemonic:16} {ins.op_str}")

print()
# Also check what the LVO table says for Wait(-$13E from SysBase $0676)
# LVO at $0676-$13E = $0538
offset = 0x0538  # ROM address = $0538, ROM offset = $0538 - $FC0000... 
# Wait: $0538 is in chip RAM, NOT ROM. ROM is at $FC0000.
# SysBase=$0676 is in chip RAM. The JMP table is AT chip RAM, set up by exec init.
# We cannot read it from the ROM file. But we can read from the exec LVO structure.
# The exec body is in ROM at $FC0xxx. The LVO table in chip RAM points back to ROM.
print("=== What's in ROM at the exec function bodies around $FC0800-$FC0900 ===")
data2 = r[0x0800:0x0800+300]
for ins in md.disasm(data2, BASE+0x0800):
    print(f"  ${ins.address:08X}: {ins.mnemonic:16} {ins.op_str}")
    if ins.mnemonic == 'rts' and ins.address > BASE+0x0830 and ins.address < BASE+0x0900:
        print(f"  (--- end of function at ${ins.address:08X} ---)")
