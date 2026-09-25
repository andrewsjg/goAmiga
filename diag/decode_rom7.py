#!/usr/bin/env python3
"""Find Intuition VBLANK handler code and trace the damage/refresh path."""
import struct

ROM_PATH = "/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom"
ROM_BASE = 0xFC0000

with open(ROM_PATH, "rb") as f:
    rom = f.read()

def rw(o):
    if 0 <= o and o+2 <= len(rom): return struct.unpack_from(">H", rom, o)[0]
    return None
def rl(o):
    if 0 <= o and o+4 <= len(rom): return struct.unpack_from(">I", rom, o)[0]
    return None
def s16(v): return v-0x10000 if v>=0x8000 else v
def s8(v): return v-256 if v>=128 else v

# The Intuition VBLANK Interrupt struct is at IntuitionBase+$130
# IS_CODE is at struct offset $12, so IntuitionBase+$142
# We need to find where IntuitionBase+$142 is set
# Look for MOVE.L #addr,$0142(An) or MOVE.L addr,$12(An) where An points to IS struct

# First, search for the Intuition init code that stores the VBLANK handler code pointer
# Near $FE540A area, look earlier for MOVE.L to offset $12 of the interrupt struct
# The interrupt struct at IBase+$130:
#   $00: ln_Succ
#   $04: ln_Pred
#   $08: ln_Type
#   $09: ln_Pri
#   $0A: ln_Name
#   $0E: is_Data
#   $12: is_Code

# Search for writes to IBase+$0142 (= $0130 + $12)
print("--- Searching for MOVE.L #addr,$0142(An) ---")
for so in range(0x0000, 0x40000, 2):
    w = rw(so)
    if w is None: continue
    # MOVE.L #imm,d16(An) = 2B7C xxxx xxxx yyyy
    if (w & 0xF1FF) == 0x2D7C or (w & 0xF1C0) == 0x2BC0:
        pass  # complex, skip
    # Actually: MOVE.L #imm,d16(A5) = 2B7C xxxx xxxx yyyy (opcode for d16(A5))
    # MOVE.L #imm,d16(A6) = 2D7C xxxx xxxx yyyy (opcode for d16(A6))
    if w == 0x2D7C:  # MOVE.L #imm,d16(A6)
        val = rl(so+2)
        disp = rw(so+6)
        if val is not None and disp is not None:
            d = s16(disp)
            if disp == 0x0142:
                print(f"  ${ROM_BASE+so:06X}: MOVE.L #${val:08X},$0142(A6) [VBLANK IS_CODE for IBase]")
            if disp == 0x0012 and 0xFC0000 <= val <= 0xFFFFFF:
                # Check if near the AddIntServer sequence
                if 0x25300 <= so <= 0x25500:  # Near Intuition init
                    print(f"  ${ROM_BASE+so:06X}: MOVE.L #${val:08X},$0012(A6)")
    # Also: MOVE.L #imm,$12(A1) = 237C xxxx xxxx yyyy
    if w == 0x237C:
        val = rl(so+2)
        disp = rw(so+6)
        if val is not None and disp is not None:
            if disp == 0x0012 and 0xFC0000 <= val <= 0xFFFFFF:
                print(f"  ${ROM_BASE+so:06X}: MOVE.L #${val:08X},$0012(A1) [possible IS_CODE]")

# Look for the Intuition VBLANK handler more broadly
# The data at $FE5448 shows a table structure. Let's check if there's a resident structure
# pointing to Intuition's init code, and trace from there

# Actually, the simplest approach: look at the Interrupt struct initialization
# near $FE5410 (LEA $0130(A6),A1). Before that, the IS_CODE would have been set up.
# Let's decode $FE5380-$FE5420
print("\n--- Intuition init around VBLANK setup ($FE5380-$FE5420) ---")
from decode_rom6 import dis, decode
decode(0x25380, 0x25440, "Intuition VBLANK area")

