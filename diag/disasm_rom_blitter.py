#!/usr/bin/env python3
"""Decode specific ROM regions for blitter operation analysis."""

import struct, sys
from capstone import *

ROM_PATH = "Kick13.rom"
ROM_BASE = 0xFC0000  # ROM mapped at $FC0000-$FFFFFF

# Known addresses for annotation
KNOWN = {
    0xFC637C: "WaitBlit",
    0xFC5BCA: "BltClear",
    0xFC5CAC: "BltBitMap_inner",
    0xDFF000: "CUSTOM_BASE",
}

# Custom register names (offset from $DFF000)
CUSTOM_REGS = {
    0x002: "DMACONR", 0x010: "ADKCONR", 0x01C: "INTREQR",
    0x01E: "INTENA", 0x040: "BLTCON0", 0x042: "BLTCON1",
    0x044: "BLTAFWM", 0x046: "BLTALWM", 0x048: "BLTCPTH",
    0x04A: "BLTCPTL", 0x04C: "BLTBPTH", 0x04E: "BLTBPTL",
    0x050: "BLTAPTH", 0x052: "BLTAPTL", 0x054: "BLTDPTH",
    0x056: "BLTDPTL", 0x058: "BLTSIZE", 0x05A: "BLTCON0L",
    0x05C: "BLTSIZV", 0x05E: "BLTSIZH", 0x060: "BLTCMOD",
    0x062: "BLTBMOD", 0x064: "BLTAMOD", 0x066: "BLTDMOD",
    0x068: "BLTCDAT", 0x06A: "BLTBDAT", 0x06C: "BLTADAT",
    0x096: "DMACON", 0x09A: "INTENA", 0x09C: "INTREQ",
    0x09E: "ADKCON", 0x100: "BPLCON0", 0x102: "BPLCON1",
    0x104: "BPLCON2", 0x108: "BPL1MOD", 0x10A: "BPL2MOD",
    0x110: "BPL1PTH", 0x112: "BPL1PTL", 0x114: "BPL2PTH",
    0x116: "BPL2PTL", 0x118: "BPL3PTH", 0x11A: "BPL3PTL",
    0x11C: "BPL4PTH", 0x11E: "BPL4PTL", 0x120: "BPL5PTH",
    0x122: "BPL5PTL", 0x180: "COLOR00",
}


def load_rom(path):
    with open(path, "rb") as f:
        return f.read()


def annotate(text, addr, rom):
    """Add annotations for known addresses and custom registers."""
    annotations = []
    
    # Check for custom register references
    for reg_off, reg_name in CUSTOM_REGS.items():
        full_addr = 0xDFF000 + reg_off
        hex_addr = f"0x{full_addr:x}"
        hex_addr2 = f"$dff{reg_off:03x}"
        if hex_addr in text.lower() or f"0x{reg_off:x}" in text.lower():
            annotations.append(reg_name)
    
    # Check for known function calls
    for known_addr, name in KNOWN.items():
        if f"0x{known_addr:x}" in text.lower():
            annotations.append(name)
    
    if annotations:
        return f"{text:<50s} ; {', '.join(annotations)}"
    return text


def disasm_region(rom, start_offset, end_offset, title):
    """Disassemble a ROM region."""
    md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)
    md.detail = True
    
    code = rom[start_offset:end_offset + 16]  # extra bytes for last instruction
    base_pc = ROM_BASE + start_offset
    
    print(f"\n{'='*80}")
    print(f"  {title}")
    print(f"  ROM offset ${start_offset:04X}-${end_offset:04X}  |  PC ${base_pc:06X}-${ROM_BASE+end_offset:06X}")
    print(f"{'='*80}")
    
    for insn in md.disasm(code, base_pc):
        if insn.address > ROM_BASE + end_offset:
            break
        
        rom_off = insn.address - ROM_BASE
        # Raw bytes
        raw = rom[rom_off:rom_off + insn.size]
        raw_hex = " ".join(f"{b:02X}" for b in raw)
        
        line = f"  ${rom_off:04X}  {insn.address:06X}:  {raw_hex:<24s}  {insn.mnemonic:<10s} {insn.op_str}"
        line = annotate(line, insn.address, rom)
        print(line)
    
    print()


