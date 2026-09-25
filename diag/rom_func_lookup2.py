#!/usr/bin/env python3
"""Focused follow-up: clean disassembly of key areas."""

import struct, sys

ROM_PATH = "/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom"
ROM_BASE = 0xFC0000

def read_rom():
    with open(ROM_PATH, "rb") as f:
        return f.read()

# Import the disassembler from the main script
sys.path.insert(0, "/Users/jga/Development/goAmiga/external-cpu/goAmiga/diag")
from rom_func_lookup import disasm_one, get_word, get_long, get_sword

def disasm_at(rom, pc_start, count):
    off = pc_start - ROM_BASE
    pc = pc_start
    for _ in range(count):
        if off >= len(rom) - 1: break
        text, length = disasm_one(rom, off, pc)
        hexb = " ".join(f"{rom[off+i]:02X}" for i in range(min(length, 10)))
        print(f"  ${pc:08X}:  {hexb:<30s} {text}")
        off += length
        pc += length

def main():
    rom = read_rom()
    
    # ===== MakeVPort Intuition wrapper (correct alignment) =====
    # The $0000 words between functions are padding. Real entry points:
    print("=" * 70)
    print("INTUITION GRAPHICS WRAPPER TABLE (corrected alignment)")
    print("=" * 70)
    
    # The wrappers follow a clear pattern. Let's decode each one with proper alignment
    wrappers = [
        (0xFE044C, "CloseWorkBench?/ScrollVPort? (LVO -198)"),
        (0xFE0460, "MrgCop wrapper (LVO -210)"),
        (0xFE0474, "MakeVPort wrapper (LVO -216)"),
        (0xFE0488, "LoadView wrapper (LVO -222)"),
        (0xFE049C, "WaitBlit? wrapper (LVO -228)"),
    ]
    
    for addr, label in wrappers:
        print(f"\n--- {label} at ${addr:08X} ---")
        # Check if there's a $0000 padding word before
        pad_off = addr - 2 - ROM_BASE
        pad_w = get_word(rom, pad_off)
        if pad_w == 0x0000:
            print(f"  ${addr-2:08X}:  00 00                          DC.W    $0000  (padding)")
        disasm_at(rom, addr, 8)
    
    # ===== Full RethinkDisplay function =====
    print()
    print("=" * 70)
    print("RETHINKDISPLAY FUNCTION (searching for start)")
    print("=" * 70)
    
    # Search backwards from $FDC782 for an RTS (end of prior function)
    target = 0xFDC782
    search_off = target - ROM_BASE - 200
    found_rts = None
    off = search_off
    while off < target - ROM_BASE:
        w = get_word(rom, off)
        if w == 0x4E75:  # RTS
            found_rts = ROM_BASE + off
        off += 2
    
    if found_rts:
        func_start = found_rts + 2
        print(f"Previous function ends with RTS at ${found_rts:08X}")
        print(f"RethinkDisplay likely starts at ${func_start:08X}")
        print()
        disasm_at(rom, func_start, 80)
    
    # ===== LVO identification table =====
    print()
    print("=" * 70)
    print("LVO OFFSET IDENTIFICATION")
    print("=" * 70)
    print()
    print("Graphics.library LVO offsets used in the wrapper table:")
    print("  -78  ($FFB2) = ???")
    print("  -90  ($FFA6) = ???")
    print("  -198 ($FF3A) = ???")
    print("  -204 ($FF34) = ScrollVPort")
    print("  -210 ($FF2E) = MrgCop   (entry 34)")
    print("  -216 ($FF28) = MakeVPort (entry 35)")
    print("  -222 ($FF22) = LoadView  (entry 36)")
    print("  -228 ($FF1C) = WaitBlit  (entry 37)")
    print()
    
    # Cross-reference: LVO -204 = ScrollVPort? Let's verify
    # LVO formula: entry = (abs(LVO) - 6) / 6 + 1, but actually
    # Open=-6(entry 0), Close=-12(entry 1), Expunge=-18(entry 2), Reserved=-24(entry 3)
    # Then functions: -30 = entry 4, -36 = entry 5, ...
    # entry = (abs(LVO) - 6) / 6
    # -210: entry = (210-6)/6 = 34 ✓ (MrgCop)
    # -216: entry = (216-6)/6 = 35 ✓ (MakeVPort)
    # -222: entry = (222-6)/6 = 36 ✓ (LoadView)
    
    # graphics.library LVO table from AmigaOS includes:
    print("Known graphics.library LVOs:")
    print("  -30  = BltBitMap")
    print("  -36  = BltTemplate")
    print("  -78  = OwnBlitter (-78)")
    print("  -84  = DisownBlitter (-84)")
    print("  -90  = SetRGB4 (-90)")
    print("  -198 = VBeamPos (-198)")
    print("  -204 = ScrollVPort (-204)")
    print("  -210 = MrgCop (-210)")
    print("  -216 = MakeVPort (-216)")
    print("  -222 = LoadView (-222)")
    print("  -228 = WaitBlit (-228)")
    print("  -234 = SetRast (-234)")
    
    # ===== Check what's at FE04F4 (called right after LoadView in RethinkDisplay) =====
    print()
    print("=" * 70)
    print("ADDITIONAL WRAPPER AT $FE04F4 (called after LoadView)")
    print("=" * 70)
    disasm_at(rom, 0xFE04F4, 10)
    
    # ===== Check $FE06E4 (memory free? called for old copper lists) =====
    print()
    print("=" * 70)
    print("FUNCTION AT $FE06E4 (old copper list cleanup)")
    print("=" * 70)
    disasm_at(rom, 0xFE06E4, 12)


if __name__ == "__main__":
    main()
