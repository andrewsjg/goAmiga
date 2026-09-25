#!/usr/bin/env python3
import sys, struct

with open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb') as f:
    r = f.read()

BASE = 0xFC0000

# 1. Direct absolute refs to $BFD100
pattern = bytes([0x00, 0xBF, 0xD1, 0x00])
print("=== Direct refs to $BFD100 (CIA-B PRB) ===")
i = 0
while True:
    idx = r.find(pattern, i)
    if idx < 0:
        break
    addr = idx + BASE
    # Show surrounding bytes for context
    start = max(0, idx - 6)
    ctx = r[start:idx+8]
    print(f"  ROM offset 0x{idx:05X} (addr ${addr:08X}): ...{ctx.hex(' ')}...")
    i = idx + 1

print()

# 2. Find all LEA $BFDxxx.l, An
print("=== LEA $BFDxxx, An patterns ===")
found_leak = []
for i in range(0, len(r)-6, 2):
    # LEA abs.l, An = 4xF9 where x = reg*2+1... actually opcode = 01000 nn1 111 001
    # Bits 15-12: 0100, bits 11-9: An reg, bits 8-6: 111 (LEA), bits 5-0: 111001 (abs.l)
    # = 0x41F9 | (An << 9) for An=0..7
    # Top byte: 0x41, 0x43, 0x45, 0x47, 0x49, 0x4B, 0x4D, 0x4F
    if r[i+1] == 0xF9 and (r[i] & 0xF1) == 0x41:
        # Check if address high byte = 0xBF
        if r[i+2] == 0x00 and r[i+3] == 0xBF:
            addr_val = struct.unpack('>I', r[i+2:i+6])[0]
            an_reg = (r[i] >> 1) & 0x7
            rom_addr = i + BASE
            print(f"  ${rom_addr:08X}: LEA ${addr_val:08X}, A{an_reg}")
            found_leak.append((rom_addr, an_reg, addr_val))

print()

# 3. Use capstone for full disassembly context around LEA $BFD000, A6
try:
    import capstone
    md = capstone.Cs(capstone.CS_ARCH_M68K, capstone.CS_MODE_M68K_000)
    md.detail = True
    
    for rom_addr, an_reg, addr_val in found_leak[:15]:
        if addr_val == 0x00BFD000:
            offset = rom_addr - BASE
            print(f"=== Disassembly around ${rom_addr:08X} (LEA $BFD000, A{an_reg}) ===")
            chunk = r[offset:offset+80]
            for ins in md.disasm(chunk, rom_addr):
                print(f"  ${ins.address:08X}: {ins.mnemonic} {ins.op_str}")
            print()
except ImportError:
    print("(capstone not available for disassembly)")
