#!/usr/bin/env python3
import struct

with open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb') as f:
    r = f.read()

BASE = 0xFC0000

try:
    import capstone
    md = capstone.Cs(capstone.CS_ARCH_M68K, capstone.CS_MODE_M68K_000)

    # Context around FC4986 (LEA $BFD100, A0 — where drive probe starts)
    print("=== FC4970-FC4A40: disk resource drive probe ===")
    data = r[0x4970:0x4970+300]
    for ins in md.disasm(data, BASE+0x4970):
        print(f"  ${ins.address:08X}: {ins.mnemonic:16} {ins.op_str}")
        if ins.mnemonic in ('rts',) and ins.address > BASE+0x4990:
            break

    print()
    print("=== FC4820-FC48D6: disk resource init preamble ===")
    data2 = r[0x4820:0x4820+200]
    for ins in md.disasm(data2, BASE+0x4820):
        print(f"  ${ins.address:08X}: {ins.mnemonic:16} {ins.op_str}")
        if ins.mnemonic in ('rts',) and ins.address > BASE+0x4840:
            break

except ImportError:
    print("capstone not available")
    # manual hex dump around FC4986
    off = 0x4986
    for i in range(0, 60, 2):
        w = struct.unpack_from('>H', r, off+i)[0]
        print(f"  ${BASE+off+i:08X}: {w:04X}")
