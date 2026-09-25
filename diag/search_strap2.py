#!/usr/bin/env python3
"""Deep analysis of strap module: disassemble $FE8600-$FE8700 and look for BootPtr jump."""

import struct

ROM_BASE = 0xFC0000

with open('Kick13.rom', 'rb') as f:
    rom = f.read()

def read_word(offset):
    return struct.unpack_from('>H', rom, offset)[0]

def read_long(offset):
    return struct.unpack_from('>I', rom, offset)[0]

def signed_word(w):
    return w - 0x10000 if w >= 0x8000 else w

# Hex dump $FE85F0-$FE8700
print("=== Hex dump $FE85F0-$FE8700 ===")
for off in range(0x285F0, min(0x28700, len(rom)), 16):
    addr = ROM_BASE + off
    chunk = rom[off:off+16]
    hexstr = ' '.join(f'{b:02X}' for b in chunk)
    ascii_str = ''.join(chr(b) if 32 <= b < 127 else '.' for b in chunk)
    print(f"  ${addr:06X}: {hexstr}  {ascii_str}")

# Hex dump $FE8680-$FE8700
print("\n=== Hex dump $FE8680-$FE8700 ===")
for off in range(0x28680, min(0x28700, len(rom)), 16):
    addr = ROM_BASE + off
    chunk = rom[off:off+16]
    hexstr = ' '.join(f'{b:02X}' for b in chunk)
    ascii_str = ''.join(chr(b) if 32 <= b < 127 else '.' for b in chunk)
    print(f"  ${addr:06X}: {hexstr}  {ascii_str}")

# Now search near the BootPtr access. The key is $FC6F44: MOVE.L $42(A6),A0
# Let's disassemble around there
print("\n=== Hex dump around $FC6EA0 - $FC6F80 (BootPtr related) ===")
for off in range(0x06EA0, min(0x06F80, len(rom)), 16):
    addr = ROM_BASE + off
    chunk = rom[off:off+16]
    hexstr = ' '.join(f'{b:02X}' for b in chunk)
    ascii_str = ''.join(chr(b) if 32 <= b < 127 else '.' for b in chunk)
    print(f"  ${addr:06X}: {hexstr}  {ascii_str}")

# Search for JMP (A0) patterns in strap area and near BootPtr code
print("\n=== Searching for JMP (A0) = $4ED0 in full ROM ===")
for off in range(0, len(rom), 2):
    w = read_word(off)
    if w == 0x4ED0:
        addr = ROM_BASE + off
        # Show context
        ctx_before = rom[max(0,off-8):off].hex()
        ctx_after = rom[off+2:min(len(rom),off+10)].hex()
        print(f"  ${addr:06X}: JMP (A0)  before=[{ctx_before}] after=[{ctx_after}]")

