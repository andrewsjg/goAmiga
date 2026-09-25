#!/usr/bin/env python3
"""Decode 68000 ROM functions around BltBitMap caller at $FCA0BA."""

import struct
import sys

with open('Kick13.rom', 'rb') as f:
    ROM = f.read()

ROM_BASE = 0xFC0000
ROM_SIZE = len(ROM)

def rb(off):
    if 0 <= off < ROM_SIZE: return ROM[off]
    return 0
def rw(off):
    if 0 <= off < ROM_SIZE-1: return (ROM[off]<<8)|ROM[off+1]
    return 0
def rl(off):
    if 0 <= off < ROM_SIZE-3: return (ROM[off]<<24)|(ROM[off+1]<<16)|(ROM[off+2]<<8)|ROM[off+3]
    return 0

def signed_byte(v): return v if v < 128 else v - 256
def signed_word(v): return v if v < 0x8000 else v - 0x10000
def signed_long(v): return v if v < 0x80000000 else v - 0x100000000

# Condition code names
CC = ['T','F','HI','LS','CC','CS','NE','EQ','VC','VS','PL','MI','GE','LT','GT','LE']

def ea_str(mode, reg, size, off):
    """Decode effective address, return (string, new_offset, extra_words)."""
    extra = 0
    if mode < 7:
        if mode == 0: return f"D{reg}", off, 0
        if mode == 1: return f"A{reg}", off, 0
        if mode == 2: return f"(A{reg})", off, 0
        if mode == 3: return f"(A{reg})+", off, 0
        if mode == 4: return f"-(A{reg})", off, 0
        if mode == 5:
            disp = signed_word(rw(off))
            return f"${disp&0xFFFF:04X}(A{reg})", off+2, 1
        if mode == 6:
            ext = rw(off)
            da = 'A' if ext & 0x8000 else 'D'
            xreg = (ext >> 12) & 7
            wl = '.L' if ext & 0x0800 else '.W'
            disp = signed_byte(ext & 0xFF)
            return f"${disp&0xFF:02X}(A{reg},${da}{xreg}{wl})", off+2, 1
    else:
        if reg == 0:
            val = rw(off)
            return f"$%04X.W" % val, off+2, 1
        if reg == 1:
            val = rl(off)
            return f"$%08X" % val, off+4, 2
        if reg == 2:
            disp = signed_word(rw(off))
            pc_val = ROM_BASE + off  # PC at time of extension word
            target = pc_val + disp
            return "$%04X(PC) {=$%06X}" % (disp & 0xFFFF, target & 0xFFFFFF), off+2, 1
        if reg == 3:
            ext = rw(off)
            da = 'A' if ext & 0x8000 else 'D'
            xreg = (ext >> 12) & 7
            wl = '.L' if ext & 0x0800 else '.W'
            disp = signed_byte(ext & 0xFF)
            pc_val = ROM_BASE + off
            return f"${disp&0xFF:02X}(PC,{da}{xreg}{wl})", off+2, 1
        if reg == 4:
            if size == 1:  # byte
                val = rw(off)
                return f"#${val&0xFF:02X}", off+2, 1
            elif size == 2:  # word
                val = rw(off)
                return f"#${val:04X}", off+2, 1
            elif size == 4:  # long
                val = rl(off)
                return f"#${val:08X}", off+4, 2
    return f"???[m={mode},r={reg}]", off, 0

def size_char(s):
    if s == 0: return '.B'
    if s == 1: return '.W'
    if s == 2: return '.L'
    return ''

def size_bytes(s):
    if s == 0: return 1
    if s == 1: return 2
    if s == 2: return 4
    return 2

def regmask_str(mask, predec=False):
    """Convert register mask to string. For predecrement, bit order is reversed."""
    regs = []
    for i in range(16):
        if predec:
            bit = 15 - i
        else:
            bit = i
        if mask & (1 << bit):
            if i < 8:
                regs.append(f"D{i}")
            else:
                regs.append(f"A{i-8}")
    return '/'.join(regs)