def disasm_region_verbose(rom, start_offset, end_offset, title):
    """Disassemble with extra analysis for branch targets and call analysis."""
    md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)
    md.detail = True
    
    code = rom[start_offset:end_offset + 32]
    base_pc = ROM_BASE + start_offset
    
    print(f"\n{'='*80}")
    print(f"  {title}")
    print(f"  ROM offset ${start_offset:04X}-${end_offset:04X}  |  PC ${base_pc:06X}-${ROM_BASE+end_offset:06X}")
    print(f"{'='*80}")
    
    instructions = list(md.disasm(code, base_pc))
    
    # Collect branch targets for labeling
    branch_targets = set()
    call_targets = {}
    for insn in instructions:
        if insn.address > ROM_BASE + end_offset:
            break
        mnem = insn.mnemonic.lower()
        if mnem.startswith('b') or mnem.startswith('db') or mnem == 'jsr' or mnem == 'bsr':
            # Try to extract target from operand
            op = insn.op_str.strip()
            if op.startswith('$') or op.startswith('0x'):
                try:
                    target = int(op.replace('$', '0x'), 16)
                    branch_targets.add(target)
                    if mnem in ('jsr', 'bsr'):
                        name = KNOWN.get(target, f"sub_{target:06X}")
                        call_targets[insn.address] = (target, name)
                except:
                    pass
    
    for insn in instructions:
        if insn.address > ROM_BASE + end_offset:
            break
        
        rom_off = insn.address - ROM_BASE
        raw = rom[rom_off:rom_off + insn.size]
        raw_hex = " ".join(f"{b:02X}" for b in raw)
        
        # Label for branch targets
        label = ""
        if insn.address in branch_targets:
            label = f"\n  ; --- target ---"
        
        line = f"  ${rom_off:04X}  {insn.address:06X}:  {raw_hex:<24s}  {insn.mnemonic:<10s} {insn.op_str}"
        
        # Annotations
        annots = []
        mnem = insn.mnemonic.lower()
        
        # Custom register detection
        op_lower = insn.op_str.lower()
        for reg_off, reg_name in CUSTOM_REGS.items():
            full_hex = f"0x{0xDFF000+reg_off:x}"
            off_hex = f"0x{reg_off:x}"
            if full_hex in op_lower:
                annots.append(reg_name)
            # Check for (an) with displacement forms referencing $DFF000+offset
        
        # Known function calls
        if insn.address in call_targets:
            target, name = call_targets[insn.address]
            annots.append(f"→ {name}")
        
        # Branch mnemonics annotation
        if mnem.startswith('db'):
            annots.append("loop-decrement")
        
        # BLTSIZE write detection
        if 'bltsize' in ' '.join(annots).lower() or '0xdff058' in op_lower:
            annots.append("*** BLTSIZE WRITE - STARTS BLIT ***")
        
        if annots:
            line = f"{line:<55s} ; {', '.join(annots)}"
        
        if label:
            print(label)
        print(line)
    
    print()


def find_waitblit_calls(rom, start_offset, end_offset):
    """Search for JSR/BSR to WaitBlit ($FC637C) in a region."""
    md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)
    md.detail = True
    
    code = rom[start_offset:end_offset + 16]
    base_pc = ROM_BASE + start_offset
    
    waitblit_addr = 0xFC637C
    calls = []
    
    for insn in md.disasm(code, base_pc):
        if insn.address > ROM_BASE + end_offset:
            break
        mnem = insn.mnemonic.lower()
        if mnem in ('jsr', 'bsr'):
            op = insn.op_str.strip()
            try:
                target = int(op.replace('$', '').replace('0x', ''), 16)
                if target == waitblit_addr:
                    calls.append(insn.address)
            except:
                pass
        # Also check for inline BTST #14 on DMACONR (polling loop)
        if mnem == 'btst' and '14' in insn.op_str and 'dff002' in insn.op_str.lower():
            calls.append(insn.address)
    
    return calls


