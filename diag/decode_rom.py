import struct, sys

rom = open('Kick13.rom', 'rb').read()
base = 0xFC0000

def rw(addr):
    return struct.unpack_from('>H', rom, addr - base)[0]

def rl(addr):
    return struct.unpack_from('>I', rom, addr - base)[0]

def decode_one(addr):
    """Decode one 68000 instruction at addr. Returns (mnemonic_str, size_in_bytes)."""
    w = rw(addr)
    
    # MOVEM.L regs,-(An)
    if (w & 0xFFF8) == 0x48E0:
        regs = rw(addr+2)
        an = w & 7
        return f'MOVEM.L #${regs:04X},-(A{an})', 4
    # MOVEM.L (An)+,regs
    if (w & 0xFFF8) == 0x4CD8 or (w & 0xFFF8) == 0x4CDF:
        regs = rw(addr+2)
        an = w & 7
        return f'MOVEM.L (A{an})+,#${regs:04X}', 4
    
    # LINK
    if w == 0x4E54 or (w & 0xFFF8) == 0x4E50:
        an = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'LINK A{an},#{disp}', 4
    # UNLK
    if (w & 0xFFF8) == 0x4E58:
        an = w & 7
        return f'UNLK A{an}', 2
    # RTS
    if w == 0x4E75:
        return 'RTS', 2
    # NOP
    if w == 0x4E71:
        return 'NOP', 2
    
    # MOVEQ
    if (w >> 8) == 0x70 or (w >> 8) == 0x72 or (w >> 8) == 0x74 or (w >> 8) == 0x76 or (w >> 8) == 0x78 or (w >> 8) == 0x7A or (w >> 8) == 0x7C or (w >> 8) == 0x7E:
        if (w & 0x0100) == 0:
            dn = (w >> 9) & 7
            imm = struct.unpack_from('>b', rom, addr+1 - base)[0]
            return f'MOVEQ #{imm},D{dn}', 2

    # MOVE.L #imm,Dn
    if (w & 0xF1FF) == 0x203C:
        dn = (w >> 9) & 7
        imm = rl(addr+2)
        return f'MOVE.L #${imm:08X},D{dn}', 6
    
    # MOVE.W #imm,Dn
    if (w & 0xF1FF) == 0x303C:
        dn = (w >> 9) & 7
        imm = rw(addr+2)
        return f'MOVE.W #${imm:04X},D{dn}', 4

    # MOVEA.L (d16,An),An
    if (w & 0xF1F8) == 0x2068:
        dst = (w >> 9) & 7
        src = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'MOVEA.L (${disp:04X},A{src}),A{dst}', 4
    
    # LEA (d16,An),An
    if (w & 0xF1F8) == 0x41E8:
        dst = (w >> 9) & 7
        src = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'LEA (${disp:04X},A{src}),A{dst}', 4
    
    # LEA (d16,PC),An
    if (w & 0xF1FF) == 0x41FA:
        dst = (w >> 9) & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        target = addr + 2 + disp
        return f'LEA (${disp:04X},PC),A{dst}  ; =${target:06X}', 4
    
    # LEA (d8,An,Xn),An  -- mode 110, reg An
    if (w & 0xF1F8) == 0x41F0:
        dst = (w >> 9) & 7
        src = w & 7
        ext = rw(addr+2)
        xr = (ext >> 12) & 7
        xl = 'A' if (ext & 0x8000) else 'D'
        xs = '.L' if (ext & 0x0800) else '.W'
        d8 = ext & 0xFF
        if d8 > 127: d8 -= 256
        return f'LEA (${d8:02X},A{src},{xl}{xr}{xs}),A{dst}', 4

    # MOVE.L (An)+,Dn / MOVE.L An,Dn / MOVE.L Dn,Dn etc -- too complex, skip
    
    # ADDQ.L #imm,An
    if (w & 0xF1F8) == 0x5088:
        data = (w >> 9) & 7
        if data == 0: data = 8
        an = w & 7
        return f'ADDQ.L #{data},A{an}', 2
    # SUBQ.L #imm,An
    if (w & 0xF1F8) == 0x5188:
        data = (w >> 9) & 7
        if data == 0: data = 8
        an = w & 7
        return f'SUBQ.L #{data},A{an}', 2
    
    # DBcc Dn,disp
    if (w & 0xF0F8) == 0x50C8:
        cc = (w >> 8) & 0xF
        dn = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        target = addr + 2 + disp
        cc_names = ['T','F','HI','LS','CC','CS','NE','EQ','VC','VS','PL','MI','GE','LT','GT','LE']
        return f'DB{cc_names[cc]} D{dn},${target:06X}', 4
    
    # Bcc / BSR / BRA
    if (w & 0xF000) == 0x6000:
        cc = (w >> 8) & 0xF
        disp = w & 0xFF
        cc_names = ['RA','SR','HI','LS','CC','CS','NE','EQ','VC','VS','PL','MI','GE','LT','GT','BLE']
        if disp == 0:
            disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
            target = addr + 2 + disp
            return f'B{cc_names[cc]} ${target:06X}', 4
        else:
            if disp > 127: disp -= 256
            target = addr + 2 + disp
            return f'B{cc_names[cc]}.S ${target:06X}', 2
    
    # TST.L Dn
    if (w & 0xFFF8) == 0x4A80:
        dn = w & 7
        return f'TST.L D{dn}', 2
    # TST.W Dn
    if (w & 0xFFF8) == 0x4A40:
        dn = w & 7
        return f'TST.W D{dn}', 2
    
    # CMP.L #imm,(d16,An)
    if (w & 0xFFFF) == 0x0CB2 or (w & 0xFF00) == 0x0C00:
        # CMPI -- complex, simplified
        pass
    
    # MOVE.L (An),(d16,An)  -- 0x2950
    if (w & 0xF1F8) == 0x2950:
        dst = (w >> 9) & 7
        src = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'MOVE.L (A{src}),(${disp:04X},A{dst})', 4
    
    # MOVE.L (d16,An),(d16,An)  -- 0x2968
    if (w & 0xF1F8) == 0x2968:
        dst = (w >> 9) & 7
        src = w & 7
        disp1 = struct.unpack_from('>h', rom, addr+2 - base)[0]
        disp2 = struct.unpack_from('>h', rom, addr+4 - base)[0]
        return f'MOVE.L (${disp1:04X},A{src}),(${disp2:04X},A{dst})', 6
    
    # MOVE.L (An)+,(d16,An)  -- 0x2958
    if (w & 0xF1F8) == 0x2958:
        dst = (w >> 9) & 7
        src = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'MOVE.L (A{src})+,(${disp:04X},A{dst})', 4
    
    # MOVE.L Dn,(d16,An) -- 0x2940
    if (w & 0xF1F8) == 0x2940:
        dst = (w >> 9) & 7
        src = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'MOVE.L D{src},(${disp:04X},A{dst})', 4

    # MOVE.L An,Dn (MOVEA.L)
    if (w & 0xF1F8) == 0x2008:
        dn = (w >> 9) & 7
        an = w & 7
        return f'MOVE.L A{an},D{dn}', 2

    # MOVE.L (An)+,Dn
    if (w & 0xF1F8) == 0x2018:
        dn = (w >> 9) & 7
        an = w & 7
        return f'MOVE.L (A{an})+,D{dn}', 2
    
    # MOVE.L Dn,Dn2
    if (w & 0xF1F8) == 0x2000:
        dn = (w >> 9) & 7
        src = w & 7
        return f'MOVE.L D{src},D{dn}', 2
    
    # MOVEA.L An,An2
    if (w & 0xF1F8) == 0x2048:
        dst = (w >> 9) & 7
        src = w & 7
        return f'MOVEA.L A{src},A{dst}', 2
    
    # MOVEA.L Dn,An
    if (w & 0xF1F8) == 0x2040:
        dst = (w >> 9) & 7
        src = w & 7
        return f'MOVEA.L D{src},A{dst}', 2
    
    # AND.L #imm,Dn
    if (w & 0xFFC0) == 0x0280:
        dn = w & 7
        imm = rl(addr+2)
        return f'ANDI.L #${imm:08X},D{dn}', 6
    # AND.L Dn,Dn
    if (w & 0xF1F8) == 0xC080:
        dst = (w >> 9) & 7
        src = w & 7
        return f'AND.L D{src},D{dst}', 2
    
    # ADDA.L Dn,An
    if (w & 0xF1F8) == 0xD1C0:
        dst = (w >> 9) & 7
        src = w & 7
        return f'ADDA.L D{src},A{dst}', 2
    
    # MULU Dn,Dn
    if (w & 0xF1F8) == 0xC0C0:
        dst = (w >> 9) & 7
        src = w & 7
        return f'MULU D{src},D{dst}', 2
    # MULU #imm,Dn
    if (w & 0xF1FF) == 0xC0FC:
        dst = (w >> 9) & 7
        imm = rw(addr+2)
        return f'MULU #${imm:04X},D{dst}', 4
    
    # CMP.L (d16,An),Dn
    if (w & 0xF1F8) == 0xB0A8:
        dn = (w >> 9) & 7
        an = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'CMP.L (${disp:04X},A{an}),D{dn}', 4
    # CMP.L Dn,Dn
    if (w & 0xF1F8) == 0xB080:
        d1 = (w >> 9) & 7
        d2 = w & 7
        return f'CMP.L D{d2},D{d1}', 2
    # CMP.L #imm,(...)
    # CMPI
    if (w & 0xFF00) == 0x0C00:
        sz = (w >> 6) & 3
        mode = (w >> 3) & 7
        reg = w & 7
        if sz == 2 and mode == 7 and reg == 2:
            # CMPI.L #imm,(d16,PC) - rare, skip
            pass
        elif sz == 2:
            imm = rl(addr+2)
            if mode == 0:
                return f'CMPI.L #${imm:08X},D{reg}', 6
            elif mode == 5:
                disp = struct.unpack_from('>h', rom, addr+6 - base)[0]
                return f'CMPI.L #${imm:08X},(${disp:04X},A{reg})', 8
            elif mode == 6:
                ext = rw(addr+6)
                xr = (ext >> 12) & 7
                xl = 'A' if (ext & 0x8000) else 'D'
                d8 = ext & 0xFF
                return f'CMPI.L #${imm:08X},(${d8:02X},A{reg},{xl}{xr})', 8
        elif sz == 1:
            imm = rw(addr+2)
            if mode == 0:
                return f'CMPI.W #${imm:04X},D{reg}', 4
        elif sz == 0:
            imm = rw(addr+2) & 0xFF
            if mode == 5:
                disp = struct.unpack_from('>h', rom, addr+4 - base)[0]
                return f'CMPI.B #${imm:02X},(${disp:04X},A{reg})', 6

    # EOR.L Dn,Dn
    if (w & 0xF1F8) == 0xB180:
        src = (w >> 9) & 7
        dst = w & 7
        return f'EOR.L D{src},D{dst}', 2
    
    # SUB.L Dn,Dn
    if (w & 0xF1F8) == 0x9080:
        dst = (w >> 9) & 7
        src = w & 7
        return f'SUB.L D{src},D{dst}', 2
    # SUBI.L #imm,Dn
    if (w & 0xFFC0) == 0x0480:
        dn = w & 7
        imm = rl(addr+2)
        return f'SUBI.L #${imm:08X},D{dn}', 6
    
    # ADD.L Dn,An (ADDA)
    if (w & 0xF1F8) == 0xD1C8:
        dst = (w >> 9) & 7
        src = w & 7
        return f'ADDA.L A{src},A{dst}', 2
    # ADD.L Dn,Dn
    if (w & 0xF1F8) == 0xD080:
        dst = (w >> 9) & 7
        src = w & 7
        return f'ADD.L D{src},D{dst}', 2
    
    # SWAP Dn
    if (w & 0xFFF8) == 0x4840:
        dn = w & 7
        return f'SWAP D{dn}', 2
    
    # CLR.W (d16,An)
    if (w & 0xFFF8) == 0x4268:
        an = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'CLR.W (${disp:04X},A{an})', 4
    
    # BTST #imm,(d16,An) -- 0x0828
    if (w & 0xFFF8) == 0x0828:
        an = w & 7
        bit = rw(addr+2)
        disp = struct.unpack_from('>h', rom, addr+4 - base)[0]
        return f'BTST #${bit:04X},(${disp:04X},A{an})', 6
    # BSET/BCLR/BCHG #imm,...
    if (w & 0xFFC0) == 0x08C0:
        reg = w & 7
        bit = rw(addr+2)
        return f'BSET #${bit:04X},D{reg}', 4
    
    # ASR/LSR/ROL/ROR etc
    if (w & 0xF018) == 0xE008:
        cnt = (w >> 9) & 7
        if cnt == 0: cnt = 8
        dr = (w >> 8) & 1
        sz = (w >> 6) & 3
        dn = w & 7
        op = 'LSR' if dr == 0 else 'LSL'
        sizes = ['B','W','L']
        return f'{op}.{sizes[sz]} #{cnt},D{dn}', 2
    if (w & 0xF018) == 0xE000:
        cnt = (w >> 9) & 7
        if cnt == 0: cnt = 8
        dr = (w >> 8) & 1
        sz = (w >> 6) & 3
        dn = w & 7
        op = 'ASR' if dr == 0 else 'ASL'
        sizes = ['B','W','L']
        return f'{op}.{sizes[sz]} #{cnt},D{dn}', 2
    if (w & 0xF018) == 0xE048:
        cnt = (w >> 9) & 7
        if cnt == 0: cnt = 8
        dr = (w >> 8) & 1
        sz = (w >> 6) & 3
        dn = w & 7
        op = 'LSR' if dr == 0 else 'LSL'
        sizes = ['B','W','L']
        return f'{op}.{sizes[sz]} D{cnt},D{dn}', 2
    if (w & 0xF118) == 0xE100:
        cnt_or_reg = (w >> 9) & 7
        ir = (w >> 5) & 1
        sz = (w >> 6) & 3
        dn = w & 7
        dr = (w >> 8) & 1
        typ = (w >> 3) & 3
        ops = ['ASR','LSR','ROXR','ROR'] if dr == 0 else ['ASL','LSL','ROXL','ROL']
        op = ops[typ]
        sizes = ['B','W','L']
        if ir:
            return f'{op}.{sizes[sz]} D{cnt_or_reg},D{dn}', 2
        else:
            c = cnt_or_reg if cnt_or_reg else 8
            return f'{op}.{sizes[sz]} #{c},D{dn}', 2
    
    # PEA (An)
    if (w & 0xFFF8) == 0x4850:
        an = w & 7
        return f'PEA (A{an})', 2
    # PEA (d16,SP)
    if w == 0x486F:
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'PEA (${disp:04X},SP)', 4
    
    # MOVE.B (d16,An),Dn
    if (w & 0xF1F8) == 0x1028:
        dn = (w >> 9) & 7
        an = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'MOVE.B (${disp:04X},A{an}),D{dn}', 4
    # CMP.B Dn,Dn
    if (w & 0xF1F8) == 0xB000:
        d1 = (w >> 9) & 7
        d2 = w & 7
        return f'CMP.B D{d2},D{d1}', 2
    # MOVE.B Dn,(d16,An) 
    if (w & 0xF1F8) == 0x1140:
        dst_an = (w >> 9) & 7
        src_dn = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'MOVE.B D{src_dn},(${disp:04X},A{dst_an})', 4
    
    # MOVE.W Dn,(d16,An) 
    if (w & 0xF1F8) == 0x3140:
        dst_an = (w >> 9) & 7
        src_dn = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'MOVE.W D{src_dn},(${disp:04X},A{dst_an})', 4
    
    # MOVE.W #imm,(d16,An)
    if (w & 0xF1FF) == 0x317C:
        dst_an = (w >> 9) & 7
        imm = rw(addr+2)
        disp = struct.unpack_from('>h', rom, addr+4 - base)[0]
        return f'MOVE.W #${imm:04X},(${disp:04X},A{dst_an})', 6
    
    # MOVE.L d16,SP (for stack)
    if (w & 0xFFFF) == 0x2F00:
        return 'MOVE.L D0,-(SP)', 2
    if (w & 0xFFF8) == 0x2F00:
        dn = w & 7
        return f'MOVE.L D{dn},-(SP)', 2
    if (w & 0xFFF8) == 0x2F08:
        an = w & 7
        return f'MOVE.L A{an},-(SP)', 2
    
    # MOVE.L (SP)+,Dn
    if (w & 0xF1FF) == 0x201F:
        dn = (w >> 9) & 7
        return f'MOVE.L (SP)+,D{dn}', 2
    
    # NEG.L Dn
    if (w & 0xFFF8) == 0x4480:
        dn = w & 7
        return f'NEG.L D{dn}', 2
    
    # CMPA.L
    if (w & 0xF1F8) == 0xB1C0:
        an = (w >> 9) & 7
        src = w & 7
        return f'CMPA.L D{src},A{an}', 2
    if (w & 0xF1FF) == 0xB1FC:
        an = (w >> 9) & 7
        imm = rl(addr+2)
        return f'CMPA.L #${imm:08X},A{an}', 6
    
    # MOVE.W (d16,An),Dn
    if (w & 0xF1F8) == 0x3028:
        dn = (w >> 9) & 7
        an = w & 7
        disp = struct.unpack_from('>h', rom, addr+2 - base)[0]
        return f'MOVE.W (${disp:04X},A{an}),D{dn}', 4
    
    # Fallback
    return f'DC.W ${w:04X}', 2


