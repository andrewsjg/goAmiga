#!/usr/bin/env python3
"""Read Kickstart 1.3 ROM to find graphics.library function addresses
and disassemble code at specific return addresses."""

import struct
import sys

ROM_PATH = "/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom"
ROM_BASE = 0xFC0000

# 68000 condition code names
CC_NAMES = {
    0: "T", 1: "F", 2: "HI", 3: "LS", 4: "CC", 5: "CS",
    6: "NE", 7: "EQ", 8: "VC", 9: "VS", 10: "PL", 11: "MI",
    12: "GE", 13: "LT", 14: "GT", 15: "LE"
}

SIZE_NAMES = {0: ".B", 1: ".W", 2: ".L"}
SIZE_BYTES = {0: 1, 1: 2, 2: 4}

def read_rom():
    with open(ROM_PATH, "rb") as f:
        return f.read()

def get_word(rom, offset):
    return struct.unpack(">H", rom[offset:offset+2])[0]

def get_long(rom, offset):
    return struct.unpack(">I", rom[offset:offset+4])[0]

def get_sword(rom, offset):
    return struct.unpack(">h", rom[offset:offset+2])[0]

def get_sbyte(v):
    if v > 127: v -= 256
    return v

def ea_str(mode, reg, size, rom, offset, pc):
    """Decode effective address, return (string, extra_words_consumed)."""
    extra = 0
    if mode == 0:
        return f"D{reg}", 0
    elif mode == 1:
        return f"A{reg}", 0
    elif mode == 2:
        return f"(A{reg})", 0
    elif mode == 3:
        return f"(A{reg})+", 0
    elif mode == 4:
        return f"-(A{reg})", 0
    elif mode == 5:
        d = get_sword(rom, offset)
        return f"({d},A{reg})", 2
    elif mode == 6:
        ext = get_word(rom, offset)
        d8 = get_sbyte(ext & 0xFF)
        xr = "A" if ext & 0x8000 else "D"
        xn = (ext >> 12) & 7
        xl = ".L" if ext & 0x800 else ".W"
        return f"({d8},A{reg},{xr}{xn}{xl})", 2
    elif mode == 7:
        if reg == 0:
            addr = get_word(rom, offset)
            return f"($%04X).W" % addr, 2
        elif reg == 1:
            addr = get_long(rom, offset)
            return f"($%08X).L" % addr, 4
        elif reg == 2:
            d = get_sword(rom, offset)
            target = pc + 2 + d  # PC + displacement (PC points to extension word)
            return f"($%08X,PC)" % target, 2
        elif reg == 3:
            ext = get_word(rom, offset)
            d8 = get_sbyte(ext & 0xFF)
            xr = "A" if ext & 0x8000 else "D"
            xn = (ext >> 12) & 7
            xl = ".L" if ext & 0x800 else ".W"
            return f"({d8},PC,{xr}{xn}{xl})", 2
        elif reg == 4:
            if size == 0:
                v = get_word(rom, offset) & 0xFF
                return f"#$%02X" % v, 2
            elif size == 1:
                v = get_word(rom, offset)
                return f"#$%04X" % v, 2
            elif size == 2:
                v = get_long(rom, offset)
                return f"#$%08X" % v, 4
    return f"???({mode},{reg})", 0

