#!/usr/bin/env python3
"""Decode the helper at FC9B38 and FD14E8"""
import capstone, struct

with open('Kick13.rom', 'rb') as f:
    rom = f.read()

ROM_BASE = 0xFC0000
md = capstone.Cs(capstone.CS_ARCH_M68K, capstone.CS_MODE_M68K_000)

EXEC_LVO = {-0x0C6: "AllocMem", -0x0D2: "FreeMem", -0x06C: "Alert",
             -0x078: "Disable", -0x07E: "Enable", -0x228: "CopyMem"}

def read_word(off):
    return struct.unpack_from('>H', rom, off)[0] if 0 <= off < len(rom)-1 else None

def disasm(start_off, end_off, title):
    print(f"\n{'='*80}")
    print(f"  {title}")
    print(f"  ROM ${start_off:04X}-${end_off:04X}  PC=${start_off+ROM_BASE:08X}-${end_off+ROM_BASE:08X}")
    print(f"{'='*80}")
    code = rom[start_off:end_off]
    decoded = {}
    for insn in md.disasm(code, start_off + ROM_BASE):
        decoded[insn.address] = insn
    addr = start_off + ROM_BASE
    end = end_off + ROM_BASE
    while addr < end:
        if addr in decoded:
            insn = decoded[addr]
            raw = ' '.join(f'{b:02X}' for b in insn.bytes)
            ann = ""
            if insn.mnemonic == 'jsr' and 'a6' in insn.op_str:
                import re
                m = re.search(r'-?\$([0-9a-fA-F]+)\(a6\)', insn.op_str)
                if m:
                    v = int(m.group(1), 16)
                    if v > 0x7FFF: v -= 0x10000
                    else: v = -v
                    name = EXEC_LVO.get(v, "")
                    if name: ann = f"  ; {name}"
            print(f"  {addr:08X}: {raw:<24s} {insn.mnemonic:<8s} {insn.op_str}{ann}")
            if insn.mnemonic == 'rts':
                print(f"  {'─'*70}")
            addr += insn.size
        else:
            w = read_word(addr - ROM_BASE)
            print(f"  {addr:08X}: {w>>8:02X} {w&0xFF:02X}                     dc.w     ${w:04X}")
            addr += 2

# Helper at FC9B38
disasm(0x9B38, 0x9C08, "BltBitMap helper FC9B38 - minterm adjust + temp alloc")

# FD14E8 - post-blit
disasm(0x114E8, 0x11560, "FD14E8 - post-blit function (FreeMem for temp?)")

# FD3B4E - offset calculator
disasm(0x13B44, 0x13B70, "FD3B44/FD3B4E - byte offset calculator")

# FC6DB6 - word count calculator  
disasm(0x6DB6, 0x6DE0, "FC6DB6 - word count calculator")

# Also search FC9B38-FC9C08 for AllocMem
print("\n--- All JSR calls in FC9B38-FC9C08 ---")
for off in range(0x9B38, 0x9C08, 2):
    w = read_word(off)
    w2 = read_word(off+2)
    if w == 0x4EB9:  # JSR abs.l
        tgt = struct.unpack_from('>I', rom, off+2)[0]
        print(f"  {off+ROM_BASE:08X}: JSR ${tgt:08X}")
    elif w == 0x4EAE:  # JSR d16(A6)
        d16 = w2
        if d16 > 0x7FFF: d16 -= 0x10000
        name = EXEC_LVO.get(d16, f"LVO ${d16:04X}")
        print(f"  {off+ROM_BASE:08X}: JSR {d16}(A6) = {name}")
    elif w == 0x4EBA:  # JSR d16(PC)
        d16 = w2
        if d16 > 0x7FFF: d16 -= 0x10000
        tgt = (off + ROM_BASE + 2) + d16
        print(f"  {off+ROM_BASE:08X}: JSR ${tgt:08X}(PC)")

print("\n--- All JSR in FD14E8-FD1560 ---")
for off in range(0x114E8, 0x11560, 2):
    w = read_word(off)
    w2 = read_word(off+2)
    if w == 0x4EB9:
        tgt = struct.unpack_from('>I', rom, off+2)[0]
        print(f"  {off+ROM_BASE:08X}: JSR ${tgt:08X}")
    elif w == 0x4EAE:
        d16 = w2
        if d16 > 0x7FFF: d16 -= 0x10000
        name = EXEC_LVO.get(d16, f"LVO ${d16:04X}")
        print(f"  {off+ROM_BASE:08X}: JSR {d16}(A6) = {name}")