# Disassemble FEAC62-FEAE00
addr = 0xFEAC62
end = 0xFEAE20
print('=== DISASSEMBLY: FEAC62-FEAE20 (track decode function) ===')
while addr < end:
    mnem, size = decode_one(addr)
    # Get raw bytes
    raw = ' '.join(f'{rom[addr-base+i]:02X}' for i in range(size))
    print(f'${addr:06X}: {raw:24s}  {mnem}')
    addr += size

sys.exit(0)



def decode_line(addr):
    w0 = rw(addr)
    w1 = rw(addr+2) if addr+2 <= 0xFFFFFF else 0
    w2 = rw(addr+4) if addr+4 <= 0xFFFFFF else 0
    raw = f"{w0:04X}"
    size = 2
    
    if w0 == 0x4E70:
        return size, f"RESET"
    elif w0 == 0x4E71:
        return size, f"NOP"
    elif w0 == 0x4E73:
        return size, f"RTE"
    elif w0 == 0x4E75:
        return size, f"RTS"
    elif w0 == 0x4E72:
        return 4, f"STOP #${w1:04X}"
    elif w0 == 0x4EF9:
        tgt = (w1 << 16) | w2
        return 6, f"JMP ${tgt:08X}"
    elif w0 == 0x4EF8:
        tgt = w1 if w1 < 0x8000 else w1 - 0x10000
        return 4, f"JMP ${tgt:08X}.W"
    elif w0 == 0x4EB9:
        tgt = (w1 << 16) | w2
        return 6, f"JSR ${tgt:08X}"
    elif w0 == 0x4EAE:
        d = w1 if w1 < 0x8000 else w1 - 0x10000
        return 4, f"JSR ({d},A6)"
    elif w0 == 0x4EA9:
        d = w1 if w1 < 0x8000 else w1 - 0x10000
        return 4, f"JSR ({d},A1)"
    elif (w0 & 0xFF00) == 0x6100 and (w0 & 0xFF) != 0:
        d = w0 & 0xFF
        if d >= 0x80: d -= 0x100
        tgt = addr + 2 + d
        return 2, f"BSR.B ${tgt:08X}"
    elif w0 == 0x6100:
        d = w1 if w1 < 0x8000 else w1 - 0x10000
        tgt = addr + 2 + d
        return 4, f"BSR.W ${tgt:08X}"
    elif (w0 & 0xFF00) == 0x6000 and (w0 & 0xFF) != 0:
        d = w0 & 0xFF
        if d >= 0x80: d -= 0x100
        tgt = addr + 2 + d
        return 2, f"BRA.B ${tgt:08X}"
    elif w0 == 0x6000:
        d = w1 if w1 < 0x8000 else w1 - 0x10000
        tgt = addr + 2 + d
        return 4, f"BRA.W ${tgt:08X}"
    elif (w0 >> 12) == 6 and (w0 & 0xFF) != 0:
        cond = (w0 >> 8) & 0xF
        d = w0 & 0xFF
        if d >= 0x80: d -= 0x100
        tgt = addr + 2 + d
        mn = mnemonics_bcc[cond] if cond < 16 else f"B?{cond}"
        return 2, f"{mn}.B ${tgt:08X}"
    elif (w0 >> 12) == 6 and (w0 & 0xFF) == 0:
        cond = (w0 >> 8) & 0xF
        d = w1 if w1 < 0x8000 else w1 - 0x10000
        tgt = addr + 2 + d
        mn = mnemonics_bcc[cond] if cond < 16 else f"B?{cond}"
        return 4, f"{mn}.W ${tgt:08X}"
    else:
        return 2, f"??? ({w0:04X} {w1:04X})"