def main():
    rom = load_rom(ROM_PATH)
    print(f"ROM loaded: {len(rom)} bytes ({len(rom)//1024}KB)")
    print(f"ROM mapped at ${ROM_BASE:06X}-${ROM_BASE+len(rom)-1:06X}")
    
    # ===== TASK 4: WaitBlit function (small, do first) =====
    print("\n" + "#"*80)
    print("# TASK 4: WaitBlit function at $FC637C")
    print("#"*80)
    disasm_region_verbose(rom, 0x637C, 0x6396, "WaitBlit - $FC637C")
    
    # ===== TASK 1: BltClear/BltFill at $5BCA-$5C6E =====
    print("\n" + "#"*80)
    print("# TASK 1: BltClear/BltFill at $FC5BCA-$FC5C6E")
    print("#"*80)
    disasm_region_verbose(rom, 0x5BCA, 0x5C6E, "BltClear/BltFill - $FC5BCA-$FC5C6E")
    
    # Check for WaitBlit calls in this region
    wbcalls = find_waitblit_calls(rom, 0x5BCA, 0x5C6E)
    if wbcalls:
        print(f"  *** WaitBlit calls found at: {', '.join(f'${a:06X}' for a in wbcalls)}")
    else:
        print(f"  *** NO WaitBlit calls found in BltClear/BltFill per-plane loop")
    
    # ===== TASK 2: BltBitMap per-plane loop at $5CAC-$5E2A =====
    print("\n" + "#"*80)
    print("# TASK 2: BltBitMap per-plane inner loop $FC5CAC-$FC5E2A")
    print("#"*80)
    disasm_region_verbose(rom, 0x5CAC, 0x5E2A, "BltBitMap inner loop - $FC5CAC-$FC5E2A")
    
    wbcalls = find_waitblit_calls(rom, 0x5CAC, 0x5E2A)
    if wbcalls:
        print(f"  *** WaitBlit calls found at: {', '.join(f'${a:06X}' for a in wbcalls)}")
    else:
        print(f"  *** NO WaitBlit calls found in BltBitMap per-plane loop")
    
    # ===== TASK 3: Calling function around $FCA0BA =====
    print("\n" + "#"*80)
    print("# TASK 3: Calling function $FC9FE0-$FCA100")
    print("#"*80)
    disasm_region_verbose(rom, 0x9FE0, 0xA100, "Caller around $FCA0BA - $FC9FE0-$FCA100")
    
    # Extended context - let's also look further back and forward
    wbcalls = find_waitblit_calls(rom, 0x9FE0, 0xA100)
    if wbcalls:
        print(f"  *** WaitBlit calls found at: {', '.join(f'${a:06X}' for a in wbcalls)}")
    
    # Also look for JSR to BltBitMap and BltClear in this region
    md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)
    md.detail = True
    code = rom[0x9FE0:0xA100 + 16]
    base_pc = ROM_BASE + 0x9FE0
    
    print("\n  --- Call analysis in $FC9FE0-$FCA100 ---")
    for insn in md.disasm(code, base_pc):
        if insn.address > ROM_BASE + 0xA100:
            break
        mnem = insn.mnemonic.lower()
        if mnem in ('jsr', 'bsr', 'jmp'):
            rom_off = insn.address - ROM_BASE
            target_str = insn.op_str.strip()
            annotation = ""
            try:
                target = int(target_str.replace('$', '').replace('0x', ''), 16)
                if target in KNOWN:
                    annotation = f" → {KNOWN[target]}"
                elif 0xFC5BCA <= target <= 0xFC5C6E:
                    annotation = " → BltClear range"
                elif 0xFC5CAC <= target <= 0xFC5E2A:
                    annotation = " → BltBitMap range"
            except:
                pass
            print(f"    ${rom_off:04X} {insn.address:06X}: {mnem} {target_str}{annotation}")
    
    # ===== Extra: Look for the template init ($CA fill) =====
    print("\n" + "#"*80)
    print("# EXTRA: Wider context - look for $104B8 references and QBlit")
    print("#"*80)
    
    # Search for references to $104B8 in a wider region
    # $104B8 as bytes in big-endian: 0x00, 0x01, 0x04, 0xB8
    target_bytes = b'\x00\x01\x04\xB8'
    found = []
    for i in range(len(rom) - 4):
        if rom[i:i+4] == target_bytes:
            found.append(i)
    
    if found:
        print(f"\n  References to $000104B8 found at ROM offsets: {', '.join(f'${o:04X}' for o in found)}")
        for off in found:
            # Decode surrounding context
            start = max(0, off - 16)
            end = min(len(rom), off + 20)
            disasm_region_verbose(rom, start, end, f"Context around $104B8 ref at ROM offset ${off:04X}")
    
    # Also search for the 2-byte form (short address) $04B8
    target_bytes2 = b'\x04\xB8'
    found2 = []
    # Only search in regions we care about
    for region_start, region_end in [(0x9F00, 0xA200), (0xA000, 0xB000)]:
        for i in range(region_start, min(region_end, len(rom) - 2)):
            if rom[i:i+2] == target_bytes2:
                found2.append(i)
    
    if found2:
        print(f"\n  Possible short references to $04B8 at ROM offsets: {', '.join(f'${o:04X}' for o in found2)}")
    
    # ===== Decode wider calling context =====
    print("\n" + "#"*80)
    print("# EXTRA: Extended calling context $FC9F00-$FCA200")
    print("#"*80)
    disasm_region_verbose(rom, 0x9F00, 0xA200, "Extended caller context - $FC9F00-$FCA200")


if __name__ == "__main__":
    main()