# Search for the specific code pattern at $FC6F44
print("\n=== Disasm around $FC6F44 (MOVE.L $42(A6),A0) ===")
off = 0x06F20
while off < 0x06F90 and off < len(rom) - 2:
    addr = ROM_BASE + off
    w = read_word(off)
    size = 2
    desc = f"DC.W ${w:04X}"
    
    if w == 0x4E75:
        desc = "RTS"
    elif w == 0x4E71:
        desc = "NOP"
    elif 0x4E90 <= w <= 0x4E97:
        desc = f"JSR (A{w-0x4E90})"
    elif 0x4ED0 <= w <= 0x4ED7:
        desc = f"JMP (A{w-0x4ED0})"
    elif w == 0x4EB9 and off + 4 < len(rom):
        t = read_long(off+2)
        desc = f"JSR ${t:08X}"
        size = 6
    elif w == 0x4EF9 and off + 4 < len(rom):
        t = read_long(off+2)
        desc = f"JMP ${t:08X}"
        size = 6
    elif (w & 0xF000) == 0x2000:
        sr = w & 7
        sm = (w >> 3) & 7
        dm = (w >> 6) & 7
        dr = (w >> 9) & 7
        if sm == 5 and off + 2 < len(rom):
            d = signed_word(read_word(off+2))
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.L ${d & 0xFFFF:04X}(A{sr}),{dn}"
            size = 4
        elif sm == 7 and sr == 0 and off + 2 < len(rom):
            a = signed_word(read_word(off+2))
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.L ${a & 0xFFFF:04X}.W,{dn}"
            size = 4
        elif sm == 0:
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.L D{sr},{dn}"
        elif sm == 2:
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.L (A{sr}),{dn}"
        elif sm == 3:
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.L (A{sr})+,{dn}"
    elif (w & 0xF000) == 0x3000:
        sr = w & 7
        sm = (w >> 3) & 7
        dm = (w >> 6) & 7
        dr = (w >> 9) & 7
        if sm == 5 and off + 2 < len(rom):
            d = signed_word(read_word(off+2))
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.W ${d & 0xFFFF:04X}(A{sr}),{dn}"
            size = 4
        elif sm == 7 and sr == 4 and off + 2 < len(rom):
            v = read_word(off+2)
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.W #${v:04X},{dn}"
            size = 4
    elif (w & 0xF100) == 0x7000:
        r = (w >> 9) & 7
        v = w & 0xFF
        desc = f"MOVEQ #${v:02X},D{r}"
    elif w == 0x4A80 + (w & 7) and (w & 0xFFF8) == 0x4A80:
        desc = f"TST.L D{w & 7}"
    elif (w & 0xF000) == 0x6000:
        cc = (w >> 8) & 0x0F
        cc_names = ['BRA','BSR','BHI','BLS','BCC','BCS','BNE','BEQ',
                   'BVC','BVS','BPL','BMI','BGE','BLT','BGT','BLE']
        disp8 = w & 0xFF
        if disp8 == 0 and off + 2 < len(rom):
            disp = signed_word(read_word(off+2))
            target = addr + 2 + disp
            desc = f"{cc_names[cc]}.W ${target:06X}"
            size = 4
        elif disp8 != 0:
            if disp8 & 0x80:
                disp8 -= 256
            target = addr + 2 + disp8
            desc = f"{cc_names[cc]}.B ${target:06X}"
    elif (w & 0xFFF8) == 0x41E8 and off + 2 < len(rom):
        dr = (w >> 9) & 7
        sr = w & 7
        d = signed_word(read_word(off+2))
        desc = f"LEA ${d & 0xFFFF:04X}(A{sr}),A{dr}"
        size = 4
    elif (w & 0xFFF8) == 0x4E58:
        desc = f"UNLK A{w & 7}"
    elif (w & 0xFFF8) == 0x4E50 and off + 2 < len(rom):
        d = signed_word(read_word(off+2))
        desc = f"LINK A{w & 7},#${d & 0xFFFF:04X}"
        size = 4
    elif (w & 0xFFF8) == 0x48E0 and off + 2 < len(rom):
        r = w & 7
        m = read_word(off+2)
        desc = f"MOVEM.L regs,-(A{r})  mask=${m:04X}"
        size = 4
    elif (w & 0xFFF8) == 0x4CDF and off + 2 < len(rom):
        r = w & 7  # not used in this encoding
        m = read_word(off+2)
        desc = f"MOVEM.L (A7)+,regs  mask=${m:04X}"
        size = 4
    elif (w & 0xFFF8) == 0x4CD0 and off + 2 < len(rom):
        r = w & 7
        m = read_word(off+2)
        desc = f"MOVEM.L (A{r})+,regs  mask=${m:04X}"
        size = 4
    elif w == 0x4E73:
        desc = "RTE"
    elif (w & 0xFFF0) == 0x4E40:
        desc = f"TRAP #{w & 0xF}"
    elif (w & 0xF1F8) == 0xD098:
        dn = (w >> 9) & 7
        an = w & 7
        desc = f"ADD.L (A{an})+,D{dn}"
    elif (w & 0xFFF8) == 0x4280:
        desc = f"CLR.L D{w & 7}"
    elif w == 0x4840 + (w & 7) and (w & 0xFFF8) == 0x4840:
        desc = f"SWAP D{w & 7}"
    elif (w & 0xF100) == 0x5000:
        data = (w >> 9) & 7
        if data == 0: data = 8
        sz = (w >> 6) & 3
        szn = ['.B','.W','.L']
        m = (w >> 3) & 7
        r = w & 7
        if sz < 3:
            if m == 0: desc = f"ADDQ{szn[sz]} #{data},D{r}"
            elif m == 1: desc = f"ADDQ{szn[sz]} #{data},A{r}"
    elif (w & 0xF100) == 0x5100:
        data = (w >> 9) & 7
        if data == 0: data = 8
        sz = (w >> 6) & 3
        szn = ['.B','.W','.L']
        m = (w >> 3) & 7
        r = w & 7
        if sz < 3:
            if m == 0: desc = f"SUBQ{szn[sz]} #{data},D{r}"
            elif m == 1: desc = f"SUBQ{szn[sz]} #{data},A{r}"
    elif (w & 0xF0F8) == 0x50C8 and off + 2 < len(rom):
        cc = (w >> 8) & 0xF
        r = w & 7
        cc_names = ['DBT','DBRA','DBHI','DBLS','DBCC','DBCS','DBNE','DBEQ',
                   'DBVC','DBVS','DBPL','DBMI','DBGE','DBLT','DBGT','DBLE']
        disp = signed_word(read_word(off+2))
        target = addr + 2 + disp
        desc = f"{cc_names[cc]} D{r},${target:06X}"
        size = 4
    elif (w & 0xFFF8) == 0x2F08:
        desc = f"MOVE.L A{w & 7},-(A7)"
    elif (w & 0xFFF8) == 0x2F00:
        desc = f"MOVE.L D{w & 7},-(A7)"
    elif (w & 0xF1C0) == 0xB0BC and off + 4 < len(rom):
        # CMP.L #imm,Dn
        dr = (w >> 9) & 7
        imm = read_long(off+2)
        desc = f"CMPI.L #${imm:08X},D{dr}"
        size = 6
    elif (w & 0xFFC0) == 0x4A80:
        m = (w >> 3) & 7
        r = w & 7
        if m == 0:
            desc = f"TST.L D{r}"

    hexbytes = ' '.join(f'{b:02X}' for b in rom[off:off+size])
    print(f"  ${addr:06X}: {hexbytes:24s} {desc}")
    off += size

