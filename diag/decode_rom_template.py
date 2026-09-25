#!/usr/bin/env python3
"""Decode Kickstart 1.3 ROM regions related to template buffer initialization.

TASK 1: ROM offset $C1E4 (PC=$FCC1E4) - writes $FF to template at $104B8
TASK 2: ROM offset $1786 (PC=$FC1786) - writes $00 to template (clearing)
TASK 3: BltBitMap public function $FC9C08-$FCA0EC - template allocation and fill path
"""

import capstone
import struct

ROM_PATH = 'Kick13.rom'
ROM_BASE = 0xFC0000
ROM_SIZE = 0x40000  # 256KB

with open(ROM_PATH, 'rb') as f:
    rom = f.read()

assert len(rom) == ROM_SIZE, f"ROM size mismatch: {len(rom)} != {ROM_SIZE}"

md = capstone.Cs(capstone.CS_ARCH_M68K, capstone.CS_MODE_M68K_000)
md.detail = True

def read_byte(off):
    if 0 <= off < ROM_SIZE:
        return rom[off]
    return None

def read_word(off):
    if 0 <= off < ROM_SIZE - 1:
        return struct.unpack_from('>H', rom, off)[0]
    return None

def read_long(off):
    if 0 <= off < ROM_SIZE - 3:
        return struct.unpack_from('>I', rom, off)[0]
    return None

def pc_to_off(pc):
    return pc - ROM_BASE

def off_to_pc(off):
    return off + ROM_BASE

# Known Amiga library vector offsets for annotation
EXEC_LVO = {
    -0x006: "Supervisor",
    -0x048: "InitCode",
    -0x04E: "InitStruct",
    -0x054: "MakeLibrary",
    -0x05A: "MakeFunctions",
    -0x060: "FindResident",
    -0x066: "InitResident",
    -0x06C: "Alert",
    -0x072: "Debug",
    -0x078: "Disable",
    -0x07E: "Enable",
    -0x084: "Forbid",
    -0x08A: "Permit",
    -0x090: "SetSR",
    -0x096: "SuperState",
    -0x09C: "UserState",
    -0x0A2: "SetIntVector",
    -0x0A8: "AddIntServer",
    -0x0AE: "RemIntServer",
    -0x0B4: "Cause",
    -0x0BA: "Allocate",
    -0x0C0: "Deallocate",
    -0x0C6: "AllocMem",
    -0x0CC: "AllocAbs",
    -0x0D2: "FreeMem",
    -0x0D8: "AvailMem",
    -0x0DE: "AllocEntry",
    -0x0E4: "FreeEntry",
    -0x0EA: "Insert",
    -0x0F0: "AddHead",
    -0x0F6: "AddTail",
    -0x0FC: "Remove",
    -0x102: "RemHead",
    -0x108: "RemTail",
    -0x10E: "Enqueue",
    -0x114: "FindName",
    -0x11A: "SetTaskPri",
    -0x120: "SetSignal",
    -0x126: "SetExcept",
    -0x12C: "FindTask",
    -0x132: "AllocSignal",
    -0x138: "FreeSignal",
    -0x13E: "AllocTrap",
    -0x144: "FreeTrap",
    -0x14A: "AddPort",
    -0x150: "RemPort",
    -0x156: "PutMsg",
    -0x15C: "GetMsg",
    -0x162: "ReplyMsg",
    -0x168: "WaitPort",
    -0x174: "CloseLibrary",
    -0x17A: "SetFunction",
    -0x180: "SumLibrary",
    -0x186: "OpenDevice",
    -0x18C: "CloseDevice",
    -0x192: "DoIO",
    -0x198: "SendIO",
    -0x19E: "CheckIO",
    -0x1A4: "WaitIO",
    -0x1AA: "AbortIO",
    -0x1B0: "AddResource",
    -0x1B6: "RemResource",
    -0x1BC: "OpenResource",
    -0x1C2: "RawIOInit",
    -0x1C8: "RawMayGetChar",
    -0x1CE: "RawPutChar",
    -0x1D4: "RawDoFmt",
    -0x1DA: "GetCC",
    -0x1E0: "TypeOfMem",
    -0x1E6: "Procure",
    -0x1EC: "Vacate",
    -0x1F2: "OpenLibrary",
    -0x228: "CopyMem",
    -0x22E: "CopyMemQuick",
    -0x264: "CacheClearU",
    -0x26A: "CacheClearE",
    -0x270: "CacheControl",
}

