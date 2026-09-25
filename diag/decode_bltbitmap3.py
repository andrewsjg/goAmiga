#!/usr/bin/env python3
"""Decode BltBitMap_inner ($FC5CAC-$FC5E2A) to find the per-plane loop."""

import struct

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
        ext = rw(off)
        da = 'A' if ext & 0x8000 else 'D'
        xreg = (ext >> 12) & 7
        wl = '.L' if ext & 0x0800 else '.W'
        disp = s8(ext & 0xFF)
        if disp >= 0: return f"${disp:02X}(A{reg},{da}{xreg}{wl})", off+2
        return f"-${-disp:02X}(A{reg},{da}{xreg}{wl})", off+2
    if mode == 7:
        if reg == 0: val = rw(off); return f"(${val:04X}).W", off+2
        if reg == 1: val = rl(off); return f"(${val:08X})", off+4
        if reg == 2:
            disp = s16(rw(off)); pc_val = ROM_BASE + off; target = pc_val + disp
            return f"${disp&0xFFFF:04X}(PC) [=${target&0xFFFFFF:06X}]", off+2
        if reg == 3:
            ext = rw(off); da = 'A' if ext & 0x8000 else 'D'
            xreg = (ext >> 12) & 7; wl = '.L' if ext & 0x0800 else '.W'
            disp = s8(ext & 0xFF)
            return f"${disp&0xFF:02X}(PC,{da}{xreg}{wl})", off+2
        if reg == 4:
            if size == 1: val = rw(off) & 0xFF; return f"#${val:02X}", off+2
            elif size == 2: val = rw(off); return f"#${val:04X}", off+2
            else: val = rl(off); return f"#${val:08X}", off+4
    return f"???[m={mode},r={reg}]", off

def sz_char(s): return ['.B','.W','.L'][s] if 0<=s<=2 else ''
def sz_bytes(s): return [1,2,4][s] if 0<=s<=2 else 2

def regmask_str(mask, predec=False):
    regs = []
    for i in range(16):
        bit = (15-i) if predec else i
        if mask & (1<<bit): regs.append(f"D{i}" if i<8 else f"A{i-8}")
    return '/'.join(regs) if regs else '(none)'

