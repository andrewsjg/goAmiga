#!/usr/bin/env python3
"""Decode ROM code around the COP2LC write at FC6D48."""
import struct

rom = open('Kick13.rom', 'rb').read()
base = 0xFC0000

print("=== ROM code around COP2LC write (FC6D00 - FC6DC0) ===")
for addr in range(0x6D00, 0x6DC0, 2):
    word = struct.unpack('>H', rom[addr:addr+2])[0]
    print(f'  ${base+addr:06X}: ${word:04X}')

print()
print("=== This is part of the VBLANK interrupt handler or LoadView ===")
print("Key instruction at $FC6D6C: MOVE.L D0,$0084(A0) = write COP2LC")
print("D0 comes from:")
print("  View->LOF copper list at offset $32(A1) or")
print("  View->SHF copper list at offset $36(A1)")
print("A0 = custom register base ($DFF000)")
print()
print("=== Checking if this is the VBLANK server ===")
# The VBLANK handler on A500 with Kick 1.3 typically:
# - reads VPOSR to determine LOF/SHF frame
# - writes COP1LC and COP2LC from the system view
# - restarts the Copper

# Let me look at the broader function - go back further
print("=== Broader context FC6C80 - FC6DC0 ===")
for addr in range(0x6C80, 0x6DC0, 2):
    word = struct.unpack('>H', rom[addr:addr+2])[0]
    print(f'  ${base+addr:06X}: ${word:04X}')