GFX_LVO = {
    -0x01E: "BltBitMap",
    -0x024: "BltTemplate",
    -0x02A: "ClearEOL",
    -0x030: "ClearScreen",
    -0x036: "TextLength",
    -0x03C: "Text",
    -0x042: "SetFont",
    -0x048: "OpenFont",
    -0x04E: "CloseFont",
    -0x054: "AskSoftStyle",
    -0x05A: "SetSoftStyle",
    -0x060: "AddBob",
    -0x066: "AddVSprite",
    -0x06C: "DoCollision",
    -0x072: "DrawGList",
    -0x078: "InitGels",
    -0x07E: "InitMasks",
    -0x084: "RemIBob",
    -0x08A: "RemVSprite",
    -0x090: "SetCollision",
    -0x096: "SortGList",
    -0x09C: "AddAnimOb",
    -0x0A2: "Animate",
    -0x0A8: "GetGBuffers",
    -0x0AE: "InitGMasks",
    -0x0C0: "ScrollVPort",
    -0x0C6: "WaitBOVP",
    -0x0CC: "GetSprite",
    -0x0D2: "FreeSprite",
    -0x0D8: "ChangeSprite",
    -0x0DE: "MoveSprite",
    -0x0E4: "LockLayerRom",
    -0x0EA: "UnlockLayerRom",
    -0x0F6: "LoadRGB4",
    -0x0FC: "InitRastPort",
    -0x102: "InitVPort",
    -0x108: "MrgCop",
    -0x10E: "MakeVPort",
    -0x114: "LoadView",
    -0x11A: "WaitBlit",
    -0x120: "SetRast",
    -0x126: "Move",
    -0x12C: "Draw",
    -0x132: "AreaMove",
    -0x138: "AreaDraw",
    -0x13E: "AreaEnd",
    -0x144: "WaitTOF",
    -0x14A: "QBlit",
    -0x150: "InitArea",
    -0x156: "SetRGB4",
    -0x15C: "QBSBlit",
    -0x162: "BltClear",
    -0x168: "RectFill",
    -0x16E: "BltPattern",
    -0x174: "ReadPixel",
    -0x17A: "WritePixel",
    -0x180: "Flood",
    -0x186: "PolyDraw",
    -0x18C: "SetAPen",
    -0x192: "SetBPen",
    -0x198: "SetDrMd",
    -0x19E: "InitView",
    -0x1A4: "CBump",
    -0x1AA: "CMove",
    -0x1B0: "CWait",
    -0x1B6: "VBeamPos",
    -0x1BC: "InitBitMap",
    -0x1C2: "ScrollRaster",
    -0x1C8: "WaitBOVP2",
    -0x1CE: "SetRGB4CM",
    -0x1D4: "BltBitMapRastPort",
    -0x1DA: "AttemptLockLayerRom",
    -0x1F6: "ClipBlit",
    -0x25E: "SetMaxPen",
}

# Custom chip register names
CUSTOM_REGS = {
    0x000: "BLTDDAT", 0x002: "DMACONR", 0x004: "VPOSR", 0x006: "VHPOSR",
    0x008: "DSKDATR", 0x00A: "JOY0DAT", 0x00C: "JOY1DAT", 0x00E: "CLXDAT",
    0x010: "ADKCONR", 0x012: "POT0DAT", 0x014: "POT1DAT", 0x016: "POTGOR",
    0x018: "SERDATR", 0x01A: "DSKBYTR", 0x01C: "INTENAR", 0x01E: "INTREQR",
    0x020: "DSKPTH", 0x022: "DSKPTL", 0x024: "DSKLEN", 0x026: "DSKDAT",
    0x028: "REFPTR", 0x02A: "VPOSW", 0x02C: "VHPOSW", 0x02E: "COPCON",
    0x030: "SERDAT", 0x032: "SERPER", 0x034: "POTGO", 0x036: "JOYTEST",
    0x038: "STREQU", 0x03A: "STRVBL", 0x03C: "STRHOR", 0x03E: "STRLONG",
    0x040: "BLTCON0", 0x042: "BLTCON1", 0x044: "BLTAFWM", 0x046: "BLTALWM",
    0x048: "BLTCPTH", 0x04A: "BLTCPTL", 0x04C: "BLTBPTH", 0x04E: "BLTBPTL",
    0x050: "BLTAPTH", 0x052: "BLTAPTL", 0x054: "BLTDPTH", 0x056: "BLTDPTL",
    0x058: "BLTSIZE", 0x05A: "BLTCON0L",
    0x060: "BLTCMOD", 0x062: "BLTBMOD", 0x064: "BLTAMOD", 0x066: "BLTDMOD",
    0x070: "BLTCDAT", 0x072: "BLTBDAT", 0x074: "BLTADAT",
    0x078: "SPRHDAT",
    0x07C: "DENISEID",
    0x07E: "DSKSYNC",
    0x080: "COP1LCH", 0x082: "COP1LCL", 0x084: "COP2LCH", 0x086: "COP2LCL",
    0x088: "COPJMP1", 0x08A: "COPJMP2",
    0x08C: "COPINS",
    0x08E: "DIWSTRT", 0x090: "DIWSTOP", 0x092: "DDFSTRT", 0x094: "DDFSTOP",
    0x096: "DMACON", 0x098: "CLXCON",
    0x09A: "INTENA", 0x09C: "INTREQ",
    0x09E: "ADKCON",
    0x0E0: "BPL1PTH", 0x0E2: "BPL1PTL",
    0x100: "BPLCON0", 0x102: "BPLCON1", 0x104: "BPLCON2",
    0x108: "BPL1MOD", 0x10A: "BPL2MOD",
    0x110: "BPL1DAT",
    0x120: "SPR0PTH",
    0x180: "COLOR00",
}

