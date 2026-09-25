#!/usr/bin/env python3
"""Decode 68000 machine code from Kickstart 1.3 ROM for periodic redraw bug investigation."""

import struct
import sys

ROM_PATH = "/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom"
ROM_BASE = 0xFC0000

def load_rom():
    with open(ROM_PATH, "rb") as f:
        return f.read()

rom = load_rom()
print(f"ROM size: {len(rom)} bytes (0x{len(rom):X})")

def read_word(offset):
    if offset < 0 or offset + 2 > len(rom):
        return None
    return struct.unpack_from(">H", rom, offset)[0]

def read_long(offset):
    if offset < 0 or offset + 4 > len(rom):
        return None
    return struct.unpack_from(">I", rom, offset)[0]

def read_byte(offset):
    if offset < 0 or offset + 1 > len(rom):
        return None
    return rom[offset]

def signed_byte(v):
    return v - 256 if v >= 128 else v

def signed_word(v):
    return v - 0x10000 if v >= 0x8000 else v

def signed_long(v):
    return v - 0x100000000 if v >= 0x80000000 else v

# Condition code names
CC_NAMES = {
    0: "T",  1: "F",  2: "HI", 3: "LS",
    4: "CC", 5: "CS", 6: "NE", 7: "EQ",
    8: "VC", 9: "VS", 10: "PL", 11: "MI",
    12: "GE", 13: "LT", 14: "GT", 15: "LE"
}

SIZE_NAMES = {0: ".B", 1: ".W", 2: ".L"}

def ea_string(mode, reg, offset, size=1):
    """Decode effective address, return (string, new_offset, extra_words_consumed)"""
    if mode == 0:  # Dn
        return f"D{reg}", offset, 0
    elif mode == 1:  # An
        return f"A{reg}", offset, 0
    elif mode == 2:  # (An)
        return f"(A{reg})", offset, 0
    elif mode == 3:  # (An)+
        return f"(A{reg})+", offset, 0
    elif mode == 4:  # -(An)
        return f"-(A{reg})", offset, 0
    elif mode == 5:  # d16(An)
        w = read_word(offset)
        if w is None:
            return "???", offset, 0
        d16 = signed_word(w)
        return f"${d16&0xFFFF:04X}(A{reg})", offset + 2, 1
    elif mode == 6:  # d8(An,Xn)
        w = read_word(offset)
        if w is None:
            return "???", offset, 0
        xreg = (w >> 12) & 7
        xtype = "A" if w & 0x8000 else "D"
        xsize = ".L" if w & 0x0800 else ".W"
        d8 = signed_byte(w & 0xFF)
        return f"${d8&0xFF:02X}(A{reg},{xtype}{xreg}{xsize})", offset + 2, 1
    elif mode == 7:
        if reg == 0:  # abs.W
            w = read_word(offset)
            if w is None:
                return "???", offset, 0
            addr = signed_word(w) & 0xFFFFFF
            return f"${'%06X' % addr}.W", offset + 2, 1
        elif reg == 1:  # abs.L
            l = read_long(offset)
            if l is None:
                return "???", offset, 0
            return f"${l:08X}", offset + 4, 2
        elif reg == 2:  # d16(PC)
            w = read_word(offset)
            if w is None:
                return "???", offset, 0
            d16 = signed_word(w)
            pc = ROM_BASE + offset  # PC points to the extension word
            target = (pc + d16) & 0xFFFFFF
            return f"${target:06X}(PC)", offset + 2, 1
        elif reg == 3:  # d8(PC,Xn)
            w = read_word(offset)
            if w is None:
                return "???", offset, 0
            xreg = (w >> 12) & 7
            xtype = "A" if w & 0x8000 else "D"
            xsize = ".L" if w & 0x0800 else ".W"
            d8 = signed_byte(w & 0xFF)
            pc = ROM_BASE + offset
            return f"${d8&0xFF:02X}(PC,{xtype}{xreg}{xsize})", offset + 2, 1
        elif reg == 4:  # #imm
            if size == 0:  # byte
                w = read_word(offset)
                if w is None:
                    return "???", offset, 0
                return f"#${w&0xFF:02X}", offset + 2, 1
            elif size == 1:  # word
                w = read_word(offset)
                if w is None:
                    return "???", offset, 0
                return f"#${w:04X}", offset + 2, 1
            else:  # long
                l = read_long(offset)
                if l is None:
                    return "???", offset, 0
                return f"#${l:08X}", offset + 4, 2
    return f"???[m={mode},r={reg}]", offset, 0

def reglist_string(mask, reverse=False):
    """Convert register list mask to string like D0-D3/A0-A2"""
    regs = []
    if reverse:
        # For predecrement mode, bit 0 = A7, bit 15 = D0
        for i in range(16):
            if mask & (1 << i):
                if i < 8:
                    regs.append(f"A{7-i}")
                else:
                    regs.append(f"D{15-i}")
    else:
        for i in range(16):
            if mask & (1 << i):
                if i < 8:
                    regs.append(f"D{i}")
                else:
                    regs.append(f"A{i-8}")
    # Compress ranges
    parts = []
    i = 0
    while i < len(regs):
        start = regs[i]
        j = i
        while j + 1 < len(regs):
            # Check if consecutive
            r1 = regs[j]
            r2 = regs[j+1]
            if r1[0] == r2[0] and int(r2[1]) == int(r1[1]) + 1:
                j += 1
            else:
                break
        if j > i:
            parts.append(f"{start}-{regs[j]}")
        else:
            parts.append(start)
        i = j + 1
    return "/".join(parts) if parts else "???"