def disasm_one(off):
    """Disassemble one instruction at ROM offset. Return (text, next_off)."""
    pc = ROM_BASE + off
    w = rw(off)
    off2 = off + 2

    top = (w >> 12) & 0xF

    # NOP
    if w == 0x4E71:
        return "NOP", off2

    # RTS
    if w == 0x4E75:
        return "RTS", off2

    # RTE
    if w == 0x4E73:
        return "RTE", off2

    # TRAP #n
    if (w & 0xFFF0) == 0x4E40:
        return f"TRAP    #{w&0xF}", off2

    # LINK
    if (w & 0xFFF8) == 0x4E50:
        disp = signed_word(rw(off2))
        return f"LINK    A{w&7},#${disp&0xFFFF:04X}", off2+2

    # UNLK
    if (w & 0xFFF8) == 0x4E58:
        return f"UNLK    A{w&7}", off2

    # JMP (ea)
    if (w & 0xFFC0) == 0x4EC0:
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, 4, off2)
        return f"JMP     {ea}", off2

    # JSR (ea)
    if (w & 0xFFC0) == 0x4E80:
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, 4, off2)
        return f"JSR     {ea}", off2

    # LEA
    if (w & 0xF1C0) == 0x41C0:
        areg = (w >> 9) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, 4, off2)
        return f"LEA     {ea},A{areg}", off2

    # PEA
    if (w & 0xFFC0) == 0x4840:
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, 4, off2)
        return f"PEA     {ea}", off2

    # MOVEM to memory (register to memory)
    if (w & 0xFB80) == 0x4880:
        sz = '.L' if w & 0x0040 else '.W'
        mode = (w >> 3) & 7
        reg = w & 7
        mask = rw(off2)
        off2 += 2
        predec = (mode == 4)
        ea, off2, _ = ea_str(mode, reg, 4, off2)
        return f"MOVEM{sz} {regmask_str(mask, predec)},{ea}", off2

    # MOVEM from memory (memory to register)
    if (w & 0xFB80) == 0x4C80:
        sz = '.L' if w & 0x0040 else '.W'
        mode = (w >> 3) & 7
        reg = w & 7
        mask = rw(off2)
        off2 += 2
        ea, off2, _ = ea_str(mode, reg, 4 if w & 0x0040 else 2, off2)
        return f"MOVEM{sz} {ea},{regmask_str(mask, False)}", off2

    # MOVEQ
    if (w & 0xF100) == 0x7000:
        dreg = (w >> 9) & 7
        val = signed_byte(w & 0xFF)
        return f"MOVEQ   #${w&0xFF:02X},D{dreg}", off2

    # Bcc / BRA / BSR
    if top == 6:
        cond = (w >> 8) & 0xF
        disp8 = w & 0xFF
        if disp8 == 0:
            disp = signed_word(rw(off2))
            target = pc + 2 + disp
            off2 += 2
        elif disp8 == 0xFF:
            disp = signed_long(rl(off2))
            target = pc + 2 + disp
            off2 += 4
        else:
            disp = signed_byte(disp8)
            target = pc + 2 + disp
        if cond == 0:
            mnem = "BRA"
        elif cond == 1:
            mnem = "BSR"
        else:
            mnem = f"B{CC[cond]}"
        return f"{mnem:8s}${target:06X}", off2

    # DBcc
    if (w & 0xF0C0) == 0x50C8:
        cond = (w >> 8) & 0xF
        dreg = w & 7
        disp = signed_word(rw(off2))
        target = pc + 2 + disp
        off2 += 2
        return f"DB{CC[cond]}    D{dreg},${target:06X}", off2

    # Scc
    if (w & 0xF0C0) == 0x50C0 and ((w >> 3) & 7) != 1:
        cond = (w >> 8) & 0xF
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, 1, off2)
        return f"S{CC[cond]}     {ea}", off2

    # ADDQ / SUBQ
    if top == 5 and ((w >> 6) & 3) != 3:
        sz = (w >> 6) & 3
        data = (w >> 9) & 7
        if data == 0: data = 8
        mode = (w >> 3) & 7
        reg = w & 7
        op = "SUBQ" if w & 0x0100 else "ADDQ"
        ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
        return f"{op}{size_char(sz)}  #{data},{ea}", off2

    # CLR
    if (w & 0xFF00) == 0x4200:
        sz = (w >> 6) & 3
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
        return f"CLR{size_char(sz)}    {ea}", off2

    # TST
    if (w & 0xFF00) == 0x4A00:
        sz = (w >> 6) & 3
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
        return f"TST{size_char(sz)}    {ea}", off2

    # EXT
    if (w & 0xFEB8) == 0x4880:
        sz = '.L' if w & 0x0040 else '.W'
        return f"EXT{sz}    D{w&7}", off2

    # SWAP
    if (w & 0xFFF8) == 0x4840:
        return f"SWAP    D{w&7}", off2

    # NOT
    if (w & 0xFF00) == 0x4600:
        sz = (w >> 6) & 3
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
        return f"NOT{size_char(sz)}    {ea}", off2

    # NEG
    if (w & 0xFF00) == 0x4400:
        sz = (w >> 6) & 3
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
        return f"NEG{size_char(sz)}    {ea}", off2

    # BTST / BCHG / BCLR / BSET (dynamic, register)
    if top == 0 and (w & 0x0100) and ((w >> 6) & 3) != 3:
        op_type = (w >> 6) & 3
        ops = ['BTST','BCHG','BCLR','BSET']
        dreg = (w >> 9) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, 1, off2)
        return f"{ops[op_type]}    D{dreg},{ea}", off2

    # BTST / BCHG / BCLR / BSET (static, immediate)
    if (w & 0xFF00) == 0x0800:
        op_type = (w >> 6) & 3
        ops = ['BTST','BCHG','BCLR','BSET']
        bit = rw(off2) & 0xFF
        off2 += 2
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, 1, off2)
        return f"{ops[op_type]}    #{bit},{ea}", off2

    # MOVE / MOVEA
    if top in (1, 2, 3):
        sz_map = {1: 0, 3: 1, 2: 2}  # 1=byte, 3=word, 2=long
        sz = sz_map[top]
        sb = size_bytes(sz)
        src_mode = (w >> 3) & 7
        src_reg = w & 7
        dst_reg = (w >> 9) & 7
        dst_mode = (w >> 6) & 7
        src_ea, off2, _ = ea_str(src_mode, src_reg, sb, off2)
        dst_ea, off2, _ = ea_str(dst_mode, dst_reg, sb, off2)
        if dst_mode == 1:
            return f"MOVEA{size_char(sz)} {src_ea},{dst_ea}", off2
        return f"MOVE{size_char(sz)}  {src_ea},{dst_ea}", off2

    # CMP
    if (w & 0xF000) == 0xB000:
        sz = (w >> 6) & 3
        if sz == 3:
            # CMPA
            sz2 = 2 if w & 0x0100 else 1
            areg = (w >> 9) & 7
            mode = (w >> 3) & 7
            reg = w & 7
            ea, off2, _ = ea_str(mode, reg, size_bytes(sz2), off2)
            return f"CMPA{size_char(sz2)} {ea},A{areg}", off2
        if w & 0x0100:
            # CMPM
            if (w & 0x0038) == 0x0008:
                ax = (w >> 9) & 7
                ay = w & 7
                return f"CMPM{size_char(sz)} (A{ay})+,(A{ax})+", off2
            # EOR
            dreg = (w >> 9) & 7
            mode = (w >> 3) & 7
            reg = w & 7
            ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
            return f"EOR{size_char(sz)}    D{dreg},{ea}", off2
        dreg = (w >> 9) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
        return f"CMP{size_char(sz)}    {ea},D{dreg}", off2

    # ADD / ADDA
    if (w & 0xF000) == 0xD000:
        sz = (w >> 6) & 3
        dreg = (w >> 9) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        if sz == 3:
            sz2 = 2 if w & 0x0100 else 1
            ea, off2, _ = ea_str(mode, reg, size_bytes(sz2), off2)
            return f"ADDA{size_char(sz2)} {ea},A{dreg}", off2
        if w & 0x0100:
            ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
            return f"ADD{size_char(sz)}    D{dreg},{ea}", off2
        ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
        return f"ADD{size_char(sz)}    {ea},D{dreg}", off2

    # SUB / SUBA
    if (w & 0xF000) == 0x9000:
        sz = (w >> 6) & 3
        dreg = (w >> 9) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        if sz == 3:
            sz2 = 2 if w & 0x0100 else 1
            ea, off2, _ = ea_str(mode, reg, size_bytes(sz2), off2)
            return f"SUBA{size_char(sz2)} {ea},A{dreg}", off2
        if w & 0x0100:
            # SUBX if mode == 0 or mode == 4
            if mode == 0:
                return f"SUBX{size_char(sz)} D{reg},D{dreg}", off2
            if mode == 4:  # actually mode encoding 1 = -(Ay)
                return f"SUBX{size_char(sz)} -(A{reg}),-(A{dreg})", off2
            ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
            return f"SUB{size_char(sz)}    D{dreg},{ea}", off2
        ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
        return f"SUB{size_char(sz)}    {ea},D{dreg}", off2

    # AND
    if (w & 0xF000) == 0xC000:
        sz = (w >> 6) & 3
        dreg = (w >> 9) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        if sz == 3:
            # MULU/MULS
            if w & 0x0100:
                ea, off2, _ = ea_str(mode, reg, 2, off2)
                return f"MULS.W  {ea},D{dreg}", off2
            else:
                ea, off2, _ = ea_str(mode, reg, 2, off2)
                return f"MULU.W  {ea},D{dreg}", off2
        if w & 0x0100:
            # EXG or AND Dn,<ea>
            if mode == 0 and ((w >> 3) & 0x1F) in (0x08,):
                # EXG Dx,Dy
                return f"EXG     D{dreg},D{reg}", off2
            if mode == 1 and ((w >> 3) & 0x1F) in (0x09,):
                return f"EXG     A{dreg},A{reg}", off2
            ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
            return f"AND{size_char(sz)}    D{dreg},{ea}", off2
        ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
        return f"AND{size_char(sz)}    {ea},D{dreg}", off2

    # OR
    if (w & 0xF000) == 0x8000:
        sz = (w >> 6) & 3
        dreg = (w >> 9) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        if sz == 3:
            if w & 0x0100:
                ea, off2, _ = ea_str(mode, reg, 2, off2)
                return f"DIVS.W  {ea},D{dreg}", off2
            else:
                ea, off2, _ = ea_str(mode, reg, 2, off2)
                return f"DIVU.W  {ea},D{dreg}", off2
        if w & 0x0100:
            ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
            return f"OR{size_char(sz)}     D{dreg},{ea}", off2
        ea, off2, _ = ea_str(mode, reg, size_bytes(sz), off2)
        return f"OR{size_char(sz)}     {ea},D{dreg}", off2

    # ORI / ANDI / ADDI / SUBI / CMPI / EORI
    if top == 0 and not (w & 0x0100):
        op_type = (w >> 9) & 7
        ops = {0:'ORI', 1:'ANDI', 2:'SUBI', 3:'ADDI', 4:None, 5:'EORI', 6:'CMPI'}
        if op_type in ops and ops[op_type]:
            op = ops[op_type]
            sz = (w >> 6) & 3
            if sz == 3:
                return f"DC.W    ${w:04X}", off2  # invalid
            sb = size_bytes(sz)
            if sb == 1:
                imm = rw(off2) & 0xFF
                off2 += 2
                imm_str = f"#${imm:02X}"
            elif sb == 2:
                imm = rw(off2)
                off2 += 2
                imm_str = f"#${imm:04X}"
            else:
                imm = rl(off2)
                off2 += 4
                imm_str = f"#${imm:08X}"
            mode = (w >> 3) & 7
            reg = w & 7
            # Special: ORI/ANDI/EORI to CCR/SR
            if mode == 7 and reg == 4:
                if op == 'ORI': return f"ORI     {imm_str},CCR", off2
                if op == 'ANDI': return f"ANDI    {imm_str},CCR", off2
                if op == 'EORI': return f"EORI    {imm_str},CCR", off2
            ea, off2, _ = ea_str(mode, reg, sb, off2)
            return f"{op}{size_char(sz)}   {imm_str},{ea}", off2

    # ASL / ASR / LSL / LSR / ROL / ROR / ROXL / ROXR
    if (w & 0xF000) == 0xE000:
        sz = (w >> 6) & 3
        if sz == 3:
            # Memory shift (only word, single bit)
            mode = (w >> 3) & 7
            reg = w & 7
            direction = 'L' if w & 0x0100 else 'R'
            stype = (w >> 9) & 3
            stypes = ['AS','LS','ROX','RO']
            ea, off2, _ = ea_str(mode, reg, 2, off2)
            return f"{stypes[stype]}{direction}.W   {ea}", off2
        direction = 'L' if w & 0x0100 else 'R'
        stype = (w >> 3) & 3
        stypes = ['AS','LS','ROX','RO']
        if w & 0x0020:
            cnt = f"D{(w>>9)&7}"
        else:
            cnt = (w >> 9) & 7
            if cnt == 0: cnt = 8
            cnt = f"#{cnt}"
        return f"{stypes[stype]}{direction}{size_char(sz)}   {cnt},D{w&7}", off2

    # MOVE to/from SR, CCR, USP
    if (w & 0xFFC0) == 0x40C0:
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, 2, off2)
        return f"MOVE.W  SR,{ea}", off2
    if (w & 0xFFC0) == 0x44C0:
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, 2, off2)
        return f"MOVE.W  {ea},CCR", off2
    if (w & 0xFFC0) == 0x46C0:
        mode = (w >> 3) & 7
        reg = w & 7
        ea, off2, _ = ea_str(mode, reg, 2, off2)
        return f"MOVE.W  {ea},SR", off2
    if (w & 0xFFF0) == 0x4E60:
        if w & 8:
            return f"MOVE.L  USP,A{w&7}", off2
        return f"MOVE.L  A{w&7},USP", off2

    # EXG
    if (w & 0xF100) == 0xC100:
        opmode = (w >> 3) & 0x1F
        rx = (w >> 9) & 7
        ry = w & 7
        if opmode == 0x08:
            return f"EXG     D{rx},D{ry}", off2
        if opmode == 0x09:
            return f"EXG     A{rx},A{ry}", off2
        if opmode == 0x11:
            return f"EXG     D{rx},A{ry}", off2

    # Default: unknown
    return f"DC.W    ${w:04X}", off2


