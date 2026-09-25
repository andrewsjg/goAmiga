#!/usr/bin/env python3
"""Trace the event dispatch and graphics function table."""
import struct

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

import sys
sys.path.insert(0, "/Users/jga/Development/goAmiga/external-cpu/goAmiga/diag")

# Suppress decode_rom6 auto-output by redirecting stdout temporarily
import io
old_stdout = sys.stdout
sys.stdout = io.StringIO()
from decode_rom6 import dis, decode as _decode_orig
sys.stdout = old_stdout

def decode(start, end, label=""):
    if label: print(f"\n{'='*70}\n  {label}\n{'='*70}")
    o=start
    while o<end:
        a=ROM_BASE+o
        line,no=dis(o)
        nb=no-o
        h=""
        for i in range(0,min(nb,10),2):
            w=rw(o+i)
            if w is not None: h+=f"{w:04X} "
        print(f"${a:06X}  {h:<24s}{line}")
        o=no

# 1. Graphics library function table at $FCB110
# The DC.L $FC5C70 was found at offset $B126
# Table is likely stored as word-swapped pairs
print("="*70)
print("  Graphics library function init table")
print("="*70)

# Find the start marker - look for $FFFF before the table
for off in range(0xB0F0, 0xB120, 2):
    w = rw(off)
    if w == 0xFFFF:
        print(f"  FFFF marker at ${ROM_BASE+off:06X}")

# The function table for MakeLibrary stores addresses as regular 4-byte entries
# but the original search found DC.L $FC5C70 at offset $B126 (not 4-byte aligned)
# Let's check what the actual byte pattern is
print(f"\n  Bytes at $FCB120-$FCB130:")
for off in range(0xB120, 0xB130):
    b = rom[off] if off < len(rom) else 0
    print(f"    [{off-0xB120:2d}] ${ROM_BASE+off:06X}: ${b:02X}")

# Try reading as 4-byte aligned from $B110
print(f"\n  4-byte entries from $FCB110:")
for i in range(0, 40, 4):
    off = 0xB110 + i
    v = rl(off)
    if v is not None:
        # check if high byte pair $00FC suggests address
        hi16 = (v >> 16) & 0xFFFF
        lo16 = v & 0xFFFF
        print(f"    ${ROM_BASE+off:06X}: ${v:08X}  (words: ${hi16:04X} ${lo16:04X})")

# Try reading as 2-byte aligned from $B110
print(f"\n  Words from $FCB110:")
for i in range(0, 50, 2):
    off = 0xB110 + i
    w = rw(off)
    if w is not None:
        print(f"    ${ROM_BASE+off:06X}: ${w:04X}")

# 2. Decode $FE4D82 - the event dispatch function
print()
decode(0x24D82, 0x24E30, "Event dispatch $FE4D82")

# 3. Who calls $FE4D82?
print("\n--- Callers of $FE4D82 ---")
for so in range(0x24000, 0x26000, 2):
    w = rw(so)
    if w is None: continue
    if w == 0x4EB9:
        a = rl(so+2)
        if a == 0xFE4D82:
            print(f"  JSR $FE4D82 at ${ROM_BASE+so:06X}")
    if w == 0x6100:
        d = rw(so+2)
        if d is not None:
            t = (ROM_BASE+so+2+s16(d)) & 0xFFFFFF
            if t == 0xFE4D82:
                print(f"  BSR.W $FE4D82 at ${ROM_BASE+so:06X}")
    if (w & 0xFF00) == 0x6100 and (w & 0xFF) != 0 and (w & 0xFF) != 0xFF:
        d8 = s8(w & 0xFF); t = (ROM_BASE+so+2+d8) & 0xFFFFFF
        if t == 0xFE4D82:
            print(f"  BSR.B $FE4D82 at ${ROM_BASE+so:06X}")
    # BRA patterns
    if w == 0x6000:
        d = rw(so+2)
        if d is not None:
            t = (ROM_BASE+so+2+s16(d)) & 0xFFFFFF
            if t == 0xFE4D82:
                print(f"  BRA.W $FE4D82 at ${ROM_BASE+so:06X}")
    if (w & 0xFF00) == 0x6000 and (w & 0xFF) != 0 and (w & 0xFF) != 0xFF:
        d8 = s8(w & 0xFF); t = (ROM_BASE+so+2+d8) & 0xFFFFFF
        if t == 0xFE4D82:
            print(f"  BRA.B $FE4D82 at ${ROM_BASE+so:06X}")

# 4. Decode the initStruct at $FE544A to find Intuition VBLANK IS_CODE
# The initStruct format (exec/initializers.h):
# CMD_INIT: byte{cmd+sizecount}, then offset(word), then data
# cmd=0xE0: CMD_INITSTRUCT, size=1 long, count=1 → stores 1 long at offset
# cmd=0xC0: CMD_INITSTRUCT, size=1 long, count=0 → stores 1 long at offset  
# cmd=0xD0: CMD_INITSTRUCT, size=1 word, count=1 → stores 1 word
# Entry at $FE5480: E000 0138 0200 → cmd=E0, offset=$0138, long data=next 4 bytes $0200xxxx
# $FE5486: E000 0139 → cmd=E0, offset=$0139...
# Wait, this doesn't align. Let me decode it properly.

print()
print("="*70)
print("  InitStruct data at $FE5448")
print("="*70)
off = 0x25448
while off < 0x254C0:
    cmd = rom[off] if off < len(rom) else 0
    if cmd == 0:
        print(f"  ${ROM_BASE+off:06X}: $00 — END")
        break
    # Parse command byte: bits 7-4 = type, bits 3-0 = count-1
    ctype = (cmd >> 4) & 0xF
    count = (cmd & 0x0F) + 1
    
    if ctype == 0xE:
        # CMD_INITSTRUCT: 24-bit offset follows
        # Format: E{count-1} OO OO [data...]
        # Actually format: {cmd} {offset_hi} {offset_lo} {data...}
        # Each data item is 4 bytes (long)
        off_hi = rom[off+1] if off+1 < len(rom) else 0
        off_lo = rw(off+2)
        struct_off = (off_hi << 8) | (off_lo >> 8) if off_lo else 0
        # Actually in initStruct format:
        # byte 0: command/count
        # byte 1-2: offset (word)
        # byte 3+: data (longs)
        offset_val = rw(off+1)
        print(f"  ${ROM_BASE+off:06X}: CMD ${cmd:02X} offset=${offset_val:04X if offset_val else '????'} count={count}", end="")
        off += 3
        for j in range(count):
            v = rl(off)
            print(f" data=${v:08X}" if v else " data=???", end="")
            off += 4
        print()
    elif ctype == 0xD:
        # word-sized data
        offset_val = rw(off+1)
        print(f"  ${ROM_BASE+off:06X}: CMD ${cmd:02X} offset=${offset_val:04X if offset_val else '????'} count={count}", end="")
        off += 3
        for j in range(count):
            v = rw(off)
            print(f" data=${v:04X}" if v else " data=???", end="")
            off += 2
        print()
    elif ctype == 0xC:
        # long-sized data, count=1
        offset_val = rw(off+1)
        print(f"  ${ROM_BASE+off:06X}: CMD ${cmd:02X} offset=${offset_val:04X if offset_val else '????'} count={count}", end="")
        off += 3
        for j in range(count):
            v = rl(off)
            print(f" data=${v:08X}" if v else " data=???", end="")
            off += 4
        print()
    else:
        print(f"  ${ROM_BASE+off:06X}: CMD ${cmd:02X} (type={ctype:X})")
        off += 1
        break

print("\nDone.")