# Now disasm $FE86A0-$FE8700 (where BootPtr logic might be)
print("\n=== Disasm $FE85F0-$FE8700 ===")
off = 0x285F0
while off < 0x28700 and off < len(rom) - 2:
    addr = ROM_BASE + off
    w = read_word(off)
    size = 2
    desc = f"DC.W ${w:04X}"
    
    if w == 0x4E75:
        desc = "RTS"
    elif w == 0x4E71:
        desc = "NOP"
    elif 0x4E90 <= w <= 0x4E97:
        desc = f"JSR (A{w-0x4E90})"
    elif 0x4ED0 <= w <= 0x4ED7:
        desc = f"JMP (A{w-0x4ED0})"
    elif w == 0x4EB9 and off + 4 < len(rom):
        t = read_long(off+2)
        desc = f"JSR ${t:08X}"
        size = 6
    elif w == 0x4EF9 and off + 4 < len(rom):
        t = read_long(off+2)
        desc = f"JMP ${t:08X}"
        size = 6
    elif (w & 0xF000) == 0x2000:
        sr = w & 7
        sm = (w >> 3) & 7
        dm = (w >> 6) & 7
        dr = (w >> 9) & 7
        if sm == 5 and off + 2 < len(rom):
            d = signed_word(read_word(off+2))
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.L ${d & 0xFFFF:04X}(A{sr}),{dn}"
            size = 4
        elif sm == 7 and sr == 0 and off + 2 < len(rom):
            a = signed_word(read_word(off+2))
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.L ${a & 0xFFFF:04X}.W,{dn}"
            size = 4
        elif sm == 0:
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.L D{sr},{dn}"
        elif sm == 1:
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.L A{sr},{dn}"
        elif sm == 2:
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.L (A{sr}),{dn}"
        elif sm == 3:
            dn = f"A{dr}" if dm == 1 else f"D{dr}" if dm == 0 else "?"
            desc = f"MOVE.L (A{sr})+,{dn}"
    elif (w & 0xFFF8) == 0x2F08:
        desc = f"MOVE.L A{w & 7},-(A7)"
    elif (w & 0xFFF8) == 0x2F00:
        desc = f"MOVE.L D{w & 7},-(A7)"
    elif (w & 0xFFF8) == 0x205F:
        desc = f"MOVEA.L (A7)+,A{(w >> 9) & 7}"
    elif (w & 0xFFF8) == 0x4E58:
        desc = f"UNLK A{w & 7}"
    elif (w & 0xFFF8) == 0x4E50 and off + 2 < len(rom):
        d = signed_word(read_word(off+2))
        desc = f"LINK A{w & 7},#${d & 0xFFFF:04X}"
        size = 4
    elif (w & 0xFFF8) == 0x41E8 and off + 2 < len(rom):
        dr = (w >> 9) & 7
        sr = w & 7
        d = signed_word(read_word(off+2))
        desc = f"LEA ${d & 0xFFFF:04X}(A{sr}),A{dr}"
        size = 4
    elif (w & 0xFFF8) == 0x48E0 and off + 2 < len(rom):
        r = w & 7
        m = read_word(off+2)
        desc = f"MOVEM.L regs,-(A{r})  mask=${m:04X}"
        size = 4
    elif (w & 0xFFF8) == 0x4CDF and off + 2 < len(rom):
        m = read_word(off+2)
        desc = f"MOVEM.L (A7)+,regs  mask=${m:04X}"
        size = 4
    elif (w & 0xF000) == 0x6000:
        cc = (w >> 8) & 0x0F
        cc_names = ['BRA','BSR','BHI','BLS','BCC','BCS','BNE','BEQ',
                   'BVC','BVS','BPL','BMI','BGE','BLT','BGT','BLE']
        disp8 = w & 0xFF
        if disp8 == 0 and off + 2 < len(rom):
            disp = signed_word(read_word(off+2))
            target = addr + 2 + disp
            desc = f"{cc_names[cc]}.W ${target:06X}"
            size = 4
        elif disp8 != 0:
            if disp8 & 0x80:
                disp8 -= 256
            target = addr + 2 + disp8
            desc = f"{cc_names[cc]}.B ${target:06X}"
    elif (w & 0xF100) == 0x7000:
        r = (w >> 9) & 7
        v = w & 0xFF
        desc = f"MOVEQ #${v:02X},D{r}"
    elif (w & 0xFFF0) == 0x4E40:
        desc = f"TRAP #{w & 0xF}"
    elif (w & 0xF100) == 0x5000:
        data = (w >> 9) & 7
        if data == 0: data = 8
        sz = (w >> 6) & 3
        szn = ['.B','.W','.L']
        m = (w >> 3) & 7
        r = w & 7
        if sz < 3:
            if m == 0: desc = f"ADDQ{szn[sz]} #{data},D{r}"
            elif m == 1: desc = f"ADDQ{szn[sz]} #{data},A{r}"
    elif (w & 0xF100) == 0x5100:
        data = (w >> 9) & 7
        if data == 0: data = 8
        sz = (w >> 6) & 3
        m = (w >> 3) & 7
        r = w & 7
        szn = ['.B','.W','.L']
        if sz < 3:
            if m == 0: desc = f"SUBQ{szn[sz]} #{data},D{r}"
            elif m == 1: desc = f"SUBQ{szn[sz]} #{data},A{r}"
    elif (w & 0xF0F8) == 0x50C8 and off + 2 < len(rom):
        cc = (w >> 8) & 0xF
        r = w & 7
        cc_names = ['DBT','DBRA','DBHI','DBLS','DBCC','DBCS','DBNE','DBEQ',
                   'DBVC','DBVS','DBPL','DBMI','DBGE','DBLT','DBGT','DBLE']
        disp = signed_word(read_word(off+2))
        target = addr + 2 + disp
        desc = f"{cc_names[cc]} D{r},${target:06X}"
        size = 4
    elif (w & 0xFFF8) == 0x4A80:
        desc = f"TST.L D{w & 7}"
    elif w == 0x4E73:
        desc = "RTE"
    elif (w & 0xF1F8) == 0xD098:
        dn = (w >> 9) & 7
        an = w & 7
        desc = f"ADD.L (A{an})+,D{dn}"

    hexbytes = ' '.join(f'{b:02X}' for b in rom[off:off+size])
    print(f"  ${addr:06X}: {hexbytes:24s} {desc}")
    off += size

