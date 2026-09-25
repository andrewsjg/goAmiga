#!/usr/bin/env python3
import struct

with open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb') as f:
    r = f.read()

BASE = 0xFC0000

try:
    import capstone
    md = capstone.Cs(capstone.CS_ARCH_M68K, capstone.CS_MODE_M68K_000)
    md.detail = True

    # Context around FC4986 (LEA $BFD100, A0)
    print("=== FC4986: LEA $BFD100, A0 context ===")
    offset = 0x04986 - 0
    data = r[0x4960:0x4960+120]
    for ins in md.disasm(data, BASE+0x4960):
        print(f"  ${ins.address:08X}: {ins.mnemonic} {ins.op_str}")
        if ins.mnemonic in ('rts', 'rte') and ins.address > BASE+0x4970:
            break

    print()
    # Context around FC48D6 (LEA $BFD000 init / CIA-B init)
    print("=== FC4860-FC49B0: disk resource init ===")
    data2 = r[0x4860:0x4860+320]
    for ins in md.disasm(data2, BASE+0x4860):
        print(f"  ${ins.address:08X}: {ins.mnemonic} {ins.op_str}")
        if ins.mnemonic in ('rts', 'rte') and ins.address > BASE+0x4900:
            break

    print()
    # Context around FC4A8A (has MOVE.B #$FF, $BFD100)
    print("=== FC4A70-FC4B00: disk motor start area ===")
    data3 = r[0x4A70:0x4A70+160]
    for ins in md.disasm(data3, BASE+0x4A70):
        print(f"  ${ins.address:08X}: {ins.mnemonic} {ins.op_str}")
        if ins.mnemonic in ('rts', 'rte') and ins.address > BASE+0x4A90:
            break

except ImportError:
    print("capstone not available")