def disasm(start, end_addr, stop_at_reset=True):
    addr = start
    while addr < end_addr:
        size, mnem = decode_line(addr)
        w0 = rw(addr)
        raw = ' '.join(f"{rw(addr+i*2):04X}" for i in range(size//2))
        print(f"  {addr:08X}: {raw:<20} {mnem}")
        if stop_at_reset and w0 == 0x4E70:
            break
        addr += size

print("=== FC043E (ChkSum) onwards ===")
disasm(0xFC043E, 0xFC0600)

print()
print("=== FC0896 test (boot-detect branch) ===")
disasm(0xFC0890, 0xFC08E0)

print()
print("=== FC0984 (SoftIRQ scanner called from FC093C) ===")
disasm(0xFC0984, 0xFC09C8)

print()
print("=== FC093C (Exec scheduler, called from FC0504) ===")
disasm(0xFC093C, 0xFC09A0)  # show just the dispatch portion

print()
print("=== FC2FF0 (warm start probe / 2nd stage boot) ===")
disasm(0xFC2FF0, 0xFC3100)

print()
print("=== FC0960..FC0984 (scheduler body) ===")
disasm(0xFC0958, 0xFC0986)

print()
print("=== FC0A00..FC0A40 (possible task switch) ===")
disasm(0xFC0A00, 0xFC0A50)