def disasm_range(start_off, end_off, label=None):
    """Disassemble a range of ROM offsets."""
    if label:
        print(f"\n{'='*70}")
        print(f"  {label}")
        print(f"{'='*70}")
    off = start_off
    while off < end_off:
        pc = ROM_BASE + off
        # Show raw bytes
        raw_off = off
        text, next_off = disasm_one(off)
        nbytes = next_off - off
        raw = ' '.join(f'{ROM[raw_off+i]:02X}{ROM[raw_off+i+1]:02X}' for i in range(0, nbytes, 2))
        print(f"  {pc:06X} [{off:05X}]  {raw:24s} {text}")
        off = next_off


# ============================================================
# TASK 1: Decode $FCA000-$FCA100
# ============================================================
print("=" * 70)
print("  TASK 1: ROM offset $A000-$A100 (PC=$FCA000-$FCA100)")
print("=" * 70)
disasm_range(0xA000, 0xA100)

# Also decode wider context $A100-$A260 to see sub-functions
print()
disasm_range(0xA100, 0xA260, "Extended context: $FCA100-$FCA260")

# ============================================================
# TASK 2: Search for references to $5C40
# ============================================================
print("\n" + "=" * 70)
print("  TASK 2: Search ROM for references to $5C40")
print("=" * 70)