def annotate_instruction(addr, mnemonic, op_str):
    """Add annotations for known library calls and hardware registers."""
    annotations = []
    
    # JSR via A6 (exec library calls)
    if mnemonic == 'jsr' and '(a6)' in op_str:
        # Extract offset from the operand
        # Format might be like "-$c6(a6)" or "($ff3a, a6)"
        import re
        m = re.search(r'(-?\$[0-9a-fA-F]+)\(a6\)', op_str)
        if not m:
            m = re.search(r'\((-?0x[0-9a-fA-F]+),\s*a6\)', op_str)
        if not m:
            m = re.search(r'\((-?\$[0-9a-fA-F]+),\s*a6\)', op_str)
        if m:
            off_str = m.group(1).replace('$', '0x')
            try:
                off = int(off_str, 16)
                if off > 0x7FFF:
                    off = off - 0x10000
                name = EXEC_LVO.get(off) or GFX_LVO.get(off)
                if name:
                    annotations.append(f"; {name}")
            except:
                pass
    
    # Check for DFF000 range references (custom chip)
    import re
    m = re.search(r'\$dff([0-9a-fA-F]{3})', op_str, re.I)
    if m:
        reg_off = int(m.group(1), 16)
        name = CUSTOM_REGS.get(reg_off)
        if name:
            annotations.append(f"; {name}")
    
    # Check for (a0) with offset that could be custom regs
    # BltBitMap often uses A0=$DFF000
    m = re.search(r'\$([0-9a-fA-F]+)\(a0\)', op_str)
    if m:
        off = int(m.group(1), 16)
        if off in CUSTOM_REGS:
            annotations.append(f"; [A0+${off:02X} = {CUSTOM_REGS[off]} if A0=$DFF000]")
    
    return " ".join(annotations)

def disassemble_region(start_off, end_off, title):
    """Disassemble a ROM region and print annotated output."""
    print(f"\n{'='*80}")
    print(f"  {title}")
    print(f"  ROM offset ${start_off:04X}-${end_off:04X}  PC=${off_to_pc(start_off):08X}-${off_to_pc(end_off):08X}")
    print(f"{'='*80}")
    
    code = rom[start_off:end_off]
    pc = off_to_pc(start_off)
    
    prev_was_rts = False
    for insn in md.disasm(code, pc):
        off = pc_to_off(insn.address)
        # Print raw hex bytes
        raw = ' '.join(f'{b:02X}' for b in insn.bytes)
        ann = annotate_instruction(insn.address, insn.mnemonic, insn.op_str)
        
        line = f"  {insn.address:08X} ({off:04X}): {raw:<24s} {insn.mnemonic:<8s} {insn.op_str}"
        if ann:
            line += f"  {ann}"
        print(line)
        
        # Mark function boundaries
        if insn.mnemonic == 'rts' or insn.mnemonic == 'rte':
            prev_was_rts = True
        elif prev_was_rts:
            print(f"  {'─'*70}")
            prev_was_rts = False


