#!/usr/bin/env python3
"""Decode callers of BltBitMap to find ClipRect loops."""

import struct

with open('Kick13.rom', 'rb') as f:
    ROM = f.read()

ROM_BASE = 0xFC0000; ROM_SIZE = len(ROM)

def rb(off):
    if 0 <= off < ROM_SIZE: return ROM[off]
    return 0
def rw(off):
    if 0 <= off < ROM_SIZE-1: return (ROM[off]<<8)|ROM[off+1]
    return 0
def rl(off):
    if 0 <= off < ROM_SIZE-3: return (ROM[off]<<24)|(ROM[off+1]<<16)|(ROM[off+2]<<8)|ROM[off+3]
    return 0
def s8(v): return v if v < 128 else v - 256
def s16(v): return v if v < 0x8000 else v - 0x10000
def s32(v): return v if v < 0x80000000 else v - 0x100000000
CC = ['T','F','HI','LS','CC','CS','NE','EQ','VC','VS','PL','MI','GE','LT','GT','LE']
def ea_str(mode, reg, size, off):
    if mode == 0: return f"D{reg}", off
    if mode == 1: return f"A{reg}", off
    if mode == 2: return f"(A{reg})", off
    if mode == 3: return f"(A{reg})+", off
    if mode == 4: return f"-(A{reg})", off
    if mode == 5:
        disp = s16(rw(off))
        if disp >= 0: return f"${disp:04X}(A{reg})", off+2
        return f"-${-disp:04X}(A{reg})", off+2
    if mode == 6:
        ext = rw(off); da='A' if ext&0x8000 else 'D'; xreg=(ext>>12)&7
        wl='.L' if ext&0x0800 else '.W'; disp=s8(ext&0xFF)
        if disp >= 0: return f"${disp:02X}(A{reg},{da}{xreg}{wl})", off+2
        return f"-${-disp:02X}(A{reg},{da}{xreg}{wl})", off+2
    if mode == 7:
        if reg == 0: v=rw(off); return f"(${v:04X}).W", off+2
        if reg == 1: v=rl(off); return f"(${v:08X})", off+4
        if reg == 2:
            d=s16(rw(off));pc=ROM_BASE+off;t=pc+d
            return f"${d&0xFFFF:04X}(PC) [=${t&0xFFFFFF:06X}]", off+2
        if reg == 3:
            ext=rw(off);da='A' if ext&0x8000 else 'D';xr=(ext>>12)&7
            wl='.L' if ext&0x0800 else '.W';d=s8(ext&0xFF)
            return f"${d&0xFF:02X}(PC,{da}{xr}{wl})", off+2
        if reg == 4:
            if size==1: v=rw(off)&0xFF; return f"#${v:02X}", off+2
            elif size==2: v=rw(off); return f"#${v:04X}", off+2
            else: v=rl(off); return f"#${v:08X}", off+4
    return f"???", off
def sz_char(s): return ['.B','.W','.L'][s] if 0<=s<=2 else ''
def sz_bytes(s): return [1,2,4][s] if 0<=s<=2 else 2
def regmask_str(mask, predec=False):
    regs = []
    for i in range(16):
        bit=(15-i) if predec else i
        if mask&(1<<bit): regs.append(f"D{i}" if i<8 else f"A{i-8}")
    return '/'.join(regs) if regs else '(none)'