# Search for byte patterns that encode $5C40
# Patterns to look for:
# - $00005C40 as a 32-bit value (MOVE.L #$5C40,...)
# - $5C40 as a 16-bit value
# - JSR $5C40 would be 4EB9 00005C40

target_long = bytes([0x00, 0x00, 0x5C, 0x40])
target_word = bytes([0x5C, 0x40])

print("\n--- Searching for 32-bit $00005C40 ---")
pos = 0
count = 0
while True:
    pos = ROM.find(target_long, pos)
    if pos < 0: break
    pc = ROM_BASE + pos
    # Show context: decode a few instructions before
    ctx_start = max(0, pos - 6)
    print(f"\n  Found at ROM offset ${pos:05X} (PC=${pc:06X}):")
    disasm_range(max(0, pos - 8), min(ROM_SIZE, pos + 8))
    pos += 1
    count += 1
    if count > 20: 
        print("  ... (truncated)")
        break

print(f"\n--- Searching for 16-bit $5C40 (word-aligned) ---")
# Only check word-aligned positions that aren't part of long matches
pos = 0
count = 0
while True:
    pos = ROM.find(target_word, pos)
    if pos < 0: break
    if pos % 2 == 0:  # word aligned
        # Skip if preceded by 00 00 (already found as long)
        if pos >= 2 and ROM[pos-2] == 0 and ROM[pos-1] == 0:
            pos += 2
            continue
        pc = ROM_BASE + pos
        print(f"\n  Found at ROM offset ${pos:05X} (PC=${pc:06X}):")
        disasm_range(max(0, pos - 6), min(ROM_SIZE, pos + 8))
        count += 1
    pos += 2
    if count > 15:
        print("  ... (truncated)")
        break