def disasm_one(off):
    pc = ROM_BASE + off; w = rw(off); nxt = off + 2; top = (w>>12)&0xF
    if w == 0x4E71: return "NOP", nxt
    if w == 0x4E75: return "RTS", nxt
    if w == 0x4E73: return "RTE", nxt
    if (w&0xFFF0)==0x4E40: return f"TRAP    #{w&0xF}", nxt
    if (w&0xFFF8)==0x4E50: d=s16(rw(nxt)); return f"LINK    A{w&7},#${d&0xFFFF:04X}", nxt+2
    if (w&0xFFF8)==0x4E58: return f"UNLK    A{w&7}", nxt
    if (w&0xFFF0)==0x4E60:
        if w&8: return f"MOVE.L  USP,A{w&7}", nxt
        return f"MOVE.L  A{w&7},USP", nxt
    if (w&0xFFF8)==0x4880: return f"EXT.W   D{w&7}", nxt
    if (w&0xFFF8)==0x48C0: return f"EXT.L   D{w&7}", nxt
    if (w&0xFFF8)==0x4840: return f"SWAP    D{w&7}", nxt
    if (w&0xFFC0)==0x4EC0: m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,4,nxt); return f"JMP     {ea}", nxt
    if (w&0xFFC0)==0x4E80: m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,4,nxt); return f"JSR     {ea}", nxt
    if (w&0xF1C0)==0x41C0: ar=(w>>9)&7;m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,4,nxt); return f"LEA     {ea},A{ar}", nxt
    if (w&0xFFC0)==0x4840: m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,4,nxt); return f"PEA     {ea}", nxt
    if (w&0xFB80)==0x4880:
        m=(w>>3)&7
        if m!=0:
            sl='.L' if w&0x40 else '.W'; r=w&7; mask=rw(nxt); nxt+=2; predec=(m==4)
            ea,nxt=ea_str(m,r,4,nxt); return f"MOVEM{sl} {regmask_str(mask,predec)},{ea}", nxt
    if (w&0xFB80)==0x4C80:
        sl='.L' if w&0x40 else '.W'; m=(w>>3)&7;r=w&7;mask=rw(nxt);nxt+=2
        ea,nxt=ea_str(m,r,4 if w&0x40 else 2,nxt); return f"MOVEM{sl} {ea},{regmask_str(mask,False)}", nxt
    if (w&0xFFC0)==0x40C0: m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,2,nxt); return f"MOVE.W  SR,{ea}", nxt
    if (w&0xFFC0)==0x44C0: m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,2,nxt); return f"MOVE.W  {ea},CCR", nxt
    if (w&0xFFC0)==0x46C0: m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,2,nxt); return f"MOVE.W  {ea},SR", nxt
    if (w&0xFF00)==0x4200: sz=(w>>6)&3;m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"CLR{sz_char(sz)}    {ea}", nxt
    if (w&0xFF00)==0x4400: sz=(w>>6)&3;m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"NEG{sz_char(sz)}    {ea}", nxt
    if (w&0xFF00)==0x4600: sz=(w>>6)&3;m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"NOT{sz_char(sz)}    {ea}", nxt
    if (w&0xFF00)==0x4A00: sz=(w>>6)&3;m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"TST{sz_char(sz)}    {ea}", nxt
    if (w&0xFF00)==0x4000: sz=(w>>6)&3;m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"NEGX{sz_char(sz)}   {ea}", nxt
    if (w&0xF100)==0x7000: return f"MOVEQ   #${w&0xFF:02X},D{(w>>9)&7}", nxt
    if top==6:
        cond=(w>>8)&0xF; disp8=w&0xFF
        if disp8==0: disp=s16(rw(nxt));target=pc+2+disp;nxt+=2
        elif disp8==0xFF: disp=s32(rl(nxt));target=pc+2+disp;nxt+=4
        else: disp=s8(disp8);target=pc+2+disp
        mn="BRA" if cond==0 else ("BSR" if cond==1 else f"B{CC[cond]}")
        return f"{mn:8s}${target:06X}", nxt
    if (w&0xF0C0)==0x50C8: cond=(w>>8)&0xF;dr=w&7;disp=s16(rw(nxt));target=pc+2+disp;nxt+=2; return f"DB{CC[cond]}    D{dr},${target:06X}", nxt
    if (w&0xF0C0)==0x50C0 and ((w>>3)&7)!=1:
        cond=(w>>8)&0xF;m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,1,nxt); return f"S{CC[cond]}     {ea}", nxt
    if top==5 and ((w>>6)&3)!=3:
        sz=(w>>6)&3;data=(w>>9)&7
        if data==0:data=8
        op="SUBQ" if w&0x0100 else "ADDQ"; m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,sz_bytes(sz),nxt)
        return f"{op}{sz_char(sz)}  #{data},{ea}", nxt
    if top in (1,2,3):
        sz_map={1:0,3:1,2:2};sz=sz_map[top];sb=sz_bytes(sz)
        src_m=(w>>3)&7;src_r=w&7;dst_r=(w>>9)&7;dst_m=(w>>6)&7
        src,nxt=ea_str(src_m,src_r,sb,nxt);dst,nxt=ea_str(dst_m,dst_r,sb,nxt)
        if dst_m==1: return f"MOVEA{sz_char(sz)} {src},{dst}", nxt
        return f"MOVE{sz_char(sz)}  {src},{dst}", nxt
    if top==0 and not (w&0x0100):
        op_type=(w>>9)&7; ops={0:'ORI',1:'ANDI',2:'SUBI',3:'ADDI',5:'EORI',6:'CMPI'}
        if op_type in ops:
            op=ops[op_type];sz=(w>>6)&3
            if sz==3: return f"DC.W    ${w:04X}", nxt
            sb=sz_bytes(sz)
            if sb==1: imm=rw(nxt)&0xFF;nxt+=2;imm_s=f"#${imm:02X}"
            elif sb==2: imm=rw(nxt);nxt+=2;imm_s=f"#${imm:04X}"
            else: imm=rl(nxt);nxt+=4;imm_s=f"#${imm:08X}"
            m=(w>>3)&7;r=w&7
            if m==7 and r==4:
                if sz==0: return f"{op}     {imm_s},CCR", nxt
                return f"{op}     {imm_s},SR", nxt
            ea,nxt=ea_str(m,r,sb,nxt); return f"{op}{sz_char(sz)}   {imm_s},{ea}", nxt
    if top==0 and (w&0x0100) and ((w>>6)&3)!=3:
        bops=['BTST','BCHG','BCLR','BSET'];bt=(w>>6)&3;dr=(w>>9)&7;m=(w>>3)&7;r=w&7
        ea,nxt=ea_str(m,r,1,nxt); return f"{bops[bt]}    D{dr},{ea}", nxt
    if (w&0xFF00)==0x0800:
        bops=['BTST','BCHG','BCLR','BSET'];bt=(w>>6)&3;bit=rw(nxt)&0xFF;nxt+=2;m=(w>>3)&7;r=w&7
        ea,nxt=ea_str(m,r,1,nxt); return f"{bops[bt]}    #{bit},{ea}", nxt
    if (w&0xF000)==0xB000:
        sz=(w>>6)&3;dr=(w>>9)&7;m=(w>>3)&7;r=w&7
        if sz==3: sz2=2 if w&0x0100 else 1;ea,nxt=ea_str(m,r,sz_bytes(sz2),nxt); return f"CMPA{sz_char(sz2)} {ea},A{dr}", nxt
        if w&0x0100:
            if m==1: return f"CMPM{sz_char(sz)} (A{r})+,(A{dr})+", nxt
            ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"EOR{sz_char(sz)}    D{dr},{ea}", nxt
        ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"CMP{sz_char(sz)}    {ea},D{dr}", nxt
    if (w&0xF000)==0xD000:
        sz=(w>>6)&3;dr=(w>>9)&7;m=(w>>3)&7;r=w&7
        if sz==3: sz2=2 if w&0x0100 else 1;ea,nxt=ea_str(m,r,sz_bytes(sz2),nxt); return f"ADDA{sz_char(sz2)} {ea},A{dr}", nxt
        if w&0x0100:
            if m==0: return f"ADDX{sz_char(sz)} D{r},D{dr}", nxt
            if m==1: return f"ADDX{sz_char(sz)} -(A{r}),-(A{dr})", nxt
            ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"ADD{sz_char(sz)}    D{dr},{ea}", nxt
        ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"ADD{sz_char(sz)}    {ea},D{dr}", nxt
    if (w&0xF000)==0x9000:
        sz=(w>>6)&3;dr=(w>>9)&7;m=(w>>3)&7;r=w&7
        if sz==3: sz2=2 if w&0x0100 else 1;ea,nxt=ea_str(m,r,sz_bytes(sz2),nxt); return f"SUBA{sz_char(sz2)} {ea},A{dr}", nxt
        if w&0x0100:
            if m==0: return f"SUBX{sz_char(sz)} D{r},D{dr}", nxt
            if m==1: return f"SUBX{sz_char(sz)} -(A{r}),-(A{dr})", nxt
            ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"SUB{sz_char(sz)}    D{dr},{ea}", nxt
        ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"SUB{sz_char(sz)}    {ea},D{dr}", nxt
    if (w&0xF000)==0xC000:
        sz=(w>>6)&3;dr=(w>>9)&7;m=(w>>3)&7;r=w&7
        if sz==3:
            ea,nxt=ea_str(m,r,2,nxt)
            if w&0x0100: return f"MULS.W  {ea},D{dr}", nxt
            return f"MULU.W  {ea},D{dr}", nxt
        if w&0x0100:
            opmode=(w>>3)&0x1F
            if opmode==0x08: return f"EXG     D{dr},D{r}", nxt
            if opmode==0x09: return f"EXG     A{dr},A{r}", nxt
            if opmode==0x11: return f"EXG     D{dr},A{r}", nxt
            ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"AND{sz_char(sz)}    D{dr},{ea}", nxt
        ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"AND{sz_char(sz)}    {ea},D{dr}", nxt
    if (w&0xF000)==0x8000:
        sz=(w>>6)&3;dr=(w>>9)&7;m=(w>>3)&7;r=w&7
        if sz==3:
            ea,nxt=ea_str(m,r,2,nxt)
            if w&0x0100: return f"DIVS.W  {ea},D{dr}", nxt
            return f"DIVU.W  {ea},D{dr}", nxt
        if w&0x0100: ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"OR{sz_char(sz)}     D{dr},{ea}", nxt
        ea,nxt=ea_str(m,r,sz_bytes(sz),nxt); return f"OR{sz_char(sz)}     {ea},D{dr}", nxt
    if (w&0xF000)==0xE000:
        sz=(w>>6)&3
        if sz==3:
            direction='L' if w&0x0100 else 'R';stype=(w>>9)&3;stypes=['AS','LS','ROX','RO']
            m=(w>>3)&7;r=w&7;ea,nxt=ea_str(m,r,2,nxt); return f"{stypes[stype]}{direction}.W   {ea}", nxt
        direction='L' if w&0x0100 else 'R';stype=(w>>3)&3;stypes=['AS','LS','ROX','RO']
        if w&0x0020: cnt=f"D{(w>>9)&7}"
        else: cnt=(w>>9)&7;cnt=8 if cnt==0 else cnt;cnt=f"#{cnt}"
        return f"{stypes[stype]}{direction}{sz_char(sz)}   {cnt},D{w&7}", nxt
    return f"DC.W    ${w:04X}", nxt


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