def disassemble_with_data(start_off, end_off, title):
    """Disassemble but also show data words where disassembly fails."""
    print(f"\n{'='*80}")
    print(f"  {title}")
    print(f"  ROM offset ${start_off:04X}-${end_off:04X}  PC=${off_to_pc(start_off):08X}-${off_to_pc(end_off):08X}")
    print(f"{'='*80}")
    
    code = rom[start_off:end_off]
    pc = off_to_pc(start_off)
    
    # Build address map of successfully decoded instructions
    decoded = {}
    for insn in md.disasm(code, pc):
        decoded[insn.address] = insn
    
    addr = pc
    end_addr = off_to_pc(end_off)
    prev_was_rts = False
    
    while addr < end_addr:
        if addr in decoded:
            insn = decoded[addr]
            off = pc_to_off(addr)
            raw = ' '.join(f'{b:02X}' for b in insn.bytes)
            ann = annotate_instruction(addr, insn.mnemonic, insn.op_str)
            line = f"  {addr:08X} ({off:04X}): {raw:<24s} {insn.mnemonic:<8s} {insn.op_str}"
            if ann:
                line += f"  {ann}"
            print(line)
            if insn.mnemonic in ('rts', 'rte'):
                prev_was_rts = True
            elif prev_was_rts:
                print(f"  {'─'*70}")
                prev_was_rts = False
            addr += insn.size
        else:
            off = pc_to_off(addr)
            w = read_word(off)
            if w is not None:
                print(f"  {addr:08X} ({off:04X}): {w >> 8:02X} {w & 0xFF:02X}                     dc.w     ${w:04X}")
            addr += 2
            prev_was_rts = False

# ============================================================
# TASK 1: ROM offset $C1E4 area — writes $FF to template
# ============================================================
print("\n" + "#"*80)
print("# TASK 1: Function at $FCC1E4 — writes $FF to template buffer ($104B8)")
print("# Scanning $C100-$C300")
print("#"*80)
disassemble_with_data(0xC100, 0xC300, "TASK 1: Template $FF fill function")

# Also check wider context — function entry probably starts earlier
# Look backwards for LINK or MOVEM instruction
print("\n--- Scanning backwards for function entry from $C100 ---")
for check in range(0xC0C0, 0xC100, 2):
    w = read_word(check)
    # LINK A5: 4E55, LINK A6: 4E56, MOVEM: 48xx
    if w is not None:
        if w == 0x4E55 or w == 0x4E56:
            print(f"  Found LINK at offset ${check:04X} (PC=${off_to_pc(check):08X})")
        elif (w & 0xFF00) == 0x4800:
            print(f"  Found MOVEM.L at offset ${check:04X} (PC=${off_to_pc(check):08X})")

# ============================================================
# TASK 2: ROM offset $1786 — writes $00 to template (clearing)
# ============================================================
print("\n" + "#"*80)
print("# TASK 2: Function at $FC1786 — writes $00 to template (clearing)")
print("# Scanning $1760-$17C0")
print("#"*80)
disassemble_with_data(0x1760, 0x17C0, "TASK 2: Template $00 clear function")

# Check wider context
print("\n--- Scanning for function entry from $1700 ---")
disassemble_with_data(0x1700, 0x1770, "TASK 2 wider context: $1700-$1770")


# ============================================================
# TASK 3: BltBitMap public function $FC9C08-$FCA0EC
# ============================================================
print("\n" + "#"*80)
print("# TASK 3: BltBitMap public function $FC9C08-$FCA0EC")
print("# Full disassembly looking for AllocMem + template fill")
print("#"*80)
disassemble_with_data(0x9C08, 0xA100, "TASK 3: BltBitMap public ($FC9C08-$FCA0EC+)")

# ============================================================
# Also decode the BltBitMap pass sub routines for context
# ============================================================
print("\n" + "#"*80)
print("# BONUS: BltBitMap pass sub (ascending) $FCA0EE")
print("#"*80)
disassemble_with_data(0xA0EE, 0xA220, "BltBitMap ascending pass sub")

print("\n" + "#"*80)
print("# BONUS: BltBitMap pass sub (descending) $FCA21A")
print("#"*80)
disassemble_with_data(0xA21A, 0xA340, "BltBitMap descending pass sub")

# ============================================================
# Analysis: Search for specific patterns
# ============================================================
print("\n" + "#"*80)
print("# ANALYSIS: Searching for key patterns in BltBitMap")
print("#"*80)

# Search for BLTADAT writes ($DFF074 or offset $74 from A0)
print("\n--- Searching for BLTADAT ($0074) references in BltBitMap range ---")
for off in range(0x9C08, 0xA340, 2):
    w = read_word(off)
    # Check for $0074 which could be offset to BLTADAT
    if w == 0x0074:
        # Show surrounding context
        context_start = max(0x9C08, off - 6)
        context_end = min(0xA340, off + 8)
        print(f"\n  Found $0074 at offset ${off:04X} (PC=${off_to_pc(off):08X}):")
        for c in range(context_start, context_end, 2):
            cw = read_word(c)
            marker = " <--" if c == off else ""
            print(f"    {off_to_pc(c):08X}: {cw:04X}{marker}")

