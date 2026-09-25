#!/usr/bin/env python3
"""Find Intuition VBLANK handler and damage path.
Traces: VBLANK → Intuition handler → damage list → BltBitMap"""
import struct, sys

ROM_PATH = "/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom"
ROM_BASE = 0xFC0000
with open(ROM_PATH, "rb") as f:
    rom = f.read()
def rw(o):
    if 0<=o and o+2<=len(rom): return struct.unpack_from(">H", rom, o)[0]
    return None
def rl(o):
    if 0<=o and o+4<=len(rom): return struct.unpack_from(">I", rom, o)[0]
    return None
def s16(v): return v-0x10000 if v>=0x8000 else v
def s8(v): return v-256 if v>=128 else v
def s32(v): return v-0x100000000 if v>=0x80000000 else v

sys.path.insert(0, "/Users/jga/Development/goAmiga/external-cpu/goAmiga/diag")
from decode_rom6 import dis, decode

# =====================================================================
# 1. Decode the Intuition VBLANK sub at $FE5120 (called from the handler)
# =====================================================================
print("="*70)
print("  1. Intuition VBLANK subroutine at $FE5120")
print("="*70)
decode(0x25120, 0x25340, "$FE5120 — full decode")

# =====================================================================
# 2. Decode Intuition VBLANK handler entry (IS_CODE points here)
# The initStruct data table sets IS_CODE. Find possible entry before $FE538E.
# =====================================================================
print("\n" + "="*70)
print("  2. Intuition VBLANK handler entry")
print("="*70)
# Scan back to find entry (look for MOVEM or LINK)
decode(0x25320, 0x253B2, "VBLANK handler near $FE5320-$FE53B0")

# =====================================================================
# 3. Callers of $FE5120
# =====================================================================
print("\n" + "="*70)
print("  3. Callers of $FE5120")
print("="*70)
for so in range(0, 0x40000, 2):
    w = rw(so)
    if w is None: continue
    if w == 0x4EB9:
        a = rl(so+2)
        if a == 0xFE5120:
            print(f"  JSR $FE5120 at ${ROM_BASE+so:06X}")
    if w == 0x6100:
        d = rw(so+2)
        if d is not None:
            t = (ROM_BASE+so+2+s16(d)) & 0xFFFFFF
            if t == 0xFE5120:
                print(f"  BSR.W $FE5120 at ${ROM_BASE+so:06X}")
    if (w & 0xFF00) == 0x6100 and (w & 0xFF) != 0 and (w & 0xFF) != 0xFF:
        d8 = s8(w & 0xFF); t = (ROM_BASE+so+2+d8) & 0xFFFFFF
        if t == 0xFE5120:
            print(f"  BSR.B $FE5120 at ${ROM_BASE+so:06X}")

# =====================================================================
# 4. Search for BltBitMap GfxBase LVO calls
# BltBitMap LVO = -$1E (from graphics.library)
# Pattern: JSR -$1E(A6) = 4EAE FFE2
# =====================================================================
print("\n" + "="*70)
print("  4. Graphics BltBitMap LVO -$1E calls")
print("="*70)
for so in range(0, 0x40000, 2):
    w = rw(so)
    if w == 0x4EAE:
        d = rw(so+2)
        if d == 0xFFE2:
            print(f"  JSR -$1E(A6) at ${ROM_BASE+so:06X}")

# =====================================================================
# 5. Find $FC5C70 address stored in jump tables or DC.L
# Search for 00FC5C70 anywhere in ROM (could be in a library vector table)
# =====================================================================
print("\n" + "="*70)
print("  5. References to $FC5C70 in ROM data")
print("="*70)
target = 0x00FC5C70
for so in range(0, len(rom)-3):
    v = rl(so)
    if v == target:
        print(f"  DC.L $FC5C70 at ROM offset ${so:06X} (addr ${ROM_BASE+so:06X})")

# Also search for 4EF9 00FC5C70 (JMP $FC5C70) in jump table
print("\n--- JMP $FC5C70 in ROM ---")
for so in range(0, 0x40000, 2):
    w = rw(so)
    if w == 0x4EF9:
        a = rl(so+2)
        if a == 0x00FC5C70:
            print(f"  JMP $FC5C70 at ${ROM_BASE+so:06X}")

# =====================================================================
# 6. Decode the initStruct data at $FE544A to find IS_CODE
# The format is documented in exec/initializers.h
# =====================================================================
print("\n" + "="*70)
print("  6. IntuitionBase initStruct data at $FE544A")
print("="*70)
# Read raw data around $FE544A = offset 0x2544A
base = 0x2544A
print(f"  Raw data at $FE544A:")
for i in range(0, 64, 2):
    w = rw(base + i)
    if w is not None:
        print(f"    ${ROM_BASE+base+i:06X}: ${w:04X}")

# =====================================================================
# 7. Search for MOVE.L #handler_addr,$0012(A1) near Intuition VBLANK init
# IS_CODE is at offset $12 in the Interrupt struct
# $0130(A6) is the struct start, but code might set up via A1
# =====================================================================
print("\n" + "="*70)
print("  7. IS_CODE stores near Intuition VBLANK init ($FE5380-$FE5480)")
print("="*70)
# Search for MOVE.L #imm,$0012(A1) = 237C xxxxxxxx 0012
# Also MOVE.L #imm,$0142(A6) = 2D7C xxxxxxxx 0142
for so in range(0x25380, 0x25480, 2):
    w = rw(so)
    if w is None: continue
    # MOVE.L #xxx,$nnnn(A1) = 237C imm32 disp16
    if w == 0x237C:
        imm = rl(so+2); disp = rw(so+6)
        if imm is not None and disp is not None:
            print(f"  MOVE.L #${imm:08X},${disp:04X}(A1) at ${ROM_BASE+so:06X}")
    # MOVE.L #xxx,$nnnn(A6) = 2D7C imm32 disp16
    if w == 0x2D7C:
        imm = rl(so+2); disp = rw(so+6)
        if imm is not None and disp is not None:
            print(f"  MOVE.L #${imm:08X},${disp:04X}(A6) at ${ROM_BASE+so:06X}")

# Broader IS_CODE search across full ROM for Intuition-related
print("\n--- All MOVE.L #xxx,$0012(A1) in ROM ---")
for so in range(0, 0x40000, 2):
    w = rw(so)
    if w == 0x237C:
        imm = rl(so+2); disp = rw(so+6)
        if imm is not None and disp is not None and disp == 0x0012:
            print(f"  MOVE.L #${imm:08X},$0012(A1) at ${ROM_BASE+so:06X}")

# =====================================================================
# 8. Find the Intuition initStruct IS_CODE by looking at $FE54C0 func table
# =====================================================================
print("\n" + "="*70)
print("  8. Intuition library function table at $FE54C0")
print("="*70)
base = 0x254C0
print(f"  Entries (word offsets or absolute):")
for i in range(0, 80, 2):
    w = rw(base + i)
    if w is not None:
        # Check if it's a negative offset (FFFF = end of table)
        if w == 0xFFFF:
            print(f"    ${ROM_BASE+base+i:06X}: $FFFF (end of table)")
            break
        print(f"    ${ROM_BASE+base+i:06X}: ${w:04X}")

# =====================================================================
# 9. Decode Intuition init code at $FE53CA more broadly
# =====================================================================
print("\n" + "="*70)
print("  9. Intuition init code $FE53CA-$FE5480")
print("="*70)
decode(0x253CA, 0x25480, "Intuition init (broader)")

print("\nDone.")