def disasm_one(rom, rom_offset, pc):
    """Disassemble one instruction. Return (text, byte_length)."""
    if rom_offset + 2 > len(rom):
        return "???", 2
    
    w = get_word(rom, rom_offset)
    top4 = (w >> 12) & 0xF
    
    # NOP
    if w == 0x4E71:
        return "NOP", 2
    
    # RTS
    if w == 0x4E75:
        return "RTS", 2
    
    # RTE
    if w == 0x4E73:
        return "RTE", 2
    
    # ILLEGAL
    if w == 0x4AFC:
        return "ILLEGAL", 2
    
    # TRAP #n
    if (w & 0xFFF0) == 0x4E40:
        return f"TRAP    #{w & 0xF}", 2
    
    # LINK
    if (w & 0xFFF8) == 0x4E50:
        reg = w & 7
        d = get_sword(rom, rom_offset + 2)
        return f"LINK    A{reg},#${d & 0xFFFF:04X}", 4
    
    # UNLK
    if (w & 0xFFF8) == 0x4E58:
        return f"UNLK    A{w & 7}", 2
    
    # MOVEM (register to memory or memory to register)
    if (w & 0xFB80) == 0x4880:
        direction = (w >> 10) & 1  # 0=reg-to-mem, 1=mem-to-reg
        sz = ".W" if (w & 0x40) == 0 else ".L"
        mode = (w >> 3) & 7
        reg = w & 7
        mask = get_word(rom, rom_offset + 2)
        
        # Build register list string
        regs = []
        if direction == 0 and mode == 4:  # predecrement reverses bit order
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
        reglist = "/".join(regs) if regs else "???"
        
        ea, extra = ea_str(mode, reg, 2 if (w & 0x40) else 1, rom, rom_offset + 4, pc + 2)
        if direction == 0:
            return f"MOVEM{sz} {reglist},{ea}", 4 + extra
        else:
            return f"MOVEM{sz} {ea},{reglist}", 4 + extra
    
    # JSR
    if (w & 0xFFC0) == 0x4E80:
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, 2, rom, rom_offset + 2, pc)
        return f"JSR     {ea}", 2 + extra
    
    # JMP
    if (w & 0xFFC0) == 0x4EC0:
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, 2, rom, rom_offset + 2, pc)
        return f"JMP     {ea}", 2 + extra
    
    # LEA
    if (w & 0xF1C0) == 0x41C0:
        areg = (w >> 9) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, 2, rom, rom_offset + 2, pc)
        return f"LEA     {ea},A{areg}", 2 + extra
    
    # PEA
    if (w & 0xFFC0) == 0x4840:
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, 2, rom, rom_offset + 2, pc)
        return f"PEA     {ea}", 2 + extra

    # MOVE to/from SR, CCR
    if (w & 0xFFC0) == 0x40C0:
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, 1, rom, rom_offset + 2, pc)
        return f"MOVE.W  SR,{ea}", 2 + extra
    
    # MOVE to CCR
    if (w & 0xFFC0) == 0x44C0:
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, 1, rom, rom_offset + 2, pc)
        return f"MOVE.W  {ea},CCR", 2 + extra
    
    # MOVE to SR
    if (w & 0xFFC0) == 0x46C0:
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, 1, rom, rom_offset + 2, pc)
        return f"MOVE.W  {ea},SR", 2 + extra
    
    # MOVEA
    if top4 in (2, 3) and ((w >> 6) & 7) in (1, ):  # MOVEA
        sz = 2 if top4 == 2 else 1  # .L or .W
        szn = SIZE_NAMES[sz]
        dreg = (w >> 9) & 7
        smode = (w >> 3) & 7
        sreg = w & 7
        ea, extra = ea_str(smode, sreg, sz, rom, rom_offset + 2, pc)
        return f"MOVEA{szn} {ea},A{dreg}", 2 + extra

    # MOVE
    if top4 in (1, 2, 3):
        sz_map = {1: 0, 3: 1, 2: 2}  # MOVE size encoding
        sz = sz_map[top4]
        szn = SIZE_NAMES[sz]
        
        dst_reg = (w >> 9) & 7
        dst_mode = (w >> 6) & 7
        src_mode = (w >> 3) & 7
        src_reg = w & 7
        
        src, se = ea_str(src_mode, src_reg, sz, rom, rom_offset + 2, pc)
        dst, de = ea_str(dst_mode, dst_reg, sz, rom, rom_offset + 2 + se, pc)
        return f"MOVE{szn}  {src},{dst}", 2 + se + de
    
    # Bcc / BRA / BSR
    if top4 == 6:
        cond = (w >> 8) & 0xF
        disp = w & 0xFF
        if disp == 0:
            disp = get_sword(rom, rom_offset + 2)
            target = pc + 2 + disp
            inst_len = 4
        elif disp == 0xFF:
            disp = get_long(rom, rom_offset + 2)
            if disp > 0x7FFFFFFF: disp -= 0x100000000
            target = pc + 2 + disp
            inst_len = 6
        else:
            disp = get_sbyte(disp)
            target = pc + 2 + disp
            inst_len = 2
        
        if cond == 0:
            mn = "BRA"
        elif cond == 1:
            mn = "BSR"
        else:
            mn = f"B{CC_NAMES[cond]}"
        return f"{mn:<8}$%08X" % target, inst_len
    
    # DBcc
    if (w & 0xF0F8) == 0x50C8:
        cond = (w >> 8) & 0xF
        reg = w & 7
        disp = get_sword(rom, rom_offset + 2)
        target = pc + 2 + disp
        return f"DB{CC_NAMES[cond]:<6}D{reg},$%08X" % target, 4
    
    # MOVEQ
    if top4 == 7 and not (w & 0x100):
        dreg = (w >> 9) & 7
        data = get_sbyte(w & 0xFF)
        return f"MOVEQ   #${data & 0xFF:02X},D{dreg}", 2
    
    # ADD / ADDA
    if top4 == 0xD:
        dreg = (w >> 9) & 7
        opmode = (w >> 6) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        if opmode == 3:  # ADDA.W
            ea, extra = ea_str(mode, reg, 1, rom, rom_offset + 2, pc)
            return f"ADDA.W  {ea},A{dreg}", 2 + extra
        elif opmode == 7:  # ADDA.L
            ea, extra = ea_str(mode, reg, 2, rom, rom_offset + 2, pc)
            return f"ADDA.L  {ea},A{dreg}", 2 + extra
        else:
            sz = opmode & 3
            szn = SIZE_NAMES.get(sz, "?")
            ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
            if opmode < 3:
                return f"ADD{szn}   {ea},D{dreg}", 2 + extra
            else:
                return f"ADD{szn}   D{dreg},{ea}", 2 + extra
    
    # SUB / SUBA
    if top4 == 0x9:
        dreg = (w >> 9) & 7
        opmode = (w >> 6) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        if opmode == 3:
            ea, extra = ea_str(mode, reg, 1, rom, rom_offset + 2, pc)
            return f"SUBA.W  {ea},A{dreg}", 2 + extra
        elif opmode == 7:
            ea, extra = ea_str(mode, reg, 2, rom, rom_offset + 2, pc)
            return f"SUBA.L  {ea},A{dreg}", 2 + extra
        else:
            sz = opmode & 3
            szn = SIZE_NAMES.get(sz, "?")
            ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
            if opmode < 3:
                return f"SUB{szn}   {ea},D{dreg}", 2 + extra
            else:
                return f"SUB{szn}   D{dreg},{ea}", 2 + extra
    
    # CMP / CMPA
    if top4 == 0xB:
        dreg = (w >> 9) & 7
        opmode = (w >> 6) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        if opmode == 3:
            ea, extra = ea_str(mode, reg, 1, rom, rom_offset + 2, pc)
            return f"CMPA.W  {ea},A{dreg}", 2 + extra
        elif opmode == 7:
            ea, extra = ea_str(mode, reg, 2, rom, rom_offset + 2, pc)
            return f"CMPA.L  {ea},A{dreg}", 2 + extra
        elif opmode < 3:
            sz = opmode & 3
            szn = SIZE_NAMES.get(sz, "?")
            ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
            return f"CMP{szn}   {ea},D{dreg}", 2 + extra
    
    # AND
    if top4 == 0xC:
        dreg = (w >> 9) & 7
        opmode = (w >> 6) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        if opmode < 3:
            sz = opmode & 3
            szn = SIZE_NAMES.get(sz, "?")
            ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
            return f"AND{szn}   {ea},D{dreg}", 2 + extra
        elif opmode >= 4 and opmode <= 6:
            sz = opmode - 4
            szn = SIZE_NAMES.get(sz, "?")
            ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
            return f"AND{szn}   D{dreg},{ea}", 2 + extra
    
    # OR
    if top4 == 0x8:
        dreg = (w >> 9) & 7
        opmode = (w >> 6) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        if opmode < 3:
            sz = opmode & 3
            szn = SIZE_NAMES.get(sz, "?")
            ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
            return f"OR{szn}    {ea},D{dreg}", 2 + extra
        elif opmode >= 4 and opmode <= 6:
            sz = opmode - 4
            szn = SIZE_NAMES.get(sz, "?")
            ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
            return f"OR{szn}    D{dreg},{ea}", 2 + extra
    
    # EOR
    if top4 == 0xB:
        dreg = (w >> 9) & 7
        opmode = (w >> 6) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        if opmode >= 4 and opmode <= 6:
            sz = opmode - 4
            szn = SIZE_NAMES.get(sz, "?")
            ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
            return f"EOR{szn}   D{dreg},{ea}", 2 + extra
    
    # TST
    if (w & 0xFF00) == 0x4A00:
        sz = (w >> 6) & 3
        szn = SIZE_NAMES.get(sz, "?")
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
        return f"TST{szn}   {ea}", 2 + extra
    
    # CLR
    if (w & 0xFF00) == 0x4200:
        sz = (w >> 6) & 3
        szn = SIZE_NAMES.get(sz, "?")
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
        return f"CLR{szn}   {ea}", 2 + extra
    
    # NEG
    if (w & 0xFF00) == 0x4400:
        sz = (w >> 6) & 3
        szn = SIZE_NAMES.get(sz, "?")
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
        return f"NEG{szn}   {ea}", 2 + extra
    
    # NOT
    if (w & 0xFF00) == 0x4600:
        sz = (w >> 6) & 3
        szn = SIZE_NAMES.get(sz, "?")
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
        return f"NOT{szn}   {ea}", 2 + extra
    
    # EXT
    if (w & 0xFEB8) == 0x4880:
        sz = (w >> 6) & 7
        reg = w & 7
        if sz == 2:
            return f"EXT.W   D{reg}", 2
        elif sz == 3:
            return f"EXT.L   D{reg}", 2
    
    # SWAP
    if (w & 0xFFF8) == 0x4840:
        return f"SWAP    D{w & 7}", 2
    
    # ADDI
    if (w & 0xFF00) == 0x0600:
        sz = (w >> 6) & 3
        szn = SIZE_NAMES.get(sz, "?")
        mode = (w >> 3) & 7
        reg = w & 7
        if sz == 0:
            imm = get_word(rom, rom_offset + 2) & 0xFF
            imm_s = f"#${imm:02X}"
            ie = 2
        elif sz == 1:
            imm = get_word(rom, rom_offset + 2)
            imm_s = f"#${imm:04X}"
            ie = 2
        else:
            imm = get_long(rom, rom_offset + 2)
            imm_s = f"#${imm:08X}"
            ie = 4
        ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2 + ie, pc)
        return f"ADDI{szn}  {imm_s},{ea}", 2 + ie + extra
    
    # SUBI
    if (w & 0xFF00) == 0x0400:
        sz = (w >> 6) & 3
        szn = SIZE_NAMES.get(sz, "?")
        mode = (w >> 3) & 7
        reg = w & 7
        if sz == 0:
            imm = get_word(rom, rom_offset + 2) & 0xFF
            imm_s = f"#${imm:02X}"
            ie = 2
        elif sz == 1:
            imm = get_word(rom, rom_offset + 2)
            imm_s = f"#${imm:04X}"
            ie = 2
        else:
            imm = get_long(rom, rom_offset + 2)
            imm_s = f"#${imm:08X}"
            ie = 4
        ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2 + ie, pc)
        return f"SUBI{szn}  {imm_s},{ea}", 2 + ie + extra
    
    # CMPI
    if (w & 0xFF00) == 0x0C00:
        sz = (w >> 6) & 3
        szn = SIZE_NAMES.get(sz, "?")
        mode = (w >> 3) & 7
        reg = w & 7
        if sz == 0:
            imm = get_word(rom, rom_offset + 2) & 0xFF
            imm_s = f"#${imm:02X}"
            ie = 2
        elif sz == 1:
            imm = get_word(rom, rom_offset + 2)
            imm_s = f"#${imm:04X}"
            ie = 2
        else:
            imm = get_long(rom, rom_offset + 2)
            imm_s = f"#${imm:08X}"
            ie = 4
        ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2 + ie, pc)
        return f"CMPI{szn}  {imm_s},{ea}", 2 + ie + extra
    
    # ORI
    if (w & 0xFF00) == 0x0000 and (w & 0xC0) != 0xC0:
        sz = (w >> 6) & 3
        if sz <= 2:
            szn = SIZE_NAMES.get(sz, "?")
            mode = (w >> 3) & 7
            reg = w & 7
            if sz == 0:
                imm = get_word(rom, rom_offset + 2) & 0xFF
                imm_s = f"#${imm:02X}"
                ie = 2
            elif sz == 1:
                imm = get_word(rom, rom_offset + 2)
                imm_s = f"#${imm:04X}"
                ie = 2
            else:
                imm = get_long(rom, rom_offset + 2)
                imm_s = f"#${imm:08X}"
                ie = 4
            ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2 + ie, pc)
            return f"ORI{szn}   {imm_s},{ea}", 2 + ie + extra
    
    # ANDI
    if (w & 0xFF00) == 0x0200:
        sz = (w >> 6) & 3
        szn = SIZE_NAMES.get(sz, "?")
        mode = (w >> 3) & 7
        reg = w & 7
        if sz == 0:
            imm = get_word(rom, rom_offset + 2) & 0xFF
            imm_s = f"#${imm:02X}"
            ie = 2
        elif sz == 1:
            imm = get_word(rom, rom_offset + 2)
            imm_s = f"#${imm:04X}"
            ie = 2
        else:
            imm = get_long(rom, rom_offset + 2)
            imm_s = f"#${imm:08X}"
            ie = 4
        ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2 + ie, pc)
        return f"ANDI{szn}  {imm_s},{ea}", 2 + ie + extra
    
    # BTST/BCHG/BCLR/BSET (dynamic)
    if top4 == 0 and (w & 0x0100) and ((w >> 6) & 3) != 3:
        op = (w >> 6) & 3
        op_names = {0: "BTST", 1: "BCHG", 2: "BCLR", 3: "BSET"}
        dreg = (w >> 9) & 7
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, 0, rom, rom_offset + 2, pc)
        return f"{op_names[op]}    D{dreg},{ea}", 2 + extra
    
    # ADDQ / SUBQ
    if top4 == 5 and ((w >> 6) & 3) != 3:
        sz = (w >> 6) & 3
        szn = SIZE_NAMES.get(sz, "?")
        data = (w >> 9) & 7
        if data == 0: data = 8
        mode = (w >> 3) & 7
        reg = w & 7
        ea, extra = ea_str(mode, reg, sz, rom, rom_offset + 2, pc)
        if w & 0x100:
            return f"SUBQ{szn}  #${data:X},{ea}", 2 + extra
        else:
            return f"ADDQ{szn}  #${data:X},{ea}", 2 + extra
    
    # Scc
    if top4 == 5 and ((w >> 6) & 3) == 3:
        cond = (w >> 8) & 0xF
        mode = (w >> 3) & 7
        reg = w & 7
        # Check it's not DBcc (mode == 1)
        if mode != 1:
            ea, extra = ea_str(mode, reg, 0, rom, rom_offset + 2, pc)
            return f"S{CC_NAMES[cond]:<7}{ea}", 2 + extra
    
    # LSL/LSR/ASL/ASR/ROL/ROR/ROXL/ROXR (register)
    if top4 == 0xE:
        if (w & 0xC0) == 0xC0:
            # Memory shift
            op = (w >> 9) & 3
            dr = (w >> 8) & 1
            op_names_l = {0: "ASR", 1: "LSR", 2: "ROXR", 3: "ROR"}
            op_names_r = {0: "ASL", 1: "LSL", 2: "ROXL", 3: "ROL"}
            names = op_names_r if dr else op_names_l
            mode = (w >> 3) & 7
            reg = w & 7
            ea, extra = ea_str(mode, reg, 1, rom, rom_offset + 2, pc)
            return f"{names[op]}.W   {ea}", 2 + extra
        else:
            sz = (w >> 6) & 3
            szn = SIZE_NAMES.get(sz, "?")
            dr = (w >> 8) & 1  # 0=right, 1=left
            ir = (w >> 5) & 1  # 0=immediate, 1=register
            op = (w >> 3) & 3
            reg = w & 7
            count = (w >> 9) & 7
            op_names_l = {0: "ASR", 1: "LSR", 2: "ROXR", 3: "ROR"}
            op_names_r = {0: "ASL", 1: "LSL", 2: "ROXL", 3: "ROL"}
            names = op_names_r if dr else op_names_l
            if ir:
                return f"{names[op]}{szn}   D{count},D{reg}", 2
            else:
                if count == 0: count = 8
                return f"{names[op]}{szn}   #${count:X},D{reg}", 2
    
    # MOVE USP
    if (w & 0xFFF0) == 0x4E60:
        reg = w & 7
        if w & 8:
            return f"MOVE.L  USP,A{reg}", 2
        else:
            return f"MOVE.L  A{reg},USP", 2
    
    # EXG
    if top4 == 0xC and (w & 0x130) == 0x100:
        rx = (w >> 9) & 7
        ry = w & 7
        opmode = (w >> 3) & 0x1F
        if opmode == 0x08:
            return f"EXG     D{rx},D{ry}", 2
        elif opmode == 0x09:
            return f"EXG     A{rx},A{ry}", 2
        elif opmode == 0x11:
            return f"EXG     D{rx},A{ry}", 2
    
    # MULU / MULS
    if top4 == 0xC:
        if (w & 0x1C0) == 0x0C0:
            dreg = (w >> 9) & 7
            mode = (w >> 3) & 7
            reg = w & 7
            ea, extra = ea_str(mode, reg, 1, rom, rom_offset + 2, pc)
            return f"MULU.W  {ea},D{dreg}", 2 + extra
        elif (w & 0x1C0) == 0x1C0:
            dreg = (w >> 9) & 7
            mode = (w >> 3) & 7
            reg = w & 7
            ea, extra = ea_str(mode, reg, 1, rom, rom_offset + 2, pc)
            return f"MULS.W  {ea},D{dreg}", 2 + extra
    
    # DIVU / DIVS
    if top4 == 0x8:
        if (w & 0x1C0) == 0x0C0:
            dreg = (w >> 9) & 7
            mode = (w >> 3) & 7
            reg = w & 7
            ea, extra = ea_str(mode, reg, 1, rom, rom_offset + 2, pc)
            return f"DIVU.W  {ea},D{dreg}", 2 + extra
        elif (w & 0x1C0) == 0x1C0:
            dreg = (w >> 9) & 7
            mode = (w >> 3) & 7
            reg = w & 7
            ea, extra = ea_str(mode, reg, 1, rom, rom_offset + 2, pc)
            return f"DIVS.W  {ea},D{dreg}", 2 + extra
    
    # Fallback: raw word
    return f"DC.W    $%04X" % w, 2


