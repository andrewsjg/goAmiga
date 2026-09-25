#!/usr/bin/env python3
"""Disassemble Kickstart 1.3 ROM around key blitter-related addresses."""

from capstone import Cs, CS_ARCH_M68K, CS_MODE_M68K_000
import struct

ROM_PATH = "Kick13.rom"
ROM_BASE = 0xFC0000

# Key addresses to analyze
TARGETS = [
    (0xFC5E12, "SetRast/BltBitMap BLTSIZE write area"),
    (0xFC577E, "DrawImage line draw origin"),
    (0xFC5C58, "Blit origin #3"),
    (0xFC7630, "Blit origin #4 (QBlit handler)"),
    (0xFC553A, "Blit origin #5"),
]

# Custom chip register names for annotation
CUSTOM_REGS = {
    0x000: "BLTDDAT", 0x002: "DMACONR", 0x004: "VPOSR", 0x006: "VHPOSR",
    0x008: "DSKDATR", 0x00A: "JOY0DAT", 0x00C: "JOY1DAT", 0x00E: "CLXDAT",
    0x010: "ADKCONR", 0x012: "POT0DAT", 0x014: "POT1DAT", 0x016: "POTGOR",
    0x018: "SERDATR", 0x01A: "DSKBYTR", 0x01C: "INTENAR", 0x01E: "INTREQR",
    0x020: "DSKPT", 0x024: "DSKLEN", 0x026: "DSKDAT",
    0x028: "REFPTR", 0x02A: "VPOSW", 0x02C: "VHPOSW", 0x02E: "COPCON",
    0x030: "SERDAT", 0x032: "SERPER", 0x034: "POTGO",
    0x036: "JOYTEST", 0x038: "STREQU", 0x03A: "STRVBL", 0x03C: "STRHOR",
    0x03E: "STRLONG",
    0x040: "BLTCON0", 0x042: "BLTCON1", 0x044: "BLTAFWM", 0x046: "BLTALWM",
    0x048: "BLTCPT", 0x04A: "BLTCPT+2", 0x04C: "BLTBPT", 0x04E: "BLTBPT+2",
    0x050: "BLTAPT", 0x052: "BLTAPT+2", 0x054: "BLTDPT", 0x056: "BLTDPT+2",
    0x058: "BLTSIZE", 0x05A: "BLTCON0L", 0x05C: "BLTSIZV", 0x05E: "BLTSIZH",
    0x060: "BLTCMOD", 0x062: "BLTBMOD", 0x064: "BLTAMOD", 0x066: "BLTDMOD",
    0x070: "BLTCDAT", 0x072: "BLTBDAT", 0x074: "BLTADAT",
    0x078: "SPRHDAT",
    0x07C: "DENISEID",
    0x07E: "DSKSYNC",
    0x080: "COP1LC", 0x084: "COP2LC", 0x088: "COPJMP1", 0x08A: "COPJMP2",
    0x08C: "COPINS",
    0x08E: "DIWSTRT", 0x090: "DIWSTOP", 0x092: "DDFSTRT", 0x094: "DDFSTOP",
    0x096: "DMACON", 0x098: "CLXCON",
    0x09A: "INTENA", 0x09C: "INTREQ",
    0x09E: "ADKCON",
    0x0A0: "AUD0LC", 0x0A4: "AUD0LEN", 0x0A6: "AUD0PER", 0x0A8: "AUD0VOL",
    0x0AA: "AUD0DAT",
    0x0E0: "BPL1PT", 0x0E4: "BPL2PT", 0x0E8: "BPL3PT", 0x0EC: "BPL4PT",
    0x0F0: "BPL5PT", 0x0F4: "BPL6PT",
    0x100: "BPLCON0", 0x102: "BPLCON1", 0x104: "BPLCON2",
    0x108: "BPL1MOD", 0x10A: "BPL2MOD",
    0x110: "BPL1DAT",
    0x120: "SPR0PT",
    0x180: "COLOR00",
}

