#!/usr/bin/env python3
"""Focused decode of TASK 1 area: $C0A0-$C210 and the helper at $FC9B38"""

import capstone
import struct

ROM_PATH = 'Kick13.rom'
ROM_BASE = 0xFC0000
ROM_SIZE = 0x40000

with open(ROM_PATH, 'rb') as f:
    rom = f.read()

md = capstone.Cs(capstone.CS_ARCH_M68K, capstone.CS_MODE_M68K_000)
md.detail = True

def read_word(off):
    if 0 <= off < ROM_SIZE - 1:
        return struct.unpack_from('>H', rom, off)[0]
    return None

def read_long(off):
    if 0 <= off < ROM_SIZE - 3:
        return struct.unpack_from('>I', rom, off)[0]
    return None

def off_to_pc(off):
    return off + ROM_BASE

def pc_to_off(pc):
    return pc - ROM_BASE

def disasm(start_off, end_off, title):
    print(f"\n{'='*80}")
    print(f"  {title}")
    print(f"  ROM ${start_off:04X}-${end_off:04X}  PC=${off_to_pc(start_off):08X}-${off_to_pc(end_off):08X}")
    print(f"{'='*80}")
    
    code = rom[start_off:end_off]
    pc = off_to_pc(start_off)
    
    decoded = {}
    for insn in md.disasm(code, pc):
        decoded[insn.address] = insn
    
    addr = pc
    end_addr = off_to_pc(end_off)
    
    while addr < end_addr:
        if addr in decoded:
            insn = decoded[addr]
            off = pc_to_off(addr)
            raw = ' '.join(f'{b:02X}' for b in insn.bytes)
            print(f"  {addr:08X} ({off:04X}): {raw:<24s} {insn.mnemonic:<8s} {insn.op_str}")
            if insn.mnemonic in ('rts', 'rte', 'unlk'):
                if insn.mnemonic == 'rts':
                    print(f"  {'─'*70}")
            addr += insn.size
        else:
            off = pc_to_off(addr)
            w = read_word(off)
            if w is not None:
                print(f"  {addr:08X} ({off:04X}): {w >> 8:02X} {w & 0xFF:02X}                     dc.w     ${w:04X}")
            addr += 2

# Scan backwards from $C100 looking for RTS to find function boundary
print("--- Looking for function start before $C1E4 ---")
for off in range(0xC100, 0xC000, -2):
    w = read_word(off)
    if w == 0x4E75:  # RTS
        print(f"  RTS at ${off:04X} (PC=${off_to_pc(off):08X})")
        print(f"  Function likely starts at ${off+2:04X} (PC=${off_to_pc(off+2):08X})")
        break

# Decode the entire TASK 1 function area
disasm(0xC0A0, 0xC210, "TASK 1: Full function context $C0A0-$C210")

# Now decode $FC9B38 - the helper called by BltBitMap at $FC9DDC
# This is called with args: d3 (minterm), $1c(a5), $2a(a5), d6, ($2e(a5) + -$8(a5)), &-$14(a5)
print("\n\n" + "#"*80)
print("# BltBitMap helper at $FC9B38 - called to compute adjusted minterm")
print("#"*80)
disasm(0x9B38, 0x9C08, "Helper $FC9B38 (called from BltBitMap main)")

# Also decode $FD14E8 - called after BltBitMap_inner with -$14(a5) and d3
print("\n\n" + "#"*80)
print("# $FD14E8 - function called post-blit (possibly FreeMem for temp)")
print("#"*80)
disasm(0x114E8, 0x11580, "Post-blit function $FD14E8")

# Decode $FD3B4E - called to compute byte offset from word row info
print("\n\n" + "#"*80)
print("# $FD3B4E - byte offset calculator")
print("#"*80)
disasm(0x13B4E, 0x13B80, "Offset calculator $FD3B4E")

# Decode $FD3B44 - similar calculator used in pass subs
print("\n\n" + "#"*80)
print("# $FD3B44 - another offset calculator")
print("#"*80)
disasm(0x13B44, 0x13B80, "Offset calculator $FD3B44")

# Now the KEY question: where does BltBitMap allocate its temp buffer?
# -$14(a5) is the temp buffer pointer
# It's cleared at $FC9CB6 (CLR.L -$14(a5))
# Then checked with TST.L -$14(a5) at multiple points
# The helper at $FC9B38 receives a pointer to -$14(a5) as an argument
# Let's look at $FC9B38 more carefully for AllocMem

print("\n\n" + "#"*80)
print("# SEARCHING for AllocMem (4EAE FF3A) in helper $FC9B38-$FC9C08")
print("#"*80)
for off in range(0x9B38, 0x9C08, 2):
    w = read_word(off)
    w2 = read_word(off + 2)
    if w == 0x4EAE and w2 == 0xFF3A:
        print(f"  JSR AllocMem at ${off:04X} (PC=${off_to_pc(off):08X})")