# Also: look for who calls the functions at $FCEFA6 (JSR $FC9C08 from within ClipRect loop)
# That's already known - it's the second blit path for superbitmap

# KEY QUESTION: What drives the periodic invocation?
# Look for timer/copper/VBLANK driven refresh paths
# Search for calls to $FC5C70 or any reference to it
print("\n--- ALL callers of $FC5C70 (BltBitMap dispatcher) ---")
for so in range(0, 0x40000, 2):
    w = rw(so)
    if w is None: continue
    if w == 0x4EB9:
        a = rl(so+2)
        if a == 0xFC5C70:
            print(f"  JSR $FC5C70 at ${ROM_BASE+so:06X}")
    if w == 0x6100:
        d = rw(so+2)
        if d is not None:
            t = (ROM_BASE+so+2+s16(d)) & 0xFFFFFF
            if t == 0xFC5C70:
                print(f"  BSR.W $FC5C70 at ${ROM_BASE+so:06X}")
    if (w&0xFF00)==0x6100 and (w&0xFF)!=0:
        d8=s8(w&0xFF); t=(ROM_BASE+so+2+d8)&0xFFFFFF
        if t==0xFC5C70:
            print(f"  BSR.B $FC5C70 at ${ROM_BASE+so:06X}")

# Search for ALL callers of FC9C08 (BltBitMap_public)
print("\n--- ALL callers of $FC9C08 (BltBitMap_public) ---")
for so in range(0, 0x40000, 2):
    w = rw(so)
    if w is None: continue
    if w == 0x4EB9:
        a = rl(so+2)
        if a == 0xFC9C08:
            print(f"  JSR $FC9C08 at ${ROM_BASE+so:06X}")

# Also: search for where IS_CODE for Intuition VBLANK is stored
# Look at the data table at $FE5448 onwards - it looks like a library function table
# Those MOVEQ/DC.W values after $FE543A are actually ASCII:
# 7069 6F67 2E72 6573 6F75 7263 65 = "potg.resource" - wait that's wrong
# 706F = "po", 7467 = "tg", 6F2E = "o.", 7265 = "re", 736F = "so", 7572 = "ur", 6365 = "ce"
# = "potgo.resource" !
print("\n--- String at $FE543A ---")
s = ""
for i in range(0x2543A - 0xFC0000, 0x2544A - 0xFC0000):
    b = rom[i]
    if 32 <= b < 127:
        s += chr(b)
    else:
        s += f"[{b:02X}]"
print(f"  '{s}'")

# So the Intuition init at $FE53E0-$FE5438 is followed by "potgo.resource" string
# The actual VBLANK handler address must be stored in the data table
# Let's look at what's at the table near $FE5448
print("\n--- Data table at $FE5448 ---")
for i in range(0, 0x40, 4):
    off = 0x25448 + i
    v = rl(off)
    if v is not None:
        print(f"  ${ROM_BASE+off:06X}: ${v:08X}")

# Now let's look at all the references from the first run's output
# The critical BSET #5,$001E(A0) at FE4DBA — this sets bit 5 in a structure
# On Amiga, Layer flag LAYERIREFRESH = 0x20 = bit 5
# Layer->Flags is at offset $0014 in the Layer structure (not $001E)
# But Window->Flags is at offset $001E in the Window structure!
# WFLG_REFRESHBITS = Window flag for refresh type

# Let's decode around $FE4DBA
print("\n--- BSET #5,$001E(A0) context at $FE4DBA ---")
decode(0x24D90, 0x24DE0, "Window flag manipulation at $FE4DBA")

# And BCLR #4,$0022(A0) at $FED71E and BTST #4,$0022(A0) at $FED6F0
# This could be layer->Flags offset $22
print("\n--- Layer flag BCLR at $FED71E ---") 
decode(0x2D700, 0x2D740, "Layer flag at $FED71E")
print("\n--- Layer flag BTST at $FED6F0 ---")
decode(0x2D6D0, 0x2D720, "Layer flag at $FED6F0")

print("\nDone.")