def disasm_one(off):
    pc=ROM_BASE+off; w=rw(off); n=off+2; top=(w>>12)&0xF
    if w==0x4E71: return "NOP",n
    if w==0x4E75: return "RTS",n
    if w==0x4E73: return "RTE",n
    if (w&0xFFF0)==0x4E40: return f"TRAP    #{w&0xF}",n
    if (w&0xFFF8)==0x4E50: d=s16(rw(n)); return f"LINK    A{w&7},#${d&0xFFFF:04X}",n+2
    if (w&0xFFF8)==0x4E58: return f"UNLK    A{w&7}",n
    if (w&0xFFF0)==0x4E60:
        if w&8: return f"MOVE.L  USP,A{w&7}",n
        return f"MOVE.L  A{w&7},USP",n
    if (w&0xFFF8)==0x4880: return f"EXT.W   D{w&7}",n
    if (w&0xFFF8)==0x48C0: return f"EXT.L   D{w&7}",n
    if (w&0xFFF8)==0x4840: return f"SWAP    D{w&7}",n
    if (w&0xFFC0)==0x4EC0: m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,4,n); return f"JMP     {ea}",n
    if (w&0xFFC0)==0x4E80: m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,4,n); return f"JSR     {ea}",n
    if (w&0xF1C0)==0x41C0: ar=(w>>9)&7;m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,4,n); return f"LEA     {ea},A{ar}",n
    if (w&0xFFC0)==0x4840: m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,4,n); return f"PEA     {ea}",n
    if (w&0xFB80)==0x4880:
        m=(w>>3)&7
        if m!=0:
            sl='.L' if w&0x40 else '.W'; r=w&7; mask=rw(n); n+=2; predec=(m==4)
            ea,n=ea_str(m,r,4,n); return f"MOVEM{sl} {regmask_str(mask,predec)},{ea}",n
    if (w&0xFB80)==0x4C80:
        sl='.L' if w&0x40 else '.W'; m=(w>>3)&7;r=w&7;mask=rw(n);n+=2
        ea,n=ea_str(m,r,4 if w&0x40 else 2,n); return f"MOVEM{sl} {ea},{regmask_str(mask,False)}",n
    if (w&0xFFC0)==0x40C0: m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,2,n); return f"MOVE.W  SR,{ea}",n
    if (w&0xFFC0)==0x44C0: m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,2,n); return f"MOVE.W  {ea},CCR",n
    if (w&0xFFC0)==0x46C0: m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,2,n); return f"MOVE.W  {ea},SR",n
    if (w&0xFF00)==0x4200: sz=(w>>6)&3;m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,sz_bytes(sz),n); return f"CLR{sz_char(sz)}    {ea}",n
    if (w&0xFF00)==0x4400: sz=(w>>6)&3;m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,sz_bytes(sz),n); return f"NEG{sz_char(sz)}    {ea}",n
    if (w&0xFF00)==0x4600: sz=(w>>6)&3;m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,sz_bytes(sz),n); return f"NOT{sz_char(sz)}    {ea}",n
    if (w&0xFF00)==0x4A00: sz=(w>>6)&3;m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,sz_bytes(sz),n); return f"TST{sz_char(sz)}    {ea}",n
    if (w&0xFF00)==0x4000: sz=(w>>6)&3;m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,sz_bytes(sz),n); return f"NEGX{sz_char(sz)}   {ea}",n
    if (w&0xF100)==0x7000: return f"MOVEQ   #${w&0xFF:02X},D{(w>>9)&7}",n
    if top==6:
        c=(w>>8)&0xF;d8=w&0xFF
        if d8==0: d=s16(rw(n));t=pc+2+d;n+=2
        elif d8==0xFF: d=s32(rl(n));t=pc+2+d;n+=4
        else: d=s8(d8);t=pc+2+d
        mn="BRA" if c==0 else ("BSR" if c==1 else f"B{CC[c]}")
        return f"{mn:8s}${t:06X}",n
    if (w&0xF0C0)==0x50C8: c=(w>>8)&0xF;dr=w&7;d=s16(rw(n));t=pc+2+d;n+=2; return f"DB{CC[c]}    D{dr},${t:06X}",n
    if (w&0xF0C0)==0x50C0 and ((w>>3)&7)!=1:
        c=(w>>8)&0xF;m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,1,n); return f"S{CC[c]}     {ea}",n
    if top==5 and ((w>>6)&3)!=3:
        sz=(w>>6)&3;data=(w>>9)&7
        if data==0:data=8
        op="SUBQ" if w&0x100 else "ADDQ";m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,sz_bytes(sz),n)
        return f"{op}{sz_char(sz)}  #{data},{ea}",n
    if top in (1,2,3):
        sz_map={1:0,3:1,2:2};sz=sz_map[top];sb=sz_bytes(sz)
        sm=(w>>3)&7;sr=w&7;dr=(w>>9)&7;dm=(w>>6)&7
        s,n=ea_str(sm,sr,sb,n);d,n=ea_str(dm,dr,sb,n)
        if dm==1: return f"MOVEA{sz_char(sz)} {s},{d}",n
        return f"MOVE{sz_char(sz)}  {s},{d}",n
    if top==0 and not(w&0x100):
        ot=(w>>9)&7;ops={0:'ORI',1:'ANDI',2:'SUBI',3:'ADDI',5:'EORI',6:'CMPI'}
        if ot in ops:
            op=ops[ot];sz=(w>>6)&3
            if sz==3: return f"DC.W    ${w:04X}",n
            sb=sz_bytes(sz)
            if sb==1: im=rw(n)&0xFF;n+=2;ims=f"#${im:02X}"
            elif sb==2: im=rw(n);n+=2;ims=f"#${im:04X}"
            else: im=rl(n);n+=4;ims=f"#${im:08X}"
            m=(w>>3)&7;r=w&7
            if m==7 and r==4: return f"{op}     {ims},{'CCR' if sz==0 else 'SR'}",n
            ea,n=ea_str(m,r,sb,n); return f"{op}{sz_char(sz)}   {ims},{ea}",n
    if top==0 and (w&0x100) and ((w>>6)&3)!=3:
        bops=['BTST','BCHG','BCLR','BSET'];bt=(w>>6)&3;dr=(w>>9)&7;m=(w>>3)&7;r=w&7
        ea,n=ea_str(m,r,1,n); return f"{bops[bt]}    D{dr},{ea}",n
    if (w&0xFF00)==0x0800:
        bops=['BTST','BCHG','BCLR','BSET'];bt=(w>>6)&3;bit=rw(n)&0xFF;n+=2;m=(w>>3)&7;r=w&7
        ea,n=ea_str(m,r,1,n); return f"{bops[bt]}    #{bit},{ea}",n
    if (w&0xF000)==0xB000:
        sz=(w>>6)&3;dr=(w>>9)&7;m=(w>>3)&7;r=w&7
        if sz==3: s2=2 if w&0x100 else 1;ea,n=ea_str(m,r,sz_bytes(s2),n); return f"CMPA{sz_char(s2)} {ea},A{dr}",n
        if w&0x100:
            if m==1: return f"CMPM{sz_char(sz)} (A{r})+,(A{dr})+",n
            ea,n=ea_str(m,r,sz_bytes(sz),n); return f"EOR{sz_char(sz)}    D{dr},{ea}",n
        ea,n=ea_str(m,r,sz_bytes(sz),n); return f"CMP{sz_char(sz)}    {ea},D{dr}",n
    if (w&0xF000)==0xD000:
        sz=(w>>6)&3;dr=(w>>9)&7;m=(w>>3)&7;r=w&7
        if sz==3: s2=2 if w&0x100 else 1;ea,n=ea_str(m,r,sz_bytes(s2),n); return f"ADDA{sz_char(s2)} {ea},A{dr}",n
        if w&0x100:
            if m==0: return f"ADDX{sz_char(sz)} D{r},D{dr}",n
            if m==1: return f"ADDX{sz_char(sz)} -(A{r}),-(A{dr})",n
            ea,n=ea_str(m,r,sz_bytes(sz),n); return f"ADD{sz_char(sz)}    D{dr},{ea}",n
        ea,n=ea_str(m,r,sz_bytes(sz),n); return f"ADD{sz_char(sz)}    {ea},D{dr}",n
    if (w&0xF000)==0x9000:
        sz=(w>>6)&3;dr=(w>>9)&7;m=(w>>3)&7;r=w&7
        if sz==3: s2=2 if w&0x100 else 1;ea,n=ea_str(m,r,sz_bytes(s2),n); return f"SUBA{sz_char(s2)} {ea},A{dr}",n
        if w&0x100:
            if m==0: return f"SUBX{sz_char(sz)} D{r},D{dr}",n
            if m==1: return f"SUBX{sz_char(sz)} -(A{r}),-(A{dr})",n
            ea,n=ea_str(m,r,sz_bytes(sz),n); return f"SUB{sz_char(sz)}    D{dr},{ea}",n
        ea,n=ea_str(m,r,sz_bytes(sz),n); return f"SUB{sz_char(sz)}    {ea},D{dr}",n
    if (w&0xF000)==0xC000:
        sz=(w>>6)&3;dr=(w>>9)&7;m=(w>>3)&7;r=w&7
        if sz==3:
            ea,n=ea_str(m,r,2,n)
            if w&0x100: return f"MULS.W  {ea},D{dr}",n
            return f"MULU.W  {ea},D{dr}",n
        if w&0x100:
            om=(w>>3)&0x1F
            if om==0x08: return f"EXG     D{dr},D{r}",n
            if om==0x09: return f"EXG     A{dr},A{r}",n
            if om==0x11: return f"EXG     D{dr},A{r}",n
            ea,n=ea_str(m,r,sz_bytes(sz),n); return f"AND{sz_char(sz)}    D{dr},{ea}",n
        ea,n=ea_str(m,r,sz_bytes(sz),n); return f"AND{sz_char(sz)}    {ea},D{dr}",n
    if (w&0xF000)==0x8000:
        sz=(w>>6)&3;dr=(w>>9)&7;m=(w>>3)&7;r=w&7
        if sz==3:
            ea,n=ea_str(m,r,2,n)
            if w&0x100: return f"DIVS.W  {ea},D{dr}",n
            return f"DIVU.W  {ea},D{dr}",n
        if w&0x100: ea,n=ea_str(m,r,sz_bytes(sz),n); return f"OR{sz_char(sz)}     D{dr},{ea}",n
        ea,n=ea_str(m,r,sz_bytes(sz),n); return f"OR{sz_char(sz)}     {ea},D{dr}",n
    if (w&0xF000)==0xE000:
        sz=(w>>6)&3
        if sz==3:
            dir='L' if w&0x100 else 'R';st=(w>>9)&3;sts=['AS','LS','ROX','RO']
            m=(w>>3)&7;r=w&7;ea,n=ea_str(m,r,2,n); return f"{sts[st]}{dir}.W   {ea}",n
        dir='L' if w&0x100 else 'R';st=(w>>3)&3;sts=['AS','LS','ROX','RO']
        if w&0x20: cnt=f"D{(w>>9)&7}"
        else: c=(w>>9)&7;c=8 if c==0 else c;cnt=f"#{c}"
        return f"{sts[st]}{dir}{sz_char(sz)}   {cnt},D{w&7}",n
    return f"DC.W    ${w:04X}",n