# Search for AllocMem calls (JSR -$C6(A6) = 4EAE FF3A)
print("\n--- Searching for AllocMem calls in BltBitMap range ---")
for off in range(0x9C08, 0xA100, 2):
    w = read_word(off)
    w2 = read_word(off + 2) if off + 2 < 0xA100 else None
    if w == 0x4EAE and w2 == 0xFF3A:
        print(f"  JSR AllocMem at offset ${off:04X} (PC=${off_to_pc(off):08X})")

# Search for FreeMem calls
print("\n--- Searching for FreeMem calls in BltBitMap range ---")
for off in range(0x9C08, 0xA100, 2):
    w = read_word(off)
    w2 = read_word(off + 2) if off + 2 < 0xA100 else None
    if w == 0x4EAE and w2 == 0xFF2E:
        print(f"  JSR FreeMem at offset ${off:04X} (PC=${off_to_pc(off):08X})")

# Search for $FF byte fill patterns (MOVE.B #$FF or MOVE.W #$FFFF)
print("\n--- Searching for $FF fill patterns in BltBitMap range ---")
for off in range(0x9C08, 0xA100, 2):
    w = read_word(off)
    # MOVE.B #imm,... starts with 1xxx or 13FC
    # CLR patterns: 4200-42FF
    if w == 0x10FC or w == 0x12FC or w == 0x13FC:
        w2 = read_word(off + 2)
        if w2 is not None and (w2 & 0xFF) == 0xFF:
            print(f"  MOVE.B #$FF at offset ${off:04X} (PC=${off_to_pc(off):08X})")
    # MOVE.W #$FFFF
    if w == 0x30FC or w == 0x32FC or w == 0x33FC:
        w2 = read_word(off + 2)
        if w2 == 0xFFFF:
            print(f"  MOVE.W #$FFFF at offset ${off:04X} (PC=${off_to_pc(off):08X})")

# Search for MOVEQ #-1,Dn (7x FF) which sets Dn to $FFFFFFFF
print("\n--- Searching for MOVEQ #-1 (fill $FF) in BltBitMap range ---")
for off in range(0x9C08, 0xA100, 2):
    w = read_word(off)
    if (w & 0xF1FF) == 0x70FF:
        reg = (w >> 9) & 7
        print(f"  MOVEQ #-1,D{reg} at offset ${off:04X} (PC=${off_to_pc(off):08X})")

# Search for MOVEQ #0,Dn (7x 00) which sets Dn to $00000000
print("\n--- Searching for MOVEQ #0 (fill $00) in BltBitMap range ---")
for off in range(0x9C08, 0xA100, 2):
    w = read_word(off)
    if (w & 0xF1FF) == 0x7000:
        reg = (w >> 9) & 7
        print(f"  MOVEQ #0,D{reg} at offset ${off:04X} (PC=${off_to_pc(off):08X})")

# Search for CLR instructions
print("\n--- Searching for CLR in BltBitMap range ---")
for off in range(0x9C08, 0xA100, 2):
    w = read_word(off)
    if (w & 0xFF00) == 0x4200:
        print(f"  CLR at offset ${off:04X} (PC=${off_to_pc(off):08X}) opcode=${w:04X}")

# Also search TASK 2 area for context — what calls the clearing function?
print("\n" + "#"*80)
print("# TASK 2 EXTENDED: Wider context around $FC1786")
print("#"*80)
disassemble_with_data(0x16E0, 0x1820, "TASK 2 extended: $FC16E0-$FC1820")

# FreeMem calls in TASK 2 area
print("\n--- Searching for FreeMem/AllocMem in $1700-$1820 ---")
for off in range(0x1700, 0x1820, 2):
    w = read_word(off)
    w2 = read_word(off + 2)
    if w == 0x4EAE:
        if w2 == 0xFF3A:
            print(f"  JSR AllocMem at offset ${off:04X}")
        elif w2 == 0xFF2E:
            print(f"  JSR FreeMem at offset ${off:04X}")
        elif w2 is not None:
            soff = w2
            if soff > 0x7FFF:
                soff = soff - 0x10000
            name = EXEC_LVO.get(soff) or GFX_LVO.get(soff)
            if name:
                print(f"  JSR {name} (${w2:04X}) at offset ${off:04X}")