def decode_instruction(offset):
    """Decode a single 68000 instruction at the given ROM offset.
    Returns (mnemonic_string, next_offset)"""
    pc_addr = ROM_BASE + offset
    opcode = read_word(offset)
    if opcode is None:
        return f"  ??? (end of ROM)", offset + 2
    
    off = offset + 2  # point past the opcode word
    
    # Group by top nibble
    top = (opcode >> 12) & 0xF
    
    # --- MOVEM ---
    if (opcode & 0xFB80) == 0x4880:
        direction = (opcode >> 10) & 1  # 0=reg-to-mem, 1=mem-to-reg
        size = ".L" if opcode & 0x0040 else ".W"
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        mask = read_word(off)
        if mask is None:
            return f"  MOVEM{size} ???", off
        off += 2
        ea, off, _ = ea_string(mode, reg, off, 2 if size == ".L" else 1)
        is_predec = (mode == 4)
        rlist = reglist_string(mask, reverse=is_predec)
        if direction == 0:
            return f"  MOVEM{size} {rlist},{ea}", off
        else:
            return f"  MOVEM{size} {ea},{rlist}", off
    
    # --- LINK ---
    if (opcode & 0xFFF8) == 0x4E50:
        reg = opcode & 7
        disp = read_word(off)
        if disp is None:
            return f"  LINK A{reg},???", off
        d16 = signed_word(disp)
        off += 2
        return f"  LINK A{reg},#${disp:04X}  ; {d16}", off
    
    # --- UNLK ---
    if (opcode & 0xFFF8) == 0x4E58:
        reg = opcode & 7
        return f"  UNLK A{reg}", off
    
    # --- RTS ---
    if opcode == 0x4E75:
        return f"  RTS", off
    
    # --- RTE ---
    if opcode == 0x4E73:
        return f"  RTE", off
    
    # --- NOP ---
    if opcode == 0x4E71:
        return f"  NOP", off
    
    # --- TRAP ---
    if (opcode & 0xFFF0) == 0x4E40:
        vector = opcode & 0xF
        return f"  TRAP #{vector}", off
    
    # --- JSR ---
    if (opcode & 0xFFC0) == 0x4E80:
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        ea, off, _ = ea_string(mode, reg, off, 2)
        return f"  JSR {ea}", off
    
    # --- JMP ---
    if (opcode & 0xFFC0) == 0x4EC0:
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        ea, off, _ = ea_string(mode, reg, off, 2)
        return f"  JMP {ea}", off
    
    # --- LEA ---
    if (opcode & 0xF1C0) == 0x41C0:
        areg = (opcode >> 9) & 7
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        ea, off, _ = ea_string(mode, reg, off, 2)
        return f"  LEA {ea},A{areg}", off
    
    # --- PEA ---
    if (opcode & 0xFFC0) == 0x4840:
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        ea, off, _ = ea_string(mode, reg, off, 2)
        return f"  PEA {ea}", off
    
    # --- MOVE to/from SR, CCR ---
    if (opcode & 0xFFC0) == 0x40C0:  # MOVE SR,<ea>
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        ea, off, _ = ea_string(mode, reg, off, 1)
        return f"  MOVE SR,{ea}", off
    if (opcode & 0xFFC0) == 0x44C0:  # MOVE <ea>,CCR
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        ea, off, _ = ea_string(mode, reg, off, 1)
        return f"  MOVE {ea},CCR", off
    if (opcode & 0xFFC0) == 0x46C0:  # MOVE <ea>,SR
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        ea, off, _ = ea_string(mode, reg, off, 1)
        return f"  MOVE {ea},SR", off
    
    # --- MOVE USP ---
    if (opcode & 0xFFF0) == 0x4E60:
        direction = (opcode >> 3) & 1
        reg = opcode & 7
        if direction:
            return f"  MOVE USP,A{reg}", off
        else:
            return f"  MOVE A{reg},USP", off
    
    # --- EXG ---
    if (opcode & 0xF100) == 0xC100 and (opcode & 0x00C0) in (0x0040, 0x0048, 0x0088):
        rx = (opcode >> 9) & 7
        ry = opcode & 7
        opmode = (opcode >> 3) & 0x1F
        if opmode == 0x08:
            return f"  EXG D{rx},D{ry}", off
        elif opmode == 0x09:
            return f"  EXG A{rx},A{ry}", off
        elif opmode == 0x11:
            return f"  EXG D{rx},A{ry}", off
    
    # --- Bcc / BRA / BSR ---
    if top == 0x6:
        cc = (opcode >> 8) & 0xF
        disp8 = opcode & 0xFF
        if disp8 == 0:
            # 16-bit displacement
            w = read_word(off)
            if w is None:
                return f"  B{CC_NAMES[cc]} ???", off
            d16 = signed_word(w)
            off += 2
            target = (pc_addr + 2 + d16) & 0xFFFFFF
        elif disp8 == 0xFF:
            # 32-bit displacement (68020+)
            l = read_long(off)
            if l is None:
                return f"  B{CC_NAMES[cc]} ???", off
            d32 = signed_long(l)
            off += 4
            target = (pc_addr + 2 + d32) & 0xFFFFFF
        else:
            d8 = signed_byte(disp8)
            target = (pc_addr + 2 + d8) & 0xFFFFFF
        
        if cc == 0:
            mnem = "BRA"
        elif cc == 1:
            mnem = "BSR"
        else:
            mnem = f"B{CC_NAMES[cc]}"
        return f"  {mnem} ${target:06X}", off
    
    # --- DBcc ---
    if (opcode & 0xF0F8) == 0x50C8:
        cc = (opcode >> 8) & 0xF
        reg = opcode & 7
        w = read_word(off)
        if w is None:
            return f"  DB{CC_NAMES[cc]} D{reg},???", off
        d16 = signed_word(w)
        off += 2
        target = (pc_addr + 2 + d16) & 0xFFFFFF
        return f"  DB{CC_NAMES[cc]} D{reg},${target:06X}", off
    
    # --- Scc ---
    if (opcode & 0xF0C0) == 0x50C0 and ((opcode >> 3) & 7) != 1:
        cc = (opcode >> 8) & 0xF
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        ea, off, _ = ea_string(mode, reg, off, 0)
        return f"  S{CC_NAMES[cc]} {ea}", off
    
    # --- ADDQ/SUBQ ---
    if top == 0x5 and (opcode & 0x00C0) != 0x00C0:
        data = (opcode >> 9) & 7
        if data == 0:
            data = 8
        size_bits = (opcode >> 6) & 3
        sz = SIZE_NAMES.get(size_bits, "")
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        is_sub = (opcode >> 8) & 1
        ea, off, _ = ea_string(mode, reg, off, size_bits)
        mnem = "SUBQ" if is_sub else "ADDQ"
        return f"  {mnem}{sz} #{data},{ea}", off
    
    # --- MOVEQ ---
    if top == 0x7 and (opcode & 0x0100) == 0:
        dreg = (opcode >> 9) & 7
        data = signed_byte(opcode & 0xFF)
        return f"  MOVEQ #${opcode & 0xFF:02X},D{dreg}  ; {data}", off
    
    # --- MOVE ---
    if top in (0x1, 0x2, 0x3):
        size_map = {0x1: 0, 0x2: 2, 0x3: 1}  # byte, long, word
        sz_bits = size_map[top]
        sz = SIZE_NAMES[sz_bits]
        
        src_mode = (opcode >> 3) & 7
        src_reg = opcode & 7
        dst_reg = (opcode >> 9) & 7
        dst_mode = (opcode >> 6) & 7
        
        # Special: MOVEA
        if dst_mode == 1:
            src_ea, off, _ = ea_string(src_mode, src_reg, off, sz_bits)
            dst_ea = f"A{dst_reg}"
            return f"  MOVEA{sz} {src_ea},{dst_ea}", off
        
        src_ea, off, _ = ea_string(src_mode, src_reg, off, sz_bits)
        dst_ea, off, _ = ea_string(dst_mode, dst_reg, off, sz_bits)
        return f"  MOVE{sz} {src_ea},{dst_ea}", off
    
    # --- ADD/ADDA ---
    if top == 0xD:
        dreg = (opcode >> 9) & 7
        opmode = (opcode >> 6) & 7
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        
        if opmode == 3:  # ADDA.W
            ea, off, _ = ea_string(mode, reg, off, 1)
            return f"  ADDA.W {ea},A{dreg}", off
        elif opmode == 7:  # ADDA.L
            ea, off, _ = ea_string(mode, reg, off, 2)
            return f"  ADDA.L {ea},A{dreg}", off
        elif opmode in (0, 1, 2):  # ADD <ea>,Dn
            sz = SIZE_NAMES[opmode]
            ea, off, _ = ea_string(mode, reg, off, opmode)
            return f"  ADD{sz} {ea},D{dreg}", off
        elif opmode in (4, 5, 6):  # ADD Dn,<ea>
            sz_bits = opmode - 4
            sz = SIZE_NAMES[sz_bits]
            ea, off, _ = ea_string(mode, reg, off, sz_bits)
            return f"  ADD{sz} D{dreg},{ea}", off
    
    # --- SUB/SUBA ---
    if top == 0x9:
        dreg = (opcode >> 9) & 7
        opmode = (opcode >> 6) & 7
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        
        if opmode == 3:  # SUBA.W
            ea, off, _ = ea_string(mode, reg, off, 1)
            return f"  SUBA.W {ea},A{dreg}", off
        elif opmode == 7:  # SUBA.L
            ea, off, _ = ea_string(mode, reg, off, 2)
            return f"  SUBA.L {ea},A{dreg}", off
        elif opmode in (0, 1, 2):  # SUB <ea>,Dn
            sz = SIZE_NAMES[opmode]
            ea, off, _ = ea_string(mode, reg, off, opmode)
            return f"  SUB{sz} {ea},D{dreg}", off
        elif opmode in (4, 5, 6):  # SUB Dn,<ea>
            sz_bits = opmode - 4
            sz = SIZE_NAMES[sz_bits]
            ea, off, _ = ea_string(mode, reg, off, sz_bits)
            return f"  SUB{sz} D{dreg},{ea}", off
    
    # --- CMP/CMPA ---
    if top == 0xB:
        dreg = (opcode >> 9) & 7
        opmode = (opcode >> 6) & 7
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        
        if opmode == 3:  # CMPA.W
            ea, off, _ = ea_string(mode, reg, off, 1)
            return f"  CMPA.W {ea},A{dreg}", off
        elif opmode == 7:  # CMPA.L
            ea, off, _ = ea_string(mode, reg, off, 2)
            return f"  CMPA.L {ea},A{dreg}", off
        elif opmode in (0, 1, 2):  # CMP <ea>,Dn
            sz = SIZE_NAMES[opmode]
            ea, off, _ = ea_string(mode, reg, off, opmode)
            return f"  CMP{sz} {ea},D{dreg}", off
    
    # --- AND ---
    if top == 0xC:
        dreg = (opcode >> 9) & 7
        opmode = (opcode >> 6) & 7
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        
        if opmode in (0, 1, 2):  # AND <ea>,Dn
            sz = SIZE_NAMES[opmode]
            ea, off, _ = ea_string(mode, reg, off, opmode)
            return f"  AND{sz} {ea},D{dreg}", off
        elif opmode in (4, 5, 6):  # AND Dn,<ea>
            sz_bits = opmode - 4
            sz = SIZE_NAMES[sz_bits]
            ea, off, _ = ea_string(mode, reg, off, sz_bits)
            return f"  AND{sz} D{dreg},{ea}", off
    
    # --- OR ---
    if top == 0x8:
        dreg = (opcode >> 9) & 7
        opmode = (opcode >> 6) & 7
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        
        # DIVU / DIVS
        if opmode == 3:
            ea, off, _ = ea_string(mode, reg, off, 1)
            return f"  DIVU.W {ea},D{dreg}", off
        if opmode == 7:
            ea, off, _ = ea_string(mode, reg, off, 1)
            return f"  DIVS.W {ea},D{dreg}", off
        
        if opmode in (0, 1, 2):  # OR <ea>,Dn
            sz = SIZE_NAMES[opmode]
            ea, off, _ = ea_string(mode, reg, off, opmode)
            return f"  OR{sz} {ea},D{dreg}", off
        elif opmode in (4, 5, 6):  # OR Dn,<ea>
            sz_bits = opmode - 4
            sz = SIZE_NAMES[sz_bits]
            ea, off, _ = ea_string(mode, reg, off, sz_bits)
            return f"  OR{sz} D{dreg},{ea}", off
    
    # --- EOR ---
    if top == 0xB:
        dreg = (opcode >> 9) & 7
        opmode = (opcode >> 6) & 7
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        if opmode in (4, 5, 6):
            sz_bits = opmode - 4
            sz = SIZE_NAMES[sz_bits]
            ea, off, _ = ea_string(mode, reg, off, sz_bits)
            return f"  EOR{sz} D{dreg},{ea}", off
    
    # --- MULU/MULS ---
    if top == 0xC:
        dreg = (opcode >> 9) & 7
        opmode = (opcode >> 6) & 7
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        if opmode == 3:
            ea, off, _ = ea_string(mode, reg, off, 1)
            return f"  MULU.W {ea},D{dreg}", off
        if opmode == 7:
            ea, off, _ = ea_string(mode, reg, off, 1)
            return f"  MULS.W {ea},D{dreg}", off
    
    # --- BTST/BCHG/BCLR/BSET (immediate) ---
    if (opcode & 0xFF00) == 0x0800:
        bitop = (opcode >> 6) & 3
        ops = ["BTST", "BCHG", "BCLR", "BSET"]
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        bit_num = read_word(off)
        if bit_num is None:
            return f"  {ops[bitop]} #?,???", off
        off += 2
        ea, off, _ = ea_string(mode, reg, off, 0)
        return f"  {ops[bitop]} #{bit_num},{ea}", off
    
    # --- BTST/BCHG/BCLR/BSET (register) ---
    if top == 0x0 and (opcode & 0x0100):
        bitop = (opcode >> 6) & 3
        ops = ["BTST", "BCHG", "BCLR", "BSET"]
        dreg = (opcode >> 9) & 7
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        ea, off, _ = ea_string(mode, reg, off, 0)
        return f"  {ops[bitop]} D{dreg},{ea}", off
    
    # --- ORI/ANDI/SUBI/ADDI/EORI/CMPI ---
    if top == 0x0 and (opcode & 0x0100) == 0:
        op_type = (opcode >> 9) & 7
        op_names = {0: "ORI", 1: "ANDI", 2: "SUBI", 3: "ADDI", 4: "???", 5: "EORI", 6: "CMPI"}
        size_bits = (opcode >> 6) & 3
        mode = (opcode >> 3) & 7
        reg = opcode & 7
        
        if op_type in op_names and size_bits < 3:
            sz = SIZE_NAMES[size_bits]
            # Read immediate
            if size_bits == 0:  # byte
                imm = read_word(off)
                if imm is None:
                    return f"  {op_names[op_type]}{sz} #?,???", off
                imm &= 0xFF
                off += 2
                imm_str = f"#${imm:02X}"
            elif size_bits == 1:  # word
                imm = read_word(off)
                if imm is None:
                    return f"  {op_names[op_type]}{sz} #?,???", off
                off += 2
                imm_str = f"#${imm:04X}"
            else:  # long
                imm = read_long(off)
                if imm is None:
                    return f"  {op_names[op_type]}{sz} #?,???", off
                off += 4
                imm_str = f"#${imm:08X}"
            
            # Special: to CCR/SR
            if mode == 7 and reg == 4:
                if op_type in (0, 1, 5):  # ORI/ANDI/EORI to CCR/SR
                    if size_bits == 0:
                        return f"  {op_names[op_type]} {imm_str},CCR", off
                    elif size_bits == 1:
                        return f"  {op_names[op_type]} {imm_str},SR", off
            
            ea, off, _ = ea_string(mode, reg, off, size_bits)
            return f"  {op_names[op_type]}{sz} {imm_str},{ea}", off
    
    # --- CLR ---
    if (opcode & 0xFF00) == 0x4200:
        size_bits = (opcode >> 6) & 3
        if size_bits < 3:
            sz = SIZE_NAMES[size_bits]
            mode = (opcode >> 3) & 7
            reg = opcode & 7
            ea, off, _ = ea_string(mode, reg, off, size_bits)
            return f"  CLR{sz} {ea}", off
    
    # --- NEG ---
    if (opcode & 0xFF00) == 0x4400:
        size_bits = (opcode >> 6) & 3
        if size_bits < 3:
            sz = SIZE_NAMES[size_bits]
            mode = (opcode >> 3) & 7
            reg = opcode & 7
            ea, off, _ = ea_string(mode, reg, off, size_bits)
            return f"  NEG{sz} {ea}", off
    
    # --- NEGX ---
    if (opcode & 0xFF00) == 0x4000:
        size_bits = (opcode >> 6) & 3
        if size_bits < 3:
            sz = SIZE_NAMES[size_bits]
            mode = (opcode >> 3) & 7
            reg = opcode & 7
            ea, off, _ = ea_string(mode, reg, off, size_bits)
            return f"  NEGX{sz} {ea}", off
    
    # --- NOT ---
    if (opcode & 0xFF00) == 0x4600:
        size_bits = (opcode >> 6) & 3
        if size_bits < 3:
            sz = SIZE_NAMES[size_bits]
            mode = (opcode >> 3) & 7
            reg = opcode & 7
            ea, off, _ = ea_string(mode, reg, off, size_bits)
            return f"  NOT{sz} {ea}", off
    
    # --- TST ---
    if (opcode & 0xFF00) == 0x4A00:
        size_bits = (opcode >> 6) & 3
        if size_bits < 3:
            sz = SIZE_NAMES[size_bits]
            mode = (opcode >> 3) & 7
            reg = opcode & 7
            ea, off, _ = ea_string(mode, reg, off, size_bits)
            return f"  TST{sz} {ea}", off
    
    # --- EXT ---
    if (opcode & 0xFEB8) == 0x4880:
        sz = ".W" if (opcode & 0x0040) == 0 else ".L"
        reg = opcode & 7
        return f"  EXT{sz} D{reg}", off
    
    # --- SWAP ---
    if (opcode & 0xFFF8) == 0x4840:
        reg = opcode & 7
        return f"  SWAP D{reg}", off
    
    # --- ASL/ASR/LSL/LSR/ROL/ROR/ROXL/ROXR ---
    if top == 0xE:
        if (opcode & 0x00C0) == 0x00C0:
            # Memory shift/rotate
            type_bits = (opcode >> 9) & 3
            dr = (opcode >> 8) & 1
            types = ["ASR", "LSR", "ROXR", "ROR"] if dr == 0 else ["ASL", "LSL", "ROXL", "ROL"]
            mnem = types[type_bits]
            mode = (opcode >> 3) & 7
            reg = opcode & 7
            ea, off, _ = ea_string(mode, reg, off, 1)
            return f"  {mnem} {ea}", off
        else:
            # Register shift/rotate
            count_reg = (opcode >> 9) & 7
            size_bits = (opcode >> 6) & 3
            sz = SIZE_NAMES.get(size_bits, "")
            ir = (opcode >> 5) & 1  # 0=immediate count, 1=register count
            dr = (opcode >> 8) & 1
            type_bits = (opcode >> 3) & 3
            
            types_r = ["ASR", "LSR", "ROXR", "ROR"]
            types_l = ["ASL", "LSL", "ROXL", "ROL"]
            mnem = types_l[type_bits] if dr else types_r[type_bits]
            
            dreg = opcode & 7
            if ir:
                return f"  {mnem}{sz} D{count_reg},D{dreg}", off
            else:
                cnt = count_reg if count_reg != 0 else 8
                return f"  {mnem}{sz} #{cnt},D{dreg}", off
    
    # --- BTST #n (already handled above but catch edge cases) ---
    
    # --- ADDA via ADDQ already covered ---
    
    # Fallback: unknown
    return f"  DC.W ${opcode:04X}  ; ???", off