# Exec library vector offsets (negative from A6)
EXEC_VECTORS = {
    -0x006: "Supervisor",
    -0x00C: "ExitIntr",
    -0x012: "Schedule",
    -0x018: "Reschedule",
    -0x01E: "Switch",
    -0x024: "Dispatch",
    -0x02A: "Exception",
    -0x030: "InitCode",
    -0x036: "InitStruct",
    -0x03C: "MakeLibrary",
    -0x042: "MakeFunctions",
    -0x048: "FindResident",
    -0x04E: "InitResident",
    -0x054: "Alert",
    -0x05A: "Debug",
    -0x060: "Disable",
    -0x066: "Enable",
    -0x06C: "Forbid",
    -0x072: "Permit",
    -0x078: "SetSR",
    -0x07E: "SuperState",
    -0x084: "UserState",
    -0x08A: "SetIntVector",
    -0x090: "AddIntServer",
    -0x096: "RemIntServer",
    -0x09C: "Cause",
    -0x0A2: "Allocate",
    -0x0A8: "Deallocate",
    -0x0AE: "AllocMem",
    -0x0B4: "AllocAbs",
    -0x0BA: "FreeMem",
    -0x0C0: "AvailMem",
    -0x0C6: "AllocEntry",
    -0x0CC: "FreeEntry",
    -0x0D2: "Insert",
    -0x0D8: "AddHead",
    -0x0DE: "AddTail",
    -0x0E4: "Remove",
    -0x0EA: "RemHead",
    -0x0F0: "RemTail",
    -0x0F6: "Enqueue",
    -0x0FC: "FindName",
    -0x102: "AddTask",
    -0x108: "RemTask",
    -0x10E: "FindTask",
    -0x114: "SetTaskPri",
    -0x11A: "SetSignal",
    -0x120: "SetExcept",
    -0x126: "Wait",
    -0x12C: "Signal",
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
    -0x16E: "FindPort",
    -0x174: "AddLibrary",
    -0x17A: "RemLibrary",
    -0x180: "OldOpenLibrary",
    -0x186: "CloseLibrary",
    -0x18C: "SetFunction",
    -0x192: "SumLibrary",
    -0x198: "AddDevice",
    -0x19E: "RemDevice",
    -0x1A4: "OpenDevice",
    -0x1AA: "CloseDevice",
    -0x1B0: "DoIO",
    -0x1B6: "SendIO",
    -0x1BC: "CheckIO",
    -0x1C2: "WaitIO",
    -0x1C8: "AbortIO",
    -0x1CE: "AddResource",
    -0x1D4: "RemResource",
    -0x1DA: "OpenResource",
    -0x1E0: "RawIOInit",
    -0x1E6: "RawMayGetChar",
    -0x1EC: "RawPutChar",
    -0x1F2: "RawDoFmt",
    -0x1F8: "GetCC",
    -0x1FE: "TypeOfMem",
    -0x204: "Procure",
    -0x20A: "Vacate",
    -0x210: "OpenLibrary",
    -0x216: "InitSemaphore",
    -0x21C: "ObtainSemaphore",
    -0x222: "ReleaseSemaphore",
    -0x228: "AttemptSemaphore",
    -0x22E: "ObtainSemaphoreList",
    -0x234: "ReleaseSemaphoreList",
    -0x23A: "FindSemaphore",
    -0x240: "AddSemaphore",
    -0x246: "RemSemaphore",
    -0x24C: "SumKickData",
    -0x252: "AddMemList",
    -0x258: "CopyMem",
    -0x25E: "CopyMemQuick",
    -0x264: "CacheClearU",
    -0x26A: "CacheClearE",
    -0x270: "CacheControl",
}