# Also search for JSR $5C40 = 4EB9 0000 5C40
jsr_pattern = bytes([0x4E, 0xB9, 0x00, 0x00, 0x5C, 0x40])
print(f"\n--- Searching for JSR $00005C40 ---")
pos = 0
while True:
    pos = ROM.find(jsr_pattern, pos)
    if pos < 0: break
    pc = ROM_BASE + pos
    print(f"  Found JSR $5C40 at ROM offset ${pos:05X} (PC=${pc:06X})")
    disasm_range(max(0, pos - 4), min(ROM_SIZE, pos + 10))
    pos += 1

# Search for MOVE.L #addr,$5C40 patterns or LEA $5C40,An
print(f"\n--- Searching for writes to absolute $5C40 ---")
# Pattern: xxxx 0000 5C40 where xxxx could be various MOVE instructions
# MOVE.L <something>,$00005C40 = 23xx xxxx 0000 5C40
found_writes = False
for off in range(0, ROM_SIZE - 6, 2):
    w = rw(off)
    # Check if $00005C40 appears as a destination absolute long
    # MOVE.L ea,$00005C40: top nibble = 2 (long), dst mode=7, dst reg=1
    if (w & 0xF1C0) == 0x23C0 or (w & 0xF1C0) == 0x21C0:  # MOVE.L to abs.L
        # Check where the absolute address would be
        src_mode = (w >> 3) & 7
        src_reg = w & 7
        # Compute source EA size
        if src_mode <= 1:
            addr_off = off + 2
        elif src_mode <= 4:
            addr_off = off + 2
        elif src_mode == 5:
            addr_off = off + 4
        elif src_mode == 6:
            addr_off = off + 4
        elif src_mode == 7:
            if src_reg == 0:
                addr_off = off + 4
            elif src_reg == 1:
                addr_off = off + 6
            elif src_reg == 4:
                addr_off = off + 6  # immediate long
            else:
                continue
        else:
            continue
        if addr_off + 4 <= ROM_SIZE:
            dest_addr = rl(addr_off)
            if dest_addr == 0x5C40:
                pc = ROM_BASE + off
                print(f"\n  MOVE to $5C40 at ROM offset ${off:05X} (PC=${pc:06X}):")
                disasm_range(max(0, off - 4), min(ROM_SIZE, off + 12))
                found_writes = True