def decode_region(start_offset, end_offset, label=""):
    """Decode a region of ROM and print the assembly."""
    if label:
        print(f"\n{'='*70}")
        print(f"  {label}")
        print(f"{'='*70}")
    
    off = start_offset
    while off < end_offset:
        addr = ROM_BASE + off
        # Read raw bytes for hex dump
        raw_words = []
        save_off = off
        
        line, new_off = decode_instruction(off)
        
        # Collect raw bytes
        nbytes = new_off - off
        hex_str = ""
        for i in range(0, min(nbytes, 10), 2):
            w = read_word(off + i)
            if w is not None:
                hex_str += f"{w:04X} "
        
        print(f"${addr:06X}  {hex_str:<24s}{line}")
        off = new_off
        
        # Stop if RTS
        if "RTS" in line:
            break

# Correct offset calculation: offset = address - $FC0000
# $FCA0C0 - $FC0000 = $A0C0, $FC9C08 - $FC0000 = $9C08, etc.

# =====================================================================
# TASK 1: BltBitMap_public after inner loop ($FCA0C0)
# =====================================================================
print("\n" + "#"*70)
print("# TASK 1: BltBitMap_public epilogue at $FCA0C0")
print("#"*70)
decode_region(0xA0C0, 0xA0F0, "BltBitMap_public epilogue ($FCA0C0-$FCA0EC)")