# Look at $FC6F20-$FC6FA0 for BootPtr code
print("\n=== Disasm $FC6F20-$FC6FA0 (exec ColdReboot / BootPtr area) ===")
off = 0x06F20
while off < 0x06FA0 and off < len(rom) - 2:
    addr = ROM_BASE + off
    w = read_word(off)
    size = 2
    desc = f"DC.W ${w:04X}"
    
    if w == 0x4E75: desc = "RTS"
    elif 0x4E90 <= w <= 0x4E97: desc = f"JSR (A{w-0x4E90})"
    elif 0x4ED0 <= w <= 0x4ED7: desc = f"JMP (A{w-0x4ED0})"
    elif w == 0x4EB9 and off+4<len(rom):
        desc = f"JSR ${read_long(off+2):08X}"; size = 6
    elif w == 0x4EF9 and off+4<len(rom):
        desc = f"JMP ${read_long(off+2):08X}"; size = 6
    elif (w & 0xF000) == 0x2000:
        sr=w&7; sm=(w>>3)&7; dm=(w>>6)&7; dr=(w>>9)&7
        if sm == 5 and off+2<len(rom):
            d=signed_word(read_word(off+2))
            dn=f"A{dr}" if dm==1 else f"D{dr}" if dm==0 else "?"
            desc=f"MOVE.L ${d&0xFFFF:04X}(A{sr}),{dn}"; size=4
        elif sm==7 and sr==0 and off+2<len(rom):
            a=signed_word(read_word(off+2))
            dn=f"A{dr}" if dm==1 else f"D{dr}" if dm==0 else "?"
            desc=f"MOVE.L ${a&0xFFFF:04X}.W,{dn}"; size=4
        elif sm==0:
            dn=f"A{dr}" if dm==1 else f"D{dr}" if dm==0 else "?"
            desc=f"MOVE.L D{sr},{dn}"
        elif sm==1:
            dn=f"A{dr}" if dm==1 else f"D{dr}" if dm==0 else "?"
            desc=f"MOVE.L A{sr},{dn}"
        elif sm==2:
            dn=f"A{dr}" if dm==1 else f"D{dr}" if dm==0 else "?"
            desc=f"MOVE.L (A{sr}),{dn}"
        elif sm==3:
            dn=f"A{dr}" if dm==1 else f"D{dr}" if dm==0 else "?"
            desc=f"MOVE.L (A{sr})+,{dn}"
    elif (w&0xFFF8)==0x41E8 and off+2<len(rom):
        dr=(w>>9)&7; sr=w&7; d=signed_word(read_word(off+2))
        desc=f"LEA ${d&0xFFFF:04X}(A{sr}),A{dr}"; size=4
    elif (w & 0xF000) == 0x6000:
        cc=(w>>8)&0xF
        cc_names=['BRA','BSR','BHI','BLS','BCC','BCS','BNE','BEQ',
                 'BVC','BVS','BPL','BMI','BGE','BLT','BGT','BLE']
        d8=w&0xFF
        if d8==0 and off+2<len(rom):
            d=signed_word(read_word(off+2)); t=addr+2+d
            desc=f"{cc_names[cc]}.W ${t:06X}"; size=4
        elif d8!=0:
            if d8&0x80: d8-=256
            t=addr+2+d8; desc=f"{cc_names[cc]}.B ${t:06X}"
    elif (w&0xFFF8)==0x4A80:
        desc=f"TST.L D{w&7}"
    elif (w&0xF100)==0x7000:
        desc=f"MOVEQ #${w&0xFF:02X},D{(w>>9)&7}"
    elif (w&0xFFF8)==0x2F08: desc=f"MOVE.L A{w&7},-(A7)"
    elif (w&0xFFF8)==0x2F00: desc=f"MOVE.L D{w&7},-(A7)"
    elif (w&0xFFF8)==0x4E58: desc=f"UNLK A{w&7}"
    elif (w&0xFFF8)==0x48E0 and off+2<len(rom):
        desc=f"MOVEM.L regs,-(A{w&7})  mask=${read_word(off+2):04X}"; size=4
    elif (w&0xFFF8)==0x4CDF and off+2<len(rom):
        desc=f"MOVEM.L (A7)+,regs  mask=${read_word(off+2):04X}"; size=4
    elif w==0x4E73: desc="RTE"
    elif (w&0xFFF0)==0x4E40: desc=f"TRAP #{w&0xF}"
    elif (w&0xFFF8)==0x4280: desc=f"CLR.L D{w&7}"
    elif (w&0xF0F8)==0x50C8 and off+2<len(rom):
        cc=(w>>8)&0xF; r=w&7
        cc_names=['DBT','DBRA','DBHI','DBLS','DBCC','DBCS','DBNE','DBEQ',
                 'DBVC','DBVS','DBPL','DBMI','DBGE','DBLT','DBGT','DBLE']
        d=signed_word(read_word(off+2)); t=addr+2+d
        desc=f"{cc_names[cc]} D{r},${t:06X}"; size=4
    elif (w&0xFFC0)==0x4680: # NOT.L
        m=(w>>3)&7; r=w&7
        if m==0: desc=f"NOT.L D{r}"
    elif (w&0xF1C0)==0xB0BC and off+4<len(rom):
        dr=(w>>9)&7; imm=read_long(off+2)
        desc=f"CMPI.L #${imm:08X},D{dr}"; size=6
    elif (w&0xF1C0)==0xB080:
        sr=w&7; sm=(w>>3)&7; dr=(w>>9)&7
        if sm==0: desc=f"CMP.L D{sr},D{dr}"

    hexbytes=' '.join(f'{b:02X}' for b in rom[off:off+size])
    print(f"  ${addr:06X}: {hexbytes:24s} {desc}")
    off += size