if not found_writes:
    print("  No direct MOVE to $00005C40 found")


# ============================================================
# TASK 3: Analyze loop structure around BltBitMap call
# ============================================================
print("\n" + "=" * 70)
print("  TASK 3: Loop structure analysis around BltBitMap call")
print("=" * 70)

# The BltBitMap public entry is at $FC9C08-$FCA0EC per the repo memory
# Let's decode broader context
print("\n--- BltBitMap public entry ($FC9C08) and full function ---")
disasm_range(0x9C08, 0x9C80, "BltBitMap entry $FC9C08")
disasm_range(0x9C80, 0x9D00, "BltBitMap continued $FC9C80")
disasm_range(0x9D00, 0x9D80, "BltBitMap continued $FC9D00")
disasm_range(0x9D80, 0x9E00, "BltBitMap continued $FC9D80")
disasm_range(0x9E00, 0x9E80, "BltBitMap continued $FC9E00")
disasm_range(0x9E80, 0x9F00, "BltBitMap continued $FC9E80")
disasm_range(0x9F00, 0x9F80, "BltBitMap continued $FC9F00")
disasm_range(0x9F80, 0xA000, "BltBitMap continued $FC9F80")

# Now go further past $A0EC to find the end of the calling function
print()
disasm_range(0xA0EC, 0xA260, "Post-BltBitMap: sub-functions $FCA0EE+")