# =====================================================================
# TASK 2: BltBitMap_public entry at $FC9C08
# =====================================================================  
print("\n" + "#"*70)
print("# TASK 2: BltBitMap_public entry at $FC9C08")
print("#"*70)
off = 0x9C08
end = 0x9C60
print(f"\n{'='*70}")
print(f"  BltBitMap_public entry ($FC9C08)")
print(f"{'='*70}")
while off < end:
    addr = ROM_BASE + off
    line, new_off = decode_instruction(off)
    nbytes = new_off - off
    hex_str = ""
    for i in range(0, min(nbytes, 10), 2):
        w = read_word(off + i)
        if w is not None:
            hex_str += f"{w:04X} "
    print(f"${addr:06X}  {hex_str:<24s}{line}")
    off = new_off

# =====================================================================
# TASK 3: ClipRect loop at $FCEF42
# =====================================================================
print("\n" + "#"*70)
print("# TASK 3: ClipRect loop at $FCEF42")
print("#"*70)
off = 0xEF42
end = 0xEF90
print(f"\n{'='*70}")
print(f"  ClipRect loop ($FCEF42-$FCEF80)")
print(f"{'='*70}")
while off < end:
    addr = ROM_BASE + off
    line, new_off = decode_instruction(off)
    nbytes = new_off - off
    hex_str = ""
    for i in range(0, min(nbytes, 10), 2):
        w = read_word(off + i)
        if w is not None:
            hex_str += f"{w:04X} "
    print(f"${addr:06X}  {hex_str:<24s}{line}")
    off = new_off

