#!/usr/bin/env python3
"""Part 2: Decode additional ROM regions - WaitBlit, $FC5A58, and find DMACONR poll."""

import struct
from capstone import *

ROM_PATH = "Kick13.rom"
ROM_BASE = 0xFC0000

def load_rom(path):
    with open(path, "rb") as f:
        return f.read()

def disasm(rom, start_offset, end_offset, title):
    md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)
    md.detail = True
    code = rom[start_offset:end_offset + 32]
    base_pc = ROM_BASE + start_offset
    print(f"\n{'='*80}")
    print(f"  {title}")
    print(f"  ROM offset ${start_offset:04X}-${end_offset:04X}  |  PC ${base_pc:06X}-${ROM_BASE+end_offset:06X}")
    print(f"{'='*80}")
    for insn in md.disasm(code, base_pc):
        if insn.address > ROM_BASE + end_offset:
            break
        rom_off = insn.address - ROM_BASE
        raw = rom[rom_off:rom_off + insn.size]
        raw_hex = " ".join(f"{b:02X}" for b in raw)
        print(f"  ${rom_off:04X}  {insn.address:06X}:  {raw_hex:<24s}  {insn.mnemonic:<10s} {insn.op_str}")

def main():
    rom = load_rom(ROM_PATH)
    
    # ===== Decode $FC5A58 - the function called from blitter wait checks =====
    print("\n" + "#"*80)
    print("# $FC5A58 - BlitterWait function called from BltClear/BltBitMap")
    print("#"*80)
    disasm(rom, 0x5A58, 0x5AC0, "$FC5A58 function")
    
    # ===== Decode $FC64C4 - called at start of BltClear =====
    print("\n" + "#"*80)
    print("# $FC64C4 - Called at start of blitter operations")
    print("#"*80)
    disasm(rom, 0x64C4, 0x6520, "$FC64C4 function")
    
    # ===== Decode $FC64DA - Called at end of BltClear/BltBitMap =====
    print("\n" + "#"*80)
    print("# $FC64DA - Called at end of blitter operations")
    print("#"*80)
    disasm(rom, 0x64DA, 0x6530, "$FC64DA function")
    
    # ===== Search for BTST #14 on DMACONR ($DFF002) pattern =====
    # The WaitBlit polls bit 14 (BBUSY) of DMACONR
    # BTST #6, $2(a0) where a0=$DFF000 tests bit 6 of byte at $DFF002
    # But bit 14 of DMACONR word = bit 6 of high byte ($DFF002)
    # Pattern: 08 28 00 06 00 02 (BTST #6, $2(a0))
    # Or: 08 39 00 0E 00 DF F0 02 (BTST #14, $DFF002.l)
    
    print("\n" + "#"*80)
    print("# Search for BTST #6, $2(a0) patterns (WaitBlit polling DMACONR)")
    print("#"*80)
    
    pattern = bytes([0x08, 0x28, 0x00, 0x06, 0x00, 0x02])
    found = []
    for i in range(len(rom) - len(pattern)):
        if rom[i:i+len(pattern)] == pattern:
            found.append(i)
    
    print(f"\n  Found {len(found)} instances of BTST #6, $2(a0):")
    for off in found:
        pc = ROM_BASE + off
        print(f"    ROM ${off:04X} = PC ${pc:06X}")
    
    # ===== Now decode the REAL WaitBlit =====
    # In BltClear, the pattern appears twice (double-check), then BSR to $FC5A58 if still busy
    # So $FC5A58 IS the actual "wait for blitter done" helper
    # But the user says WaitBlit is at $FC637C
    # Let me check what's around $FC637C more carefully
    
    print("\n" + "#"*80)
    print("# Re-decode $FC637C region (wider context)")
    print("#"*80)
    disasm(rom, 0x6370, 0x63C0, "$FC6370-$FC63C0 wider context")
    
    # ===== Look for the function that CALLS $FC637C =====
    # Search for JSR $FC637C pattern: 4E B9 00 FC 63 7C
    jsr_pattern = bytes([0x4E, 0xB9, 0x00, 0xFC, 0x63, 0x7C])
    # Also BSR to $FC637C (relative)
    
    print("\n  Searching for JSR $FC637C:")
    found_jsr = []
    for i in range(len(rom) - len(jsr_pattern)):
        if rom[i:i+len(jsr_pattern)] == jsr_pattern:
            found_jsr.append(i)
            print(f"    ROM ${i:04X} = PC ${ROM_BASE+i:06X}")
    
    if not found_jsr:
        print("    Not found - $FC637C may not be WaitBlit")
    
    # ===== Search for JSR/BSR to $FC5A58 =====
    jsr_5a58 = bytes([0x4E, 0xB9, 0x00, 0xFC, 0x5A, 0x58])
    print("\n  Searching for JSR $FC5A58:")
    for i in range(len(rom) - len(jsr_5a58)):
        if rom[i:i+len(jsr_5a58)] == jsr_5a58:
            print(f"    ROM ${i:04X} = PC ${ROM_BASE+i:06X}")
    
    # ===== Decode function entry before $FC9FE0 (caller function prologue) =====
    print("\n" + "#"*80)
    print("# Looking for function prologue before $FC9FE0")
    print("#"*80)
    # LINK A5 is typically used - search backward for 4E55 (LINK A5,#xx)
    for off in range(0x9F00, 0x9800, -2):
        w = struct.unpack_from('>H', rom, off)[0]
        if w == 0x4E55:  # LINK A5
            print(f"  Found LINK A5 at ROM ${off:04X} = PC ${ROM_BASE+off:06X}")
            disasm(rom, off, off+0x30, f"Function prologue at ${ROM_BASE+off:06X}")
            break
    
    # ===== Decode $FCA0EE - the sub called from BltBitMap inner loop =====
    print("\n" + "#"*80)
    print("# $FCA0EE - Sub called from BltBitMap inner loop (JSR at $FC5D72)")
    print("#"*80)
    disasm(rom, 0xA0EE, 0xA218, "$FCA0EE function")
    
    # ===== Decode $FCA21A - Other sub from BltBitMap inner loop =====
    print("\n" + "#"*80)
    print("# $FCA21A - Sub called from BltBitMap descending loop (JSR at $FC5DBC)")
    print("#"*80)
    disasm(rom, 0xA21A, 0xA350, "$FCA21A function")
    
    # ===== Also decode $FD14E8 - called after BltBitMap =====
    print("\n" + "#"*80)
    print("# $FD14E8 - Called after BltBitMap (FreeMem?)")
    print("#"*80)
    disasm(rom, 0x114E8, 0x11520, "$FD14E8 function")

if __name__ == "__main__":
    main()
