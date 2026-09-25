#!/usr/bin/env python3
"""Deeper analysis: find JMP table vectors and call chains."""
import struct
from capstone import Cs, CS_ARCH_M68K, CS_MODE_M68K_000

rom = open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb').read()
BASE = 0xFC0000
md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)

def disasm_at(addr, nbytes=80):
    off = addr - BASE
    region = rom[off:off+nbytes]
    return list(md.disasm(region, addr))

def print_disasm(addr, nbytes=80, label=""):
    if label:
        print(f"\n--- {label} ---")
    for insn in disasm_at(addr, nbytes):
        hex_bytes = ' '.join(f'{b:02x}' for b in insn.bytes)
        print(f'  ${insn.address:06X}: {hex_bytes:24s} {insn.mnemonic:10s} {insn.op_str}')

# The call chain for FC5E12:
# JSR at $FCA0BA -> $FC5CAC
# Let's see what's at FCA0BA
print("=== Call chain for $FC5E12 (BltBitMap inner) ===")
print_disasm(0xFCA090, 120, "Caller at $FCA0BA area")

# Find JMP vectors pointing to our function entries
# Function entries we found:
entries = {
    0xFC5CAC: "BltBitMap inner (contains FC5E12)",
    0xFC55A4: "Template blit (contains FC577E)",
    0xFC5BC2: "BltClear plane-by-plane (contains FC5C58)",
    0xFC75DC: "QBlit blit executor (contains FC7630)",
    0xFC54C4: "BltClear main (contains FC553A)",
}

print("\n\n=== Searching for JMP vectors to our function entries ===")
for entry, name in entries.items():
    for i in range(0, len(rom) - 5, 2):
        w = struct.unpack_from('>H', rom, i)[0]
        if w == 0x4EF9:  # JMP abs.L
            addr = struct.unpack_from('>I', rom, i + 2)[0]
            if addr == entry:
                print(f'  JMP ${BASE+i:06X} -> ${entry:06X} ({name})')

# Also look for the function entries being stored in tables (as data, not JMP)
print("\n\n=== Searching for address references as data ===")
for entry, name in entries.items():
    hi = (entry >> 16) & 0xFFFF
    lo = entry & 0xFFFF
    for i in range(0, len(rom) - 3, 2):
        hi_w = struct.unpack_from('>H', rom, i)[0]
        lo_w = struct.unpack_from('>H', rom, i + 2)[0]
        if hi_w == hi and lo_w == lo:
            addr = BASE + i
            # Skip if it's JSR/JMP (already found those)
            if i >= 2:
                prev = struct.unpack_from('>H', rom, i - 2)[0]
                if prev in (0x4EB9, 0x4EF9):
                    continue
            print(f'  Address ${entry:06X} referenced at ${addr:06X} ({name})')

# Let's look at the FCA0EE and FCA21A subroutines called from the BltBitMap inner loop
# These are called during the multi-plane blit loop
print("\n\n=== Subroutine $FCA0EE (called during BltBitMap plane loop) ===")
print_disasm(0xFCA0EE, 60)

print("\n\n=== Subroutine $FCA21A (called during BltBitMap plane loop) ===")
print_disasm(0xFCA21A, 60)

# Now let's look at the actual function start for the BltBitMap variant ($FC5CAC)
print("\n\n=== Full function $FC5CAC (BltBitMap inner) ===")
print_disasm(0xFC5CAC, 400)

# And the BltClear main at $FC54C4
print("\n\n=== Full function $FC54C4 (BltClear) ===")
print_disasm(0xFC54C4, 224)

# And the template blit at $FC55A4
print("\n\n=== Function near $FC55A4 (BltTemplate/DrawImage) ===")
# Actually $FC55A4 had stack access - let's check if there's a different entry
# Check what's at $FC55A4
insns = disasm_at(0xFC55A4, 8)
for i in insns:
    print(f'  ${i.address:06X}: {i.mnemonic} {i.op_str}')

# So FC55A4 is: MOVEA.L 4(A7),A1 — gets arg from stack. This IS a function entry
# Let's also look for the BltTemplate ($FC5638) area
print_disasm(0xFC55A4, 300, "BltTemplate function from $FC55A4")