# =====================================================================
# TASK 4: Code before the ClipRect loop - find entry point
# =====================================================================
print("\n" + "#"*70)
print("# TASK 4: Code before ClipRect loop ($FCEF00)")  
print("#"*70)
off = 0xEF00
end = 0xEF50
print(f"\n{'='*70}")
print(f"  Code before ClipRect loop ($FCEF00-$FCEF50)")
print(f"{'='*70}")
while off < end:
    addr = ROM_BASE + off
    line, new_off = decode_instruction(off)
    nbytes = new_off - off
    hex_str = ""
    for i in range(0, min(nbytes, 10), 2):
        w = read_word(off + i)
        if w is not None:
            hex_str += f"{w:04X} "
    print(f"${addr:06X}  {hex_str:<24s}{line}")
    off = new_off

# Now look further back for the function entry
print(f"\n{'='*70}")
print(f"  Looking further back ($FCEE80-$FCEF10)")
print(f"{'='*70}")
off = 0xEE80
end = 0xEF10
while off < end:
    addr = ROM_BASE + off
    line, new_off = decode_instruction(off)
    nbytes = new_off - off
    hex_str = ""
    for i in range(0, min(nbytes, 10), 2):
        w = read_word(off + i)
        if w is not None:
            hex_str += f"{w:04X} "
    print(f"${addr:06X}  {hex_str:<24s}{line}")
    off = new_off