# Graphics library vector offsets
GFX_VECTORS = {
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
    -0x0B4: "DrawEllipse",
    -0x0BA: "AreaEllipse",
    -0x0C0: "LoadRGB4",
    -0x0C6: "InitRastPort",
    -0x0CC: "InitVPort",
    -0x0D2: "MrgCop",
    -0x0D8: "MakeVPort",
    -0x0DE: "LoadView",
    -0x0E4: "WaitBlit",
    -0x0EA: "SetRast",
    -0x0F0: "Move",
    -0x0F6: "Draw",
    -0x0FC: "AreaMove",
    -0x102: "AreaDraw",
    -0x108: "AreaEnd",
    -0x10E: "WaitTOF",
    -0x114: "QBlit",
    -0x11A: "InitArea",
    -0x120: "SetRGB4",
    -0x126: "QBSBlit",
    -0x12C: "BltClear",
    -0x132: "RectFill",
    -0x138: "BltPattern",
    -0x13E: "ReadPixel",
    -0x144: "WritePixel",
    -0x14A: "Flood",
    -0x150: "PolyDraw",
    -0x156: "SetAPen",
    -0x15C: "SetBPen",
    -0x162: "SetDrMd",
    -0x168: "InitView",
    -0x16E: "CBump",
    -0x174: "CMove",
    -0x17A: "CWait",
    -0x180: "VBeamPos",
    -0x186: "InitBitMap",
    -0x18C: "ScrollRaster",
    -0x192: "WaitBOVP",
    -0x198: "GetSprite",
    -0x19E: "FreeSprite",
    -0x1A4: "ChangeSprite",
    -0x1AA: "MoveSprite",
    -0x1B0: "LockLayerRom",
    -0x1B6: "UnlockLayerRom",
    -0x1BC: "SyncSBitMap",
    -0x1C2: "CopySBitMap",
    -0x1C8: "OwnBlitter",
    -0x1CE: "DisownBlitter",
    -0x1D4: "InitTmpRas",
    -0x1DA: "AskFont",
    -0x1E0: "AddFont",
    -0x1E6: "RemFont",
    -0x1EC: "AllocRaster",
    -0x1F2: "FreeRaster",
    -0x1F8: "AndRectRegion",
    -0x1FE: "OrRectRegion",
    -0x204: "NewRegion",
    -0x20A: "ClearRectRegion",
    -0x210: "ClearRegion",
    -0x216: "DisposeRegion",
    -0x21C: "FreeVPortCopLists",
    -0x222: "FreeCopList",
    -0x228: "ClipBlit",
    -0x22E: "XorRectRegion",
    -0x234: "FreeCprList",
    -0x23A: "GetColorMap",
    -0x240: "FreeColorMap",
    -0x246: "GetRGB4",
    -0x24C: "ScrollVPort",
    -0x252: "UCopperListInit",
    -0x258: "FreeGBuffers",
    -0x25E: "BltBitMapRastPort",
    -0x264: "OrRegionRegion",
    -0x26A: "XorRegionRegion",
    -0x270: "AndRegionRegion",
    -0x276: "SetRGB4CM",
    -0x27C: "BltMaskBitMapRastPort",
    -0x282: "AttemptLockLayerRom",
}


def annotate_instruction(mnemonic, op_str, addr, rom_data):
    """Add annotations for custom chip register accesses and library calls."""
    notes = []
    
    # Check for custom chip register access patterns
    # Pattern: offset(An) where An points to $DFF000
    import re
    
    # Match hex offsets in operand - like $40, $42, $48, $54, $58 etc
    # Common patterns: XXXX(a0) where a0=$DFF000
    hex_matches = re.findall(r'\$([0-9a-fA-F]+)', op_str)
    
    # Check for MOVE to custom register via offset from An
    offset_match = re.search(r'(\$[0-9a-fA-F]+)\((a[0-7])\)', op_str)
    if offset_match:
        off_val = int(offset_match.group(1)[1:], 16)
        if off_val in CUSTOM_REGS:
            notes.append(f"  ; → {CUSTOM_REGS[off_val]} (if An=$DFF000)")
    
    # Check for JSR/BSR to absolute addresses
    if mnemonic in ('jsr', 'bsr'):
        for h in hex_matches:
            target = int(h, 16)
            if ROM_BASE <= target < ROM_BASE + 0x40000:
                notes.append(f"  ; ROM subroutine")
    
    # Check for library calls via A6: JSR offset(A6)
    lib_match = re.search(r'(-?\$[0-9a-fA-F]+)\(a6\)', op_str)
    if lib_match:
        off_str = lib_match.group(1)
        if off_str.startswith('-$'):
            off_val = -int(off_str[2:], 16)
        elif off_str.startswith('$'):
            off_val = int(off_str[1:], 16)
        else:
            off_val = 0
        if off_val in EXEC_VECTORS:
            notes.append(f"  ; exec.{EXEC_VECTORS[off_val]}")
        if off_val in GFX_VECTORS:
            notes.append(f"  ; gfx.{GFX_VECTORS[off_val]}")
    
    # Check for TRAP
    if mnemonic == 'trap':
        notes.append(f"  ; TRAP handler")
    
    return ''.join(notes)


