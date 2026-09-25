#!/usr/bin/env python3
"""Search ROM for writes to BLTCDAT ($DFF070) register."""
import struct

rom = open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/../goAmiga/Kick13.rom', 'rb').read()
base = 0xFC0000

# Search for offset 0x0070 used with A0=$DFF000 in MOVE.W instructions
print("Searching for MOVE.W to ($0070,A0):")
count = 0
for i in range(len(rom) - 3):
    if rom[i:i+2] == bytes([0x00, 0x70]):
        if i >= 2:
            prev = struct.unpack('>H', rom[i-2:i])[0]
            # 3140 = MOVE.W D0,(d16,A0)
            # 317C = MOVE.W #imm,(d16,A0)
            # 3180 = MOVE.W D0,(d16,A0) -- different encoding
            if prev in (0x3140, 0x317C, 0x3180):
                addr = base + i - 2
                ctx = rom[max(0,i-6):min(len(rom),i+8)]
                print(f"  ${addr:06X}: {ctx.hex()}")
                count += 1

# Also search for absolute address $DFF070
print(f"\nSearching for absolute $00DFF070:")
target = bytes([0x00, 0xDF, 0xF0, 0x70])
for i in range(len(rom) - 3):
    if rom[i:i+4] == target:
        addr = base + i
        ctx = rom[max(0,i-4):min(len(rom),i+8)]
        print(f"  ${addr:06X}: {ctx.hex()}")

# Search for the specific trackdisk code that might set BLTCDAT
# The OwnBlitter/WaitBlit at $FEAF48 might do it
print(f"\n=== Disassembling $FEAF48 (WaitBlit/OwnBlitter) ===")
start = 0xFEAF48 - base
for i in range(0, 40, 2):
    w = struct.unpack('>H', rom[start+i:start+i+2])[0]
    print(f"  ${0xFEAF48+i:06X}: {w:04X}", end="")
    if i % 16 == 14:
        print()
print()

# Check FEA38A - the caller that sets up the decode
# Actually, check what's at FEA388 - maybe it initializes AFWM/ALWM/CDAT
print(f"\n=== Searching for 0x5555 patterns near trackdisk code (FEA000-FEB000) ===")
start = 0xFEA000 - base
end = 0xFEB000 - base
for i in range(start, end - 1, 2):
    w = struct.unpack('>H', rom[i:i+2])[0]
    if w == 0x5555:
        addr = base + i
        ctx = rom[max(0,i-4):min(len(rom),i+6)]
        print(f"  ${addr:06X}: {ctx.hex()}")