# =====================================================================
# TASK 5: Search for damage-related patterns
# =====================================================================
print("\n" + "#"*70)
print("# TASK 5: Searching for damage/refresh patterns")
print("#"*70)

# Search for specific instruction patterns in the ROM
# Look for JSR/BSR to known library vector offsets
# BeginRefresh = -$3A (LVO), EndRefresh = -$40 (LVO)
# Or look for references to Layer->DamageList, Layer->Flags

# First, let's find all JSR/BSR to addresses near the ClipRect loop
print("\n--- Searching for calls TO $FCEF42 area ---")
# Look for BSR.W or JSR with target near FCEF42
for scan_off in range(0x0000, 0x40000, 2):
    w = read_word(scan_off)
    if w is None:
        continue
    
    # BSR.W (0x6100)
    if w == 0x6100:
        disp = read_word(scan_off + 2)
        if disp is not None:
            target = (ROM_BASE + scan_off + 2 + signed_word(disp)) & 0xFFFFFF
            if 0xFCEE00 <= target <= 0xFCF000:
                print(f"  BSR.W at ${ROM_BASE + scan_off:06X} -> ${target:06X}")
    
    # BSR.B
    if (w & 0xFF00) == 0x6100 and (w & 0xFF) != 0:
        d8 = signed_byte(w & 0xFF)
        target = (ROM_BASE + scan_off + 2 + d8) & 0xFFFFFF
        if 0xFCEE00 <= target <= 0xFCF000:
            print(f"  BSR.B at ${ROM_BASE + scan_off:06X} -> ${target:06X}")
    
    # JSR abs.L (4EB9 xxxx xxxx)
    if w == 0x4EB9:
        addr = read_long(scan_off + 2)
        if addr is not None and 0xFCEE00 <= addr <= 0xFCF000:
            print(f"  JSR at ${ROM_BASE + scan_off:06X} -> ${addr:06X}")