# Decode BltBitMap_inner ($FC5CAC to $FC5E2A+)
disasm_range(0x5CAC, 0x5E30, "BltBitMap_inner ($FC5CAC - $FC5E2A)")

# Now find what calls the pass subs ($FCA0EE, $FCA21A)
print("\n" + "=" * 72)
print("  Searching for calls to pass sub asc ($FCA0EE) and desc ($FCA21A)")
print("=" * 72)

for off_range in [(0x5CAC, 0x5E30)]:
    off = off_range[0]
    while off < off_range[1]:
        text, nxt = disasm_one(off)
        pc = ROM_BASE + off
        if 'FCA0EE' in text or 'FCA21A' in text or 'JSR' in text or 'BSR' in text:
            raw = ' '.join(f'{ROM[off+i]:02X}{ROM[off+i+1]:02X}' for i in range(0, nxt-off, 2))
            print(f"  {pc:06X}  {raw:24s} {text}")
        off = nxt

# Also decode the helper at $FC9B38 (called from BltBitMap via BSR)
disasm_range(0x9B38, 0x9C08, "Helper function at $FC9B38 (temp buffer alloc?)")

# Decode the function that CALLS BltBitMap public
# Search for who calls $FC9C08
print("\n" + "=" * 72)
print("  Searching for callers of BltBitMap ($FC9C08)")
print("=" * 72)

