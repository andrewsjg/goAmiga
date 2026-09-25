#!/usr/bin/env python3
"""Disassemble the FEA970 blitnode callback and FEA932 decode_copy."""
import struct

rom = open('../goAmiga/Kick13.rom', 'rb').read()
base = 0xFC0000

def word(addr):
    off = addr - base
    return struct.unpack('>H', rom[off:off+2])[0]

def long(addr):
    off = addr - base
    return struct.unpack('>I', rom[off:off+4])[0]

print("=== FEA932: decode_copy function ===")
# Raw bytes
start = 0xFEA932 - base
for i in range(0, 64, 2):
    w = struct.unpack('>H', rom[start+i:start+i+2])[0]
    print(f"  ${0xFEA932+i:06X}: {w:04X}", end="")
    if (i // 2) % 8 == 7:
        print()
print()

print()
print("=== FEA970: blitnode callback ===")
start = 0xFEA970 - base
for i in range(0, 66, 2):
    w = struct.unpack('>H', rom[start+i:start+i+2])[0]
    print(f"  ${0xFEA970+i:06X}: {w:04X}", end="")
    if (i // 2) % 8 == 7:
        print()
print()

print()
print("Manual decode of FEA970:")
print("  $FEA970: MOVE.L A5,-(SP)       ; save A5")
print("  $FEA972: MOVEA.L A1,A5         ; A5 = blitnode (A1)")
print("  $FEA974: BSR $FEAF48           ; OwnBlitter/WaitBlit")
print("  $FEA978: MOVEA.L A5,A1         ; A1 = blitnode")

# 4CE9 2003 0008
mask = word(0xFEA97C)
disp = word(0xFEA97E)
regs = []
for b in range(16):
    if mask & (1 << b):
        if b < 8:
            regs.append(f"D{b}")
        else:
            regs.append(f"A{b-8}")
print(f"  $FEA97A: MOVEM.L (${disp:04X},A1),{'/'.join(regs)}")
print(f"           ; Load D0,D1,A5 from blitnode+$08")
print(f"           ; D0 = byte count (sector size)")
print(f"           ; D1 = destination pointer")
print(f"           ; A5 = MFM source pointer (odd data)")

# After MOVEM:
# D0 = blitnode+$08 (byte count = $200 = 512)
# D1 = blitnode+$0C (destination buffer pointer)
# A5 = blitnode+$10 (MFM odd data pointer)

print("  $FEA980: ADDA.L D0,A5          ; A5 += D0 (point to end of odd block)")
print("  $FEA982: SUBQ.L #1,A5          ; A5 -= 1 (last byte of odd block)")
print("  $FEA984: MOVE.L A5,($0050,A0)  ; custom+$50 = BLTAPT = end of odd data")
print("  $FEA988: ADDA.L D0,A5          ; A5 += D0 (skip to end of even block)")
print("  $FEA98A: MOVE.L A5,($004C,A0)  ; custom+$4C = BLTBPT = end of even data")
print("  $FEA98E: ADD.L D0,D1           ; D1 += D0 (destination end)")
print("  $FEA990: SUBQ.L #1,D1          ; D1 -= 1 (last byte of dest)")
print("  $FEA992: MOVE.L D1,($0054,A0)  ; custom+$54 = BLTDPT = end of dest")
print("  $FEA996: MOVE.W #$1DD8,($0040,A0) ; BLTCON0=$1DD8")
print("  $FEA99C: MOVE.W #$0002,($0042,A0) ; BLTCON1=$0002 (descending)")
print("  $FEA9A2: MOVE.W ($0014,A1),($0058,A0) ; BLTSIZE from blitnode+$14")
print("  $FEA9A8: MOVE.L #$FEA9B4,($0004,A1) ; set cleanup callback")
print("  $FEA9B0: MOVEA.L (SP)+,A5      ; restore A5")
print("  $FEA9B2: RTS")

print()
print("=== FEA932: decode_copy setup ===")
start = 0xFEA932 - base
for i in range(0, 64, 2):
    w = struct.unpack('>H', rom[start+i:start+i+2])[0]
    print(f"  ${0xFEA932+i:06X}: {w:04X}", end="")
    if (i // 2) % 8 == 7:
        print()
print()

print()
print("=== Custom register map for blitnode ===")
print("  $DFF040 = BLTCON0")
print("  $DFF042 = BLTCON1")
print("  $DFF044 = BLTAFWM")
print("  $DFF046 = BLTALWM")
print("  $DFF048 = BLTCPTH/L")
print("  $DFF04C = BLTBPTH/L")
print("  $DFF050 = BLTAPTH/L")
print("  $DFF054 = BLTDPTH/L")
print("  $DFF058 = BLTSIZE")

print()
print("=== Verifying blitter register writes via A0=$DFF000 ===")
print("  ($0050,A0) = $DFF050 = BLTAPTH (correct)")
print("  ($004C,A0) = $DFF04C = BLTBPTH (correct)")
print("  ($0054,A0) = $DFF054 = BLTDPTH (correct)")
print("  ($0040,A0) = $DFF040 = BLTCON0 (correct)")
print("  ($0042,A0) = $DFF042 = BLTCON1 (correct)")
print("  ($0058,A0) = $DFF058 = BLTSIZE (correct)")

print()
print("=== BUT WAIT: The blitnode writes FULL 32-bit to $DFF050, $DFF04C, $DFF054 ===")
print("    These are MOVE.L to offset $50, $4C, $54 from $DFF000")
print("    So the write hits BOTH high and low word at once")
print("    $DFF050+$DFF052 = BLTAPTH+BLTAPTL")
print("    $DFF04C+$DFF04E = BLTBPTH+BLTBPTL")
print("    $DFF054+$DFF056 = BLTDPTH+BLTDPTL")

print()
print("=== Now check: does custom.go handle 32-bit (4-byte) writes to BLT registers? ===")