# Search entire graphics.library ROM space for AllocMem near BltBitMap
print("\n--- AllocMem calls in $9A00-$A400 range ---")
for off in range(0x9A00, 0xA400, 2):
    w = read_word(off)
    w2 = read_word(off + 2)
    if w == 0x4EAE:
        if w2 == 0xFF3A:
            print(f"  JSR AllocMem at ${off:04X} (PC=${off_to_pc(off):08X})")
        elif w2 == 0xFF2E:
            print(f"  JSR FreeMem at ${off:04X} (PC=${off_to_pc(off):08X})")

# Search in the helper and broader area
print("\n--- AllocMem calls in $FC_ALL (broader search) ---")
for off in range(0x9000, 0x9C08, 2):
    w = read_word(off)
    w2 = read_word(off + 2)
    if w == 0x4EAE and w2 == 0xFF3A:
        print(f"  JSR AllocMem at ${off:04X} (PC=${off_to_pc(off):08X})")

# Also check the sub at $FC9B38 for JSR to any absolute address
print("\n--- All absolute JSR calls in $9B38-$9C08 ---")
for off in range(0x9B38, 0x9C08, 2):
    w = read_word(off)
    if w == 0x4EB9:  # JSR abs.l
        target = read_long(off + 2)
        print(f"  JSR ${target:08X} at ${off:04X} (PC=${off_to_pc(off):08X})")
    elif w == 0x4EAE:  # JSR d16(A6)
        d16 = read_word(off + 2)
        if d16 > 0x7FFF:
            d16 = d16 - 0x10000
        print(f"  JSR {d16}(A6) at ${off:04X} (PC=${off_to_pc(off):08X})")

# Check $FD14E8 for AllocMem/FreeMem
print("\n--- All JSR in $FD14E8 area ---")
for off in range(0x114E8, 0x11580, 2):
    w = read_word(off)
    if w == 0x4EB9:
        target = read_long(off + 2)
        print(f"  JSR ${target:08X} at ${off:04X} (PC=${off_to_pc(off):08X})")
    elif w == 0x4EAE:
        d16 = read_word(off + 2)
        if d16 > 0x7FFF:
            d16 = d16 - 0x10000
        from decode_rom_template import EXEC_LVO, GFX_LVO
        name = EXEC_LVO.get(d16) or GFX_LVO.get(d16) or f"unknown"
        print(f"  JSR {d16}(A6) = {name} at ${off:04X} (PC=${off_to_pc(off):08X})")

# Now let's look for the BLTADAT write ($DFF074) in the pass sub - 
# it's at $FCA146: move.w d0, $72(a3) where a3=$DFF000
# $72 offset = BLTBDAT! Not BLTADAT ($74).
# Let me check what offsets are written in the pass subs
print("\n\n" + "#"*80)
print("# Custom register writes in ascending pass sub $FCA0EE-$FCA218")
print("#"*80)
for off in range(0xA0EE, 0xA218, 2):
    w = read_word(off)
    # move.w Xx, $YY(a3) = 3x4x 00YY or 37xx 00YY
    if (w & 0xF1C0) == 0x3140 and (w >> 9) & 7 == 3:  # move.w ?, d16(a3)
        d16 = read_word(off + 2)
        from decode_rom_template import CUSTOM_REGS
        name = CUSTOM_REGS.get(d16, f"???")
        print(f"  {off_to_pc(off):08X}: writes to A3+${d16:04X} = {name}")
    # Also check move.l ?, d16(a3) = 27xx 00YY (for pointer regs)
    elif (w & 0xFFC0) == 0x2740:  # move.l ?, d16(a3) 
        d16 = read_word(off + 2)
        from decode_rom_template import CUSTOM_REGS
        name = CUSTOM_REGS.get(d16, f"???")
        print(f"  {off_to_pc(off):08X}: writes long to A3+${d16:04X} = {name}")
    elif w == 0x2748:  # move.l a0, d16(a3)
        d16 = read_word(off + 2)
        from decode_rom_template import CUSTOM_REGS
        name = CUSTOM_REGS.get(d16, f"???")
        print(f"  {off_to_pc(off):08X}: move.l a0, A3+${d16:04X} = {name}")

# Let's also brute-force check for $0074 offset writes
print("\n--- All writes to offset $0074 (BLTADAT) in pass subs ---")
for off in range(0xA0EE, 0xA360, 2):
    w = read_word(off)
    w2 = read_word(off + 2) if off + 2 < 0x1A360 else None
    if w2 == 0x0074:
        print(f"  {off_to_pc(off):08X}: opcode={w:04X} followed by $0074")
