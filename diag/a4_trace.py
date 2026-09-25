import capstone, struct

with open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom','rb') as f:
    rom = f.read()

ROM_BASE = 0xFC0000
md = capstone.Cs(capstone.CS_ARCH_M68K, capstone.CS_MODE_M68K_000)

print('=== FC0FE0: context switch jmp(A4) ===')
data = rom[0x0FE0:0x0FE0+20]
for insn in md.disasm(data, ROM_BASE+0x0FE0):
    print(f'  {insn.address:08X}: {insn.mnemonic:12} {insn.op_str}')

print()
print('=== FC0FF2: launch/switch hook routine ===')
data2 = rom[0x0FF2:0x0FF2+60]
for insn in md.disasm(data2, ROM_BASE+0x0FF2):
    print(f'  {insn.address:08X}: {insn.mnemonic:12} {insn.op_str}')
    if insn.mnemonic in ('rts','rte') and insn.address > ROM_BASE+0x0FF2+2:
        break

print()
print('=== All movea.l ...,a4 in exec init area FC0800-FC1100 ===')
data3 = rom[0x0800:0x1100]
for insn in md.disasm(data3, ROM_BASE+0x0800):
    if insn.mnemonic == 'movea.l' and insn.op_str.endswith(', a4'):
        print(f'  {insn.address:08X}: {insn.mnemonic:12} {insn.op_str}')

print()
print('=== lea ...,a4 in exec init area FC0800-FC1100 ===')
for insn in md.disasm(data3, ROM_BASE+0x0800):
    if insn.mnemonic in ('lea', 'movea.l') and insn.op_str.endswith(', a4'):
        print(f'  {insn.address:08X}: {insn.mnemonic:12} {insn.op_str}')