# Walk instruction by instruction through ROM looking for calls
# This is slow but accurate for BSR
for scan_start, scan_end in [(0x0000, 0x9C08), (0xA0EE, ROM_SIZE-10)]:
    off = scan_start
    while off < scan_end:
        w = rw(off)
        pc = ROM_BASE + off
        # JSR absolute long
        if w == 0x4EB9 and off+6 <= ROM_SIZE:
            addr = rl(off+2)
            if addr == 0xFC9C08 or addr == 0x009C08:
                print(f"  JSR at PC=${pc:06X} -> ${addr:08X}")
                disasm_range(max(0, off-8), min(ROM_SIZE, off+12))
                print()
        # BSR word
        if (w & 0xFF00) == 0x6100:
            disp8 = w & 0xFF
            if disp8 == 0:
                disp = s16(rw(off+2)); target = pc+2+disp
                if target == 0xFC9C08:
                    print(f"  BSR at PC=${pc:06X} -> ${target:06X}")
                    disasm_range(max(0, off-8), min(ROM_SIZE, off+8))
                    print()
            elif disp8 != 0xFF:
                target = pc + 2 + s8(disp8)
                if target == 0xFC9C08:
                    print(f"  BSR at PC=${pc:06X} -> ${target:06X}")
                    disasm_range(max(0, off-8), min(ROM_SIZE, off+8))
                    print()
        off += 2  # This is approximate but catches all word-aligned calls

# Also look at the Amiga library jump table - BltBitMap might be called indirectly
# through a jump vector like JSR -xxx(A6) where A6 is GfxBase
# The graphics.library LVO for BltBitMap is -$001E (decimal -30)
# Actually it's through the jump table. Let's look at what's at typical LVO offsets
print("\n--- Indirect calls via library vectors ---")
print("BltBitMap is graphics.library function. LVO = -$001E = -30")
print("Typically called as JSR -$001E(A6) where A6 = GfxBase")
# Search for 4EAE FFE2 (JSR -$001E(A6)) — that's the BltBitMap LVO
bltbitmap_lvo = bytes([0x4E, 0xAE, 0xFF, 0xE2])  # JSR -$001E(A6)
pos = 0
count = 0
while True:
    pos = ROM.find(bltbitmap_lvo, pos)
    if pos < 0: break
    if pos % 2 == 0:
        pc = ROM_BASE + pos
        print(f"\n  JSR -$001E(A6) at PC=${pc:06X}:")
        disasm_range(max(0, pos-12), min(ROM_SIZE, pos+8))
        count += 1
    pos += 2
    if count > 10:
        print("  ... truncated")
        break

print(f"\n  Total {count} JSR -$001E(A6) calls found")

print("\nDone.")
