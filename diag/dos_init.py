#!/usr/bin/env python3
import capstone

with open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb') as f:
    r = f.read()

BASE = 0xFC0000
md = capstone.Cs(capstone.CS_ARCH_M68K, capstone.CS_MODE_M68K_000)

# FE4EE8: called by bootstrap task before Wait()
print("=== FE4EE8: bootstrap pre-wait init subroutine ===")
off = 0xFE4EE8 - BASE
data = r[off:off+200]
for ins in md.disasm(data, 0xFE4EE8):
    print(f"  ${ins.address:08X}: {ins.mnemonic:16} {ins.op_str}")
    if ins.mnemonic == 'rts' and ins.address > 0xFE4EF0:
        break

print()
# FE5E6E: after Wait() returns in DOS init
print("=== FE5E6E: after Wait() returns in bootstrap task ===")
off2 = 0xFE5E6E - BASE
data2 = r[off2:off2+200]
for ins in md.disasm(data2, 0xFE5E6E):
    print(f"  ${ins.address:08X}: {ins.mnemonic:16} {ins.op_str}")
    if ins.mnemonic in ('rts','rte') and ins.address > 0xFE5E80:
        break

print()
# FE5EB4: first bsr after Wait()
print("=== FE5EB4: DOS post-wait subroutine ===")
off3 = 0xFE5EB4 - BASE
data3 = r[off3:off3+150]
for ins in md.disasm(data3, 0xFE5EB4):
    print(f"  ${ins.address:08X}: {ins.mnemonic:16} {ins.op_str}")
    if ins.mnemonic == 'rts' and ins.address > 0xFE5EC0:
        break