def disassemble_region(rom_data, target_addr, context_bytes=160, label=""):
    """Disassemble a region of ROM around target_addr."""
    md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)
    md.detail = True
    
    offset = target_addr - ROM_BASE
    start_offset = max(0, offset - context_bytes)
    end_offset = min(len(rom_data), offset + context_bytes)
    
    region = rom_data[start_offset:end_offset]
    base_addr = ROM_BASE + start_offset
    
    print(f"\n{'='*80}")
    print(f"Target: ${target_addr:06X} (ROM offset ${offset:04X}) — {label}")
    print(f"Disassembly range: ${base_addr:06X} - ${ROM_BASE + end_offset:06X}")
    print(f"{'='*80}")
    
    instructions = list(md.disasm(region, base_addr))
    
    for insn in instructions:
        marker = " >>>" if insn.address == target_addr else "    "
        annotation = annotate_instruction(insn.mnemonic, insn.op_str, insn.address, rom_data)
        hex_bytes = ' '.join(f'{b:02x}' for b in insn.bytes)
        print(f"{marker} ${insn.address:06X}: {hex_bytes:24s} {insn.mnemonic:10s} {insn.op_str}{annotation}")
    
    return instructions


def find_function_boundaries(rom_data, target_addr, instructions):
    """Try to identify function boundaries by looking for RTS/RTE before and after."""
    rts_before = None
    rts_after = None
    
    for insn in instructions:
        if insn.address >= target_addr:
            break
        if insn.mnemonic in ('rts', 'rte'):
            rts_before = insn.address
    
    for insn in instructions:
        if insn.address <= target_addr:
            continue
        if insn.mnemonic in ('rts', 'rte'):
            rts_after = insn.address
            break
    
    return rts_before, rts_after


def scan_for_references(rom_data, target_addr):
    """Scan ROM for JSR/BSR references to the target address area."""
    # Look for JSR $FC???? (4EB9 followed by 00FC xxxx) 
    # and BSR.W with appropriate displacement
    refs = []
    target_hi = (target_addr >> 16) & 0xFFFF
    target_lo = target_addr & 0xFFFF
    
    # JSR absolute long: 4EB9 HHHH LLLL
    for i in range(0, len(rom_data) - 5):
        w = struct.unpack_from('>H', rom_data, i)[0]
        if w == 0x4EB9:  # JSR abs.L
            addr_val = struct.unpack_from('>I', rom_data, i+2)[0]
            if abs(addr_val - target_addr) < 64:
                caller = ROM_BASE + i
                refs.append((caller, 'JSR', addr_val))
    
    # BSR.W: 6100 disp16
    for i in range(0, len(rom_data) - 3):
        w = struct.unpack_from('>H', rom_data, i)[0]
        if w == 0x6100:  # BSR.W
            disp = struct.unpack_from('>h', rom_data, i+2)[0]
            dest = ROM_BASE + i + 2 + disp
            if abs(dest - target_addr) < 64:
                caller = ROM_BASE + i
                refs.append((caller, 'BSR.W', dest))
        # BSR.B: 61xx where xx != 00 and xx != FF
        elif (w >> 8) == 0x61 and (w & 0xFF) != 0x00 and (w & 0xFF) != 0xFF:
            disp = w & 0xFF
            if disp & 0x80:
                disp -= 256
            dest = ROM_BASE + i + 2 + disp
            if abs(dest - target_addr) < 64:
                caller = ROM_BASE + i
                refs.append((caller, 'BSR.B', dest))
    
    return refs