# Now look for the CALLER of BltBitMap at $FCA0BA
# The BSR/JSR that calls FC5E12 should be near A0BA
print("\n--- Detailed decode of BSR target area ---")
print("Checking what instruction at $FCA0BA actually is:")
off = 0xA0BA
for i in range(4):
    pc = ROM_BASE + off
    raw = ' '.join(f'{ROM[off+j]:02X}' for j in range(6))
    text, next_off = disasm_one(off)
    nbytes = next_off - off
    raw2 = ' '.join(f'{ROM[off+j]:02X}{ROM[off+j+1]:02X}' for j in range(0, nbytes, 2))
    print(f"  {pc:06X} [{off:05X}]  {raw2:24s} {text}")
    off = next_off

# Let's also search for all BSR/JSR calls to $FC5E12
print("\n--- All calls to BltBitMap_inner ($FC5E12) in ROM ---")
for off in range(0, ROM_SIZE - 4, 2):
    w = rw(off)
    pc = ROM_BASE + off
    # BSR with word displacement
    if (w & 0xFF00) == 0x6100:
        disp8 = w & 0xFF
        if disp8 == 0:
            disp = signed_word(rw(off + 2))
            target = pc + 2 + disp
        elif disp8 == 0xFF:
            disp = signed_long(rl(off + 2))
            target = pc + 2 + disp
        else:
            disp = signed_byte(disp8)
            target = pc + 2 + disp
        if target == 0xFC5E12:
            print(f"  BSR to $FC5E12 at PC=${pc:06X} (offset ${off:05X})")
    # JSR absolute long to $FC5E12
    if w == 0x4EB9 and off + 6 <= ROM_SIZE:
        addr = rl(off + 2)
        if addr == 0xFC5E12 or addr == 0x005E12:
            print(f"  JSR to $FC5E12 at PC=${pc:06X} (offset ${off:05X})")

# Also the public BltBitMap entry $FC9C08
print("\n--- All calls to BltBitMap public ($FC9C08) ---")
for off in range(0, ROM_SIZE - 4, 2):
    w = rw(off)
    pc = ROM_BASE + off
    if (w & 0xFF00) == 0x6100:
        disp8 = w & 0xFF
        if disp8 == 0:
            disp = signed_word(rw(off + 2))
            target = pc + 2 + disp
        else:
            disp = signed_byte(disp8)
            target = pc + 2 + disp
        if target == 0xFC9C08:
            print(f"  BSR to $FC9C08 at PC=${pc:06X} (offset ${off:05X})")
    if w == 0x4EB9 and off + 6 <= ROM_SIZE:
        addr = rl(off + 2)
        if addr == 0xFC9C08:
            print(f"  JSR to $FC9C08 at PC=${pc:06X} (offset ${off:05X})")

print("\nDone.")
