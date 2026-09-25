#!/usr/bin/env python3
"""Part 3: Decode the full calling function from LINK at $FC9C08 through RTS at $FCA0EC."""

from capstone import *

ROM_PATH = "Kick13.rom"
ROM_BASE = 0xFC0000

CUSTOM_REGS = {
    0x002: "DMACONR", 0x040: "BLTCON0", 0x042: "BLTCON1",
    0x044: "BLTAFWM", 0x046: "BLTALWM", 0x048: "BLTCPTH",
    0x04C: "BLTBPTH", 0x050: "BLTAPTH", 0x054: "BLTDPTH",
    0x058: "BLTSIZE", 0x060: "BLTCMOD", 0x062: "BLTBMOD",
    0x064: "BLTAMOD", 0x066: "BLTDMOD", 0x068: "BLTCDAT",
    0x06A: "BLTBDAT", 0x06C: "BLTADAT", 0x072: "BLTAFWM/BLTALWM(ECS)",
    0x096: "DMACON", 0x09A: "INTENA", 0x09C: "INTREQ",
}

KNOWN_FUNCS = {
    0xFC5A58: "WaitBlitDone (poll DMACONR bit 6)",
    0xFC5BCA: "BltClear",
    0xFC5CAC: "BltBitMap_inner",
    0xFC64C4: "Disable/OwnBlitter?",
    0xFC64DA: "DisownBlitter/Permit?",
    0xFCF324: "Disable()",
    0xFD3BC4: "Permit()?",
    0xFD3BD4: "Enable()?",
    0xFD14E8: "FreeMem?",
    0xFD3B4E: "UMult32?",
    0xFD3B44: "SMult32?",
    0xFC703C: "func_703C",
    0xFC748A: "func_748A (stored at -4(a5))",
    0xFCA0EE: "BltBitMap_doOnePlane_ascending",
    0xFCA21A: "BltBitMap_doOnePlane_descending",
}

def load_rom(path):
    with open(path, "rb") as f:
        return f.read()

def disasm_annotated(rom, start_offset, end_offset, title):
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
        
        line = f"  ${rom_off:05X}  {insn.address:06X}:  {raw_hex:<24s}  {insn.mnemonic:<10s} {insn.op_str}"
        
        # Annotate calls
        annots = []
        mnem = insn.mnemonic.lower()
        if mnem in ('jsr', 'bsr'):
            op = insn.op_str.strip()
            try:
                if op.startswith('$'):
                    target = int(op[1:], 16)
                elif op.startswith('0x'):
                    target = int(op, 16)
                else:
                    target = int(op.replace('.l','').replace('.w','').strip('$'), 16)
                if target in KNOWN_FUNCS:
                    annots.append(f"→ {KNOWN_FUNCS[target]}")
                else:
                    annots.append(f"→ sub_{target:06X}")
            except:
                pass
        
        # Annotate DFF register writes  
        op_lower = insn.op_str.lower()
        for reg_off, reg_name in CUSTOM_REGS.items():
            full = f"0x{0xDFF000+reg_off:x}"
            if full in op_lower:
                annots.append(reg_name)
        
        if annots:
            line = f"{line:<60s} ; {', '.join(annots)}"
        
        print(line)

def main():
    rom = load_rom(ROM_PATH)
    
    print("="*80)
    print("FULL CALLING FUNCTION: $FC9C08 through $FCA0EC")
    print("This is the function that calls BltBitMap at $FCA0BA")
    print("="*80)
    
    disasm_annotated(rom, 0x9C08, 0xA0EC, "Full caller function $FC9C08-$FCA0EC")
    
    # Also need to understand: is there a BltClear call for the $CA fill?
    # Search for calls to BltClear ($FC5BCA)
    print("\n\n" + "="*80)
    print("Searching for all JSR $FC5BCA (BltClear) calls in ROM:")
    print("="*80)
    jsr_pattern = bytes([0x4E, 0xB9, 0x00, 0xFC, 0x5B, 0xCA])
    for i in range(len(rom) - len(jsr_pattern)):
        if rom[i:i+len(jsr_pattern)] == jsr_pattern:
            print(f"  ROM ${i:04X} = PC ${ROM_BASE+i:06X}")
    
    print("\n\nSearching for all JSR $FC5CAC (BltBitMap_inner) calls in ROM:")
    jsr_pattern2 = bytes([0x4E, 0xB9, 0x00, 0xFC, 0x5C, 0xAC])
    for i in range(len(rom) - len(jsr_pattern2)):
        if rom[i:i+len(jsr_pattern2)] == jsr_pattern2:
            print(f"  ROM ${i:04X} = PC ${ROM_BASE+i:06X}")

if __name__ == "__main__":
    main()