# Search for calls to areas near $FCEEA0 (potential function entry)
print("\n--- Searching for calls TO $FCEEA0-$FCEE00 (function entry area) ---")
for scan_off in range(0x0000, 0x40000, 2):
    w = read_word(scan_off)
    if w is None:
        continue
    
    if w == 0x6100:
        disp = read_word(scan_off + 2)
        if disp is not None:
            target = (ROM_BASE + scan_off + 2 + signed_word(disp)) & 0xFFFFFF
            if 0xFCEE00 <= target <= 0xFCEEA0:
                print(f"  BSR.W at ${ROM_BASE + scan_off:06X} -> ${target:06X}")
    
    if (w & 0xFF00) == 0x6100 and (w & 0xFF) != 0:
        d8 = signed_byte(w & 0xFF)
        target = (ROM_BASE + scan_off + 2 + d8) & 0xFFFFFF
        if 0xFCEE00 <= target <= 0xFCEEA0:
            print(f"  BSR.B at ${ROM_BASE + scan_off:06X} -> ${target:06X}")
    
    if w == 0x4EB9:
        addr = read_long(scan_off + 2)
        if addr is not None and 0xFCEE00 <= addr <= 0xFCEEA0:
            print(f"  JSR at ${ROM_BASE + scan_off:06X} -> ${addr:06X}")

# Search for layer damage flag patterns
# In Amiga, Layer->Flags is at offset 0x18 in the Layer struct
# LAYERREFRESH = 0x10, LAYERIREFRESH = 0x20, LAYERIREFRESH2 = 0x40
# Look for BTST/BSET/BCLR with these bit patterns on struct offsets
print("\n--- Searching for layer damage flag manipulation ---")
print("Looking for BTST/BSET/BCLR #4/#5 (LAYERREFRESH/LAYERIREFRESH) ---")
for scan_off in range(0x0000, 0x40000, 2):
    w = read_word(scan_off)
    if w is None:
        continue
    # BTST #n,d16(An) = 0800 + EA bits, followed by bit#, then d16
    # BSET #n,d16(An) = 08C0 + EA bits
    # BCLR #n,d16(An) = 0880 + EA bits
    
    # Simpler: look for BSET #4 / BSET #5 pattern (08xx 0004/0005)
    if w in (0x08E8, 0x08A8, 0x0868, 0x0828):  # BSET/BCLR/BCHG/BTST d16(An)
        bit_num = read_word(scan_off + 2)
        if bit_num is not None and bit_num in (4, 5, 6):
            reg = w & 7
            disp = read_word(scan_off + 4)
            if disp is not None:
                d16 = signed_word(disp)
                ops = {0x0828: "BTST", 0x0868: "BCHG", 0x08A8: "BCLR", 0x08E8: "BSET"}
                print(f"  ${ROM_BASE + scan_off:06X}: {ops.get(w & 0xFFF8, '???')} #{bit_num},${disp:04X}(A{reg})")

# Search for references to VBLANK interrupt handling
# On Amiga, the VBLANK server list is at IntVects[5] in ExecBase
# Look for INTENA/INTREQ writes related to VBLANK (bit 5)
print("\n--- Searching for VBLANK-related patterns ---")
print("Looking for INTENA/INTREQ (DFF09A/DFF09C) writes with bit 5 ---")
for scan_off in range(0x0000, 0x40000, 2):
    w = read_word(scan_off)
    if w is None:
        continue
    # MOVE.W #xxxx,$DFF09A pattern
    if w == 0x33FC:  # MOVE.W #imm,abs.W or abs.L
        imm = read_word(scan_off + 2)
        addr_w = read_word(scan_off + 4)
        if imm is not None and addr_w is not None:
            # Check if writing to INTENA ($DFF09A) or INTREQ ($DFF09C)
            addr_full = signed_word(addr_w) & 0xFFFFFF
            if addr_full in (0xDFF09A, 0xDFF09C):
                if imm & 0x0020:
                    print(f"  ${ROM_BASE + scan_off:06X}: MOVE.W #${imm:04X},${addr_full:06X} (VBLANK bit set)")

# Look for Intuition's VBLANK handler - search for code that does
# BeginRefresh/EndRefresh-like operations
# BeginRefresh LVO is -$144 in layers.library 
# EndRefresh LVO is -$14A
# Look for JSR -$144(A6) or JSR -$14A(A6)
print("\n--- Searching for BeginRefresh/EndRefresh calls (LVO -$144/-$14A) ---")
for scan_off in range(0x0000, 0x40000, 2):
    w = read_word(scan_off)
    if w is None:
        continue
    # JSR d16(A6) = 4EAE xxxx
    if w == 0x4EAE:
        disp = read_word(scan_off + 2)
        if disp is not None:
            d16 = signed_word(disp)
            if d16 == -0x144:
                print(f"  ${ROM_BASE + scan_off:06X}: JSR -$144(A6)  ; BeginRefresh")
            elif d16 == -0x14A:
                print(f"  ${ROM_BASE + scan_off:06X}: JSR -$14A(A6)  ; EndRefresh")

# Search more broadly for layer operations
print("\n--- Searching for key Layers library calls ---")
layers_lvos = {
    -0x1E: "InitLayers",
    -0x24: "CreateUpfrontLayer", 
    -0x2A: "CreateBehindLayer",
    -0x30: "UpfrontLayer",
    -0x36: "BehindLayer",
    -0x3C: "MoveLayer",
    -0x42: "SizeLayer",
    -0x48: "ScrollLayer",
    -0x4E: "BeginUpdate",
    -0x54: "EndUpdate",
    -0x5A: "DeleteLayer",
    -0x66: "LockLayer",
    -0x6C: "UnlockLayer",
    -0x72: "LockLayers",
    -0x78: "UnlockLayers",
    -0x7E: "LockLayerInfo",
    -0x84: "SwapBitsRastPortClipRect",
    -0x8A: "WhichLayer",
    -0x90: "UnlockLayerInfo",
    -0x96: "NewLayerInfo",
    -0x9C: "DisposeLayerInfo",
    -0xA2: "FattenLayerInfo",
    -0xA8: "ThinLayerInfo",
    -0xAE: "MoveLayerInFrontOf",
    -0xB4: "InstallClipRegion",
}
for scan_off in range(0x0000, 0x40000, 2):
    w = read_word(scan_off)
    if w is None:
        continue
    if w == 0x4EAE:
        disp = read_word(scan_off + 2)
        if disp is not None:
            d16 = signed_word(disp)
            if d16 in layers_lvos:
                print(f"  ${ROM_BASE + scan_off:06X}: JSR ${disp:04X}(A6)  ; {layers_lvos[d16]}")

