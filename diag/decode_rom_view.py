#!/usr/bin/env python3
"""Decode ROM code at FCD5C2 that writes bad copper list pointers."""
import struct

rom = open('Kick13.rom', 'rb').read()
base = 0xFC0000

print("=== ROM code at FCD5A0 - FCD5F0 ===")
for addr in range(0xD5A0, 0xD5F0, 2):
    word = struct.unpack('>H', rom[addr:addr+2])[0]
    print(f'  ${base+addr:06X}: ${word:04X}')

print()
print("=== Broader context FCD580 - FCD620 ===")
for addr in range(0xD580, 0xD620, 2):
    word = struct.unpack('>H', rom[addr:addr+2])[0]
    print(f'  ${base+addr:06X}: ${word:04X}')