def main():
    with open(ROM_PATH, 'rb') as f:
        rom_data = f.read()
    
    print(f"ROM loaded: {len(rom_data)} bytes ({len(rom_data)//1024}KB)")
    print(f"Maps to ${ROM_BASE:06X}-${ROM_BASE + len(rom_data) - 1:06X}")
    
    for target_addr, label in TARGETS:
        instructions = disassemble_region(rom_data, target_addr, context_bytes=200, label=label)
        
        rts_before, rts_after = find_function_boundaries(rom_data, target_addr, instructions)
        print(f"\n  Function boundary hints:")
        if rts_before:
            print(f"    RTS before target: ${rts_before:06X}")
            print(f"    → Function likely starts after ${rts_before:06X}")
        if rts_after:
            print(f"    RTS after target: ${rts_after:06X}")
        
        # Look for references to the function
        # Search for the start of the function (after last RTS)
        func_start = (rts_before + 2) if rts_before else (target_addr - 100)
        refs = scan_for_references(rom_data, func_start)
        if refs:
            print(f"\n  References to function area (${func_start:06X}±64):")
            for caller, ref_type, dest in refs[:20]:
                print(f"    ${caller:06X}: {ref_type} ${dest:06X}")
        
        # Also look for what's near the BLTSIZE write
        print(f"\n  Nearby BLTSIZE writes (opcode writing to offset $58):")
        # Search for MOVE.W xx,$58(An) patterns near target
        offset = target_addr - ROM_BASE
        region_start = max(0, offset - 200)
        region_end = min(len(rom_data), offset + 200)
        for i in range(region_start, region_end - 3, 2):
            w = struct.unpack_from('>H', rom_data, i)[0]
            # 3140 0058 = MOVE.W D0,$0058(A0) — common BLTSIZE write
            # 3169 xxxx 0058 = MOVE.W xxxx(A1),$0058(A0)
            # 317C xxxx 0058 = MOVE.W #xxxx,$0058(A0) — immediate to BLTSIZE
            if i + 3 < len(rom_data):
                next_w = struct.unpack_from('>H', rom_data, i+2)[0]
                if next_w == 0x0058:
                    addr = ROM_BASE + i
                    print(f"    ${addr:06X}: opcode ${w:04X} — writing to $58(An) = BLTSIZE")

    # Extra analysis: look for the WaitBlit pattern (polling DMACONR bit 14)
    print(f"\n{'='*80}")
    print("Searching for WaitBlit/OwnBlitter patterns near target addresses...")
    print(f"{'='*80}")
    
    # The common WaitBlit poll pattern: BTST #6,$0002(A0) where A0=$DFF000
    # Encoding: 0828 0006 0002
    count = 0
    for i in range(0, len(rom_data) - 5, 2):
        if rom_data[i:i+6] == bytes([0x08, 0x28, 0x00, 0x06, 0x00, 0x02]):
            addr = ROM_BASE + i
            # Check if next instruction branches back (BEQ to self-2)
            if i + 7 < len(rom_data):
                next_w = struct.unpack_from('>H', rom_data, i+6)[0]
                branch_info = ""
                if next_w == 0x0828:
                    branch_info = " (double poll: BTST again)"
                elif (next_w >> 8) == 0x67:  # BEQ
                    disp = next_w & 0xFF
                    if disp == 0xF8 or disp == 0xFA or disp == 0xFC:  # branch back  
                        branch_info = " → WaitBlit POLL LOOP"
                    else:
                        branch_info = f" → BEQ disp=${disp:02X}"
                # Only print ones near our targets
                for target, _ in TARGETS:
                    if abs(addr - target) < 0x600:
                        print(f"  ${addr:06X}: BTST #6,$0002(A0) — WaitBlit check{branch_info}")
                        count += 1
                        break
    print(f"  Found {count} WaitBlit checks near target addresses")

    # Deeper: find the actual "OwnBlitter + WaitBlit" helper called before blits
    print(f"\n{'='*80}")
    print("Analyzing the WaitBlit helper subroutine structure...")
    print(f"{'='*80}")
    
    # The pattern at our targets is: 
    # 0828 0006 0002  BTST #6,$0002(A0)   ; test BBUSY in DMACONR
    # 0828 0006 0002  BTST #6,$0002(A0)   ; test again (read twice)
    # 6704           BEQ.S +4             ; if blitter idle, skip
    # 6100 xxxx      BSR.W waitblit_sub   ; else call WaitBlit
    # This is the "inline WaitBlit" pattern in graphics.library
    
    # Find all BSR.W targets from this pattern
    waitblit_targets = set()
    for i in range(0, len(rom_data) - 17, 2):
        if (rom_data[i:i+6] == bytes([0x08, 0x28, 0x00, 0x06, 0x00, 0x02]) and
            rom_data[i+6:i+12] == bytes([0x08, 0x28, 0x00, 0x06, 0x00, 0x02])):
            # Check for BEQ.S $04 then BSR.W
            if rom_data[i+12:i+14] == bytes([0x67, 0x04]):
                if struct.unpack_from('>H', rom_data, i+14)[0] == 0x6100:
                    disp = struct.unpack_from('>h', rom_data, i+16)[0]
                    target = ROM_BASE + i + 16 + disp
                    waitblit_targets.add(target)
    
    print(f"  WaitBlit helper target(s):")
    for t in sorted(waitblit_targets):
        print(f"    ${t:06X}")
        disassemble_region(rom_data, t, context_bytes=80, label="WaitBlit helper")


if __name__ == '__main__':
    main()