# Search for BeginUpdate/EndUpdate specifically, as these control refresh
print("\n--- Specifically: BeginUpdate (-$4E) and EndUpdate (-$54) calls ---")
for scan_off in range(0x0000, 0x40000, 2):
    w = read_word(scan_off)
    if w is None:
        continue
    if w == 0x4EAE:
        disp = read_word(scan_off + 2)
        if disp is not None:
            d16 = signed_word(disp)
            if d16 == -0x4E:
                # Decode surrounding context
                print(f"\n  BeginUpdate at ${ROM_BASE + scan_off:06X}:")
                # Show a few instructions before and after
                ctx_start = max(scan_off - 16, 0)
                ctx_end = min(scan_off + 20, len(rom))
                co = ctx_start
                while co < ctx_end:
                    addr = ROM_BASE + co
                    line, new_co = decode_instruction(co)
                    nbytes = new_co - co
                    hex_str = ""
                    for i in range(0, min(nbytes, 10), 2):
                        ww = read_word(co + i)
                        if ww is not None:
                            hex_str += f"{ww:04X} "
                    marker = " <<<" if co == scan_off else ""
                    print(f"    ${addr:06X}  {hex_str:<24s}{line}{marker}")
                    co = new_co
            elif d16 == -0x54:
                print(f"\n  EndUpdate at ${ROM_BASE + scan_off:06X}:")
                ctx_start = max(scan_off - 16, 0)
                ctx_end = min(scan_off + 20, len(rom))
                co = ctx_start
                while co < ctx_end:
                    addr = ROM_BASE + co
                    line, new_co = decode_instruction(co)
                    nbytes = new_co - co
                    hex_str = ""
                    for i in range(0, min(nbytes, 10), 2):
                        ww = read_word(co + i)
                        if ww is not None:
                            hex_str += f"{ww:04X} "
                    marker = " <<<" if co == scan_off else ""
                    print(f"    ${addr:06X}  {hex_str:<24s}{line}{marker}")
                    co = new_co

# Look for the actual VBLANK interrupt server chain in Intuition
# Intuition adds a VBLANK server. Let's find AddIntServer(-$A8) calls
print("\n--- Searching for AddIntServer calls (Exec LVO -$A8) ---")
for scan_off in range(0x0000, 0x40000, 2):
    w = read_word(scan_off)
    if w is None:
        continue
    if w == 0x4EAE:
        disp = read_word(scan_off + 2)
        if disp is not None:
            d16 = signed_word(disp)
            if d16 == -0xA8:
                print(f"  ${ROM_BASE + scan_off:06X}: JSR -$A8(A6)  ; AddIntServer")
                # Show context
                ctx_start = max(scan_off - 20, 0)
                ctx_end = min(scan_off + 8, len(rom))
                co = ctx_start
                while co < ctx_end:
                    addr = ROM_BASE + co
                    line, new_co = decode_instruction(co)
                    nbytes = new_co - co
                    hex_str = ""
                    for i in range(0, min(nbytes, 10), 2):
                        ww = read_word(co + i)
                        if ww is not None:
                            hex_str += f"{ww:04X} "
                    marker = " <<<" if co == scan_off else ""
                    print(f"    ${addr:06X}  {hex_str:<24s}{line}{marker}")
                    co = new_co

# Additional: look for damage list manipulation
# ClipRect structure: cr_Next at offset 0, cr_prev at 4, 
#   cr_lobs at 8, cr_BitMap at 16, cr_bounds at 20
# Layer structure: Layer->DamageList at offset $2A (42)
# Let's search for references to offset $2A from address registers
print("\n--- Searching for Layer->DamageList offset $2A references ---")
# MOVE.L xxx,$002A(An) or MOVE.L $002A(An),xxx
count = 0
for scan_off in range(0x0000, 0x40000, 2):
    w = read_word(scan_off)
    if w is None:
        continue
    # Look for d16=002A in displacement words after MOVE-like instructions 
    # that reference An+$2A. Quick heuristic: look for 002A after EA-using instructions
    if w == 0x002A:
        # Check previous word for address register displacement usage
        pw = read_word(scan_off - 2)
        if pw is not None:
            # Various instructions that could use d16(An) with $002A displacement
            # Just print these for manual analysis
            if count < 30:
                # Try decoding from 4 bytes before
                addr = ROM_BASE + scan_off - 4
                line1, _ = decode_instruction(scan_off - 4)
                line2, _ = decode_instruction(scan_off - 2)
                # Only print if $002A appears as a displacement (not as an instruction)
                if "002A" in line1 or "002A" in line2:
                    print(f"  ${addr:06X}: {line1.strip()}")
                    count += 1

print("\n\nDone.")