def disasm_range(start_off, end_off, label=None):
    if label:
        print(f"\n{'='*72}")
        print(f"  {label}")
        print(f"{'='*72}")
    off = start_off
    while off < end_off:
        pc = ROM_BASE + off; raw_off = off
        text, next_off = disasm_one(off)
        nbytes = next_off - off
        raw = ' '.join(f'{ROM[raw_off+i]:02X}{ROM[raw_off+i+1]:02X}' for i in range(0, nbytes, 2))
        print(f"  {pc:06X} [{off:05X}]  {raw:24s} {text}")
        off = next_off

# Decode the BltClear function that calls BltBitMap at $FC5C8A
# Context around $FC5C8A (it's inside BltClear $FC5BCA-$FC5C6E, but might extend)
disasm_range(0x5BCA, 0x5CAC, "BltClear / BltBitMap wrapper ($FC5BCA)")

# Decode the caller at $FCEFA6 with broad context to find its loops
# Find the LINK before $FCEFA6
print("\n--- Scanning for LINK before $FCEFA6 ---")
for scan_off in range(0xEFA0, 0xEE00, -2):
    w = rw(scan_off)
    if (w & 0xFFF8) == 0x4E50:  # LINK An
        d = s16(rw(scan_off+2))
        pc = ROM_BASE + scan_off
        print(f"  Found LINK A{w&7},#${d&0xFFFF:04X} at PC=${pc:06X}")
        break

# Find the RTS after $FCEFA6
for scan_off in range(0xEFAC, 0xF200, 2):
    w = rw(scan_off)
    if w == 0x4E75:
        pc = ROM_BASE + scan_off
        print(f"  Found RTS at PC=${pc:06X}")
        break

# Decode the full function containing the $FCEFA6 call
disasm_range(0xEF2E, 0xF0B0, "ClipBlit or similar ($FCEF2E-$FCF0B0)")

print("\nDone.")
