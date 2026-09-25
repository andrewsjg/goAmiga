#!/usr/bin/env python3
"""Search ROM for ALL ADKCON/custom register writes."""
import sys

with open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb') as f:
    rom = f.read()

# Search for any MOVE to custom reg offset $9E
# Pattern: 337C xxxx 009E where 337C = MOVE.W #imm,(An) with various An encodings
# Actually 337C is just one encoding. Let me search for any write to (d16,An) where d16=$009E
# MOVE.W #imm,(d16,An) = 0011_ddd1_0110_1rrr where ddd=An, rrr=An (wrong)

# Actually let me search for the DMA setup at $FEA1A4 and look for $9E near there
off_start = 0xFEA1A4 - 0xFC0000  # $2A1A4
off_end = off_start + 0x200

print("=== ROM bytes $FEA1A4-$FEA3A4 (DMA setup) scanning for $9E ===")
for off in range(off_start, min(off_end, len(rom) - 2)):
    if rom[off] == 0x00 and rom[off+1] == 0x9E:
        # Found $009E, check context
        addr = 0xFC0000 + off
        ctx = rom[max(0, off-4):off+6]
        print(f'  ${addr:06X}: context bytes: {ctx.hex()}')

# Also search for ADKCON writes in trackdisk init area
print("\n=== Searching entire ROM for bytes 009E in MOVE contexts ===")
for off in range(len(rom) - 6):
    w = (rom[off] << 8) | rom[off+1]
    # MOVE.W #imm,(d16,An): opcode = 0011_rrr1_0111_1aaa
    # Where rrr = dst reg, aaa = An base reg, d16 follows after immediate
    # Actually: MOVE.W #imm,(d16,An) uses EA mode 101 (d16,An) for destination
    # Opcode: 0011 rrr 101 111 100 = 3xxx where xxx depends on An
    # Source: #imm = mode 111, reg 100
    # So: 0011 nnn 101 111 100 = 337C for A0... no.
    # MOVE.W opcode: 00ss_xxxX_XXYY_Yyyy
    # s=01 (word), dst mode (XXX), dst reg (xxx), src mode (YYY), src reg (yyy)
    # dst mode 101 = d16(An): XXX=101, xxx=nnn
    # src mode 111, reg 100 = #imm
    # So: 0011 nnn 101 111 100
    # A1: nnn=001 → 0011 001 101 111 100 = 0011_0011_0111_1100 = $337C
    # That matches A1! All 337C are MOVE.W #imm,(d16,A1)
    if w == 0x337C:
        # d16 is at off+4
        d16 = (rom[off+4] << 8) | rom[off+5]
        if d16 == 0x009E:
            val = (rom[off+2] << 8) | rom[off+3]
            addr = 0xFC0000 + off
            print(f'  ${addr:06X}: MOVE.W #${val:04X},($009E,A1) = ADKCON')

# Check for other An bases: A0=0, A2=2, etc.
for an in range(8):
    # opcode = 0011_nnn_101_111_100 where nnn = an
    opcode = 0x3000 | (an << 9) | 0x017C
    for off in range(len(rom) - 6):
        w = (rom[off] << 8) | rom[off+1]
        if w == opcode:
            d16 = (rom[off+4] << 8) | rom[off+5]
            if d16 == 0x009E:
                val = (rom[off+2] << 8) | rom[off+3]
                addr = 0xFC0000 + off
                print(f'  ${addr:06X}: MOVE.W #${val:04X},($009E,A{an})')

print("\nDone.")