def disasm_range(rom, start_offset, pc_start, count):
    """Disassemble `count` instructions starting at rom offset."""
    lines = []
    off = start_offset
    pc = pc_start
    for _ in range(count):
        if off >= len(rom) - 1:
            break
        text, length = disasm_one(rom, off, pc)
        # hex bytes
        hexbytes = " ".join(f"{rom[off+i]:02X}" for i in range(min(length, 10)))
        lines.append(f"  ${pc:08X}:  {hexbytes:<30s} {text}")
        off += length
        pc += length
    return lines


def main():
    rom = read_rom()
    print(f"ROM size: {len(rom)} bytes (${len(rom):X})")
    print(f"ROM base: ${ROM_BASE:08X}")
    print()
    
    # ===== FUNCTION TABLE =====
    table_offset = 0xB05A  # $FCB05A - $FC0000
    print("=" * 70)
    print("GRAPHICS.LIBRARY FUNCTION TABLE at $FCB05A")
    print("=" * 70)
    
    # Read entries 30-40 for context
    for i in range(30, 42):
        entry_off = table_offset + i * 4
        if entry_off + 4 <= len(rom):
            addr = get_long(rom, entry_off)
            rom_off = f"${entry_off:05X}"
            label = ""
            if i == 34: label = " <-- MrgCop (LVO -210, entry 34)"
            elif i == 35: label = " <-- MakeVPort (LVO -216, entry 35)"
            elif i == 36: label = " <-- LoadView (LVO -222, entry 36)"
            print(f"  Entry {i:3d} @ {rom_off}: ${addr:08X}{label}")
    
    print()
    
    # Verify MrgCop
    entry34_off = table_offset + 34 * 4
    mrgcop_addr = get_long(rom, entry34_off)
    print(f"MrgCop address (entry 34): ${mrgcop_addr:08X}  (expected $FC582C)")
    
    entry35_off = table_offset + 35 * 4
    makevport_addr = get_long(rom, entry35_off)
    print(f"MakeVPort address (entry 35): ${makevport_addr:08X}")
    
    entry36_off = table_offset + 36 * 4
    loadview_addr = get_long(rom, entry36_off)
    print(f"LoadView address (entry 36): ${loadview_addr:08X}")
    
    # Show what's at MakeVPort
    makevport_rom_off = makevport_addr - ROM_BASE
    if 0 <= makevport_rom_off < len(rom) - 20:
        print(f"\n--- MakeVPort code at ${makevport_addr:08X} (ROM offset ${makevport_rom_off:05X}) ---")
        lines = disasm_range(rom, makevport_rom_off, makevport_addr, 20)
        for l in lines:
            print(l)
    
    # ===== CODE AT $FE046E =====
    print()
    print("=" * 70)
    print("CODE AT $FE046E (return from MrgCop - the caller)")
    print("=" * 70)
    
    target_pc = 0xFE046E
    target_off = target_pc - ROM_BASE
    
    # Go back ~32 bytes for context before
    context_before = 40
    start_off = max(0, target_off - context_before)
    start_pc = ROM_BASE + start_off
    
    print(f"Disassembly from ${start_pc:08X} to ~${target_pc + 60:08X}:")
    print(f"(Return address $FE046E is marked with >>>)")
    print()
    
    off = start_off
    pc = start_pc
    for _ in range(50):
        if off >= len(rom) - 1:
            break
        text, length = disasm_one(rom, off, pc)
        hexbytes = " ".join(f"{rom[off+i]:02X}" for i in range(min(length, 10)))
        marker = ">>>" if pc == target_pc else "   "
        lines_text = f"{marker} ${pc:08X}:  {hexbytes:<30s} {text}"
        print(lines_text)
        off += length
        pc += length
        if pc > target_pc + 80:
            break
    
    # ===== CODE AT $FDC7A2 =====
    print()
    print("=" * 70)
    print("CODE AT $FDC7A2 (next return address on stack)")
    print("=" * 70)
    
    target_pc2 = 0xFDC7A2
    target_off2 = target_pc2 - ROM_BASE
    
    context_before2 = 32
    start_off2 = max(0, target_off2 - context_before2)
    start_pc2 = ROM_BASE + start_off2
    
    print(f"Disassembly from ${start_pc2:08X} to ~${target_pc2 + 40:08X}:")
    print(f"(Return address $FDC7A2 is marked with >>>)")
    print()
    
    off = start_off2
    pc = start_pc2
    for _ in range(40):
        if off >= len(rom) - 1:
            break
        text, length = disasm_one(rom, off, pc)
        hexbytes = " ".join(f"{rom[off+i]:02X}" for i in range(min(length, 10)))
        marker = ">>>" if pc == target_pc2 else "   "
        lines_text = f"{marker} ${pc:08X}:  {hexbytes:<30s} {text}"
        print(lines_text)
        off += length
        pc += length
        if pc > target_pc2 + 60:
            break
    
    # ===== ADDITIONAL: Look for JSR patterns near $FE046E =====
    print()
    print("=" * 70)
    print("ANALYSIS: What called MrgCop near $FE046E?")
    print("=" * 70)
    
    # The return address is $FE046E, so the JSR/BSR is just before it
    # Let's check what's at $FE046E - 6 (JSR abs.L = 6 bytes) and $FE046E - 4 (BSR.W = 4 bytes)
    for back in [2, 4, 6, 8]:
        check_pc = target_pc - back
        check_off = check_pc - ROM_BASE
        if 0 <= check_off < len(rom) - 8:
            text, length = disasm_one(rom, check_off, check_pc)
            if check_pc + length == target_pc:
                print(f"  Instruction ending at $FE046E: ${check_pc:08X}: {text}")
    
    # Similarly for $FDC7A2
    print()
    print("ANALYSIS: What called the function returning to $FDC7A2?")
    for back in [2, 4, 6, 8]:
        check_pc = target_pc2 - back
        check_off = check_pc - ROM_BASE
        if 0 <= check_off < len(rom) - 8:
            text, length = disasm_one(rom, check_off, check_pc)
            if check_pc + length == target_pc2:
                print(f"  Instruction ending at $FDC7A2: ${check_pc:08X}: {text}")
    
    # ===== Search for known Intuition function signatures =====
    print()
    print("=" * 70)
    print("SEARCHING FOR FUNCTION IDENTIFICATION")
    print("=" * 70)
    
    # Look for the function prologue before $FE046E by scanning back for LINK
    search_start = target_off - 200
    if search_start < 0: search_start = 0
    print(f"\nSearching for LINK instructions before $FE046E...")
    off = search_start
    pc = ROM_BASE + off
    while off < target_off:
        w = get_word(rom, off)
        if (w & 0xFFF8) == 0x4E50:  # LINK An,#xx
            disp = get_sword(rom, off + 2)
            reg = w & 7
            print(f"  LINK A{reg},#${disp & 0xFFFF:04X} at ${pc:08X}")
        off += 2
        pc += 2
    
    # Also search before $FDC7A2
    search_start2 = target_off2 - 200
    if search_start2 < 0: search_start2 = 0
    print(f"\nSearching for LINK instructions before $FDC7A2...")
    off = search_start2
    pc = ROM_BASE + off
    while off < target_off2:
        w = get_word(rom, off)
        if (w & 0xFFF8) == 0x4E50:
            disp = get_sword(rom, off + 2)
            reg = w & 7
            print(f"  LINK A{reg},#${disp & 0xFFFF:04X} at ${pc:08X}")
        off += 2
        pc += 2
    
    # Look for calls to graphics library base (-$D2 = MrgCop offset)
    print(f"\nLooking for library call pattern (JSR to LVO offset) near $FE046E...")
    # A6 is typically ExecBase, library calls are JSR offset(A6)
    # For graphics calls, code loads GfxBase into A6 first
    # MrgCop LVO = -$D2 = -210 = $FF2E (as unsigned word)
    search_start = target_off - 100
    off = search_start
    pc = ROM_BASE + off
    while off < target_off + 20:
        w = get_word(rom, off)
        # Check for JSR (d16,An) pattern: 4E A8+n dd dd  where n is register
        if (w & 0xFFC0) == 0x4E80:  # JSR
            mode = (w >> 3) & 7
            reg = w & 7
            if mode == 5:  # (d16,An)
                d = get_sword(rom, off + 2)
                apc = ROM_BASE + off
                print(f"  ${apc:08X}: JSR ({d},A{reg})  [offset {d} = ${d & 0xFFFF:04X}]")
        off += 2
        pc += 2


if __name__ == "__main__":
    main()
