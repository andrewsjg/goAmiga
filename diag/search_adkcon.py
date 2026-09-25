#!/usr/bin/env python3
"""Search ROM for ADKCON writes."""
import sys

with open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb') as f:
    rom = f.read()

# Search for MOVE.W #xxxx,$DFF09E (ADKCON absolute)
for off in range(len(rom) - 8):
    if rom[off:off+2] == bytes([0x33, 0xFC]):
        if rom[off+4:off+8] == bytes([0x00, 0xDF, 0xF0, 0x9E]):
            val = (rom[off+2] << 8) | rom[off+3]
            addr = 0xFC0000 + off
            print(f'${addr:06X}: MOVE.W #${val:04X},$DFF09E (ADKCON)')

# Search for MOVE.W #xxxx,$9E(An) patterns
for off in range(len(rom) - 6):
    w = (rom[off] << 8) | rom[off+1]
    if w == 0x337C:  # MOVE.W #imm,d(An)
        disp = (rom[off+4] << 8) | rom[off+5]
        if disp == 0x009E:
            val = (rom[off+2] << 8) | rom[off+3]
            addr = 0xFC0000 + off
            an = 0  # need to figure out which An
            print(f'${addr:06X}: MOVE.W #${val:04X},$9E(A?) (ADKCON)')

print("\nDone.")
