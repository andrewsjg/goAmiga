#!/usr/bin/env python3
"""Decode 68000 ROM functions around BltBitMap caller - v2 with fixed disassembler."""

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
    """Decode effective address. Returns (string, new_offset)."""
    if mode == 0: return f"D{reg}", off
    if mode == 1: return f"A{reg}", off
    if mode == 2: return f"(A{reg})", off
    if mode == 3: return f"(A{reg})+", off
    if mode == 4: return f"-(A{reg})", off
    if mode == 5:
        disp = s16(rw(off))
        if disp >= 0:
            return f"$%04X(A%d)" % (disp, reg), off+2
        else:
            return f"-$%04X(A%d)" % (-disp, reg), off+2
    if mode == 6:
        ext = rw(off)
        da = 'A' if ext & 0x8000 else 'D'
        xreg = (ext >> 12) & 7
        wl = '.L' if ext & 0x0800 else '.W'
        disp = s8(ext & 0xFF)
        if disp >= 0:
            return f"$%02X(A%d,%s%d%s)" % (disp, reg, da, xreg, wl), off+2
        else:
            return f"-$%02X(A%d,%s%d%s)" % (-disp, reg, da, xreg, wl), off+2
    if mode == 7:
        if reg == 0:
            val = rw(off)
            return f"($%04X).W" % val, off+2
        if reg == 1:
            val = rl(off)
            return f"($%08X)" % val, off+4
        if reg == 2:
            disp = s16(rw(off))
            pc_val = ROM_BASE + off  # PC points to extension word
            target = pc_val + disp
            return f"$%04X(PC) [=$%06X]" % (disp & 0xFFFF, target & 0xFFFFFF), off+2
        if reg == 3:
            ext = rw(off)
            da = 'A' if ext & 0x8000 else 'D'
            xreg = (ext >> 12) & 7
            wl = '.L' if ext & 0x0800 else '.W'
            disp = s8(ext & 0xFF)
            return f"$%02X(PC,%s%d%s)" % (disp & 0xFF, da, xreg, wl), off+2
        if reg == 4:
            if size == 1:
                val = rw(off) & 0xFF
                return f"#$%02X" % val, off+2
            elif size == 2:
                val = rw(off)
                return f"#$%04X" % val, off+2
            else:
                val = rl(off)
                return f"#$%08X" % val, off+4
    return f"???[m={mode},r={reg}]", off

def sz_char(s):
    return ['.B','.W','.L'][s] if 0 <= s <= 2 else ''

def sz_bytes(s):
    return [1,2,4][s] if 0 <= s <= 2 else 2

def regmask_str(mask, predec=False):
    regs = []
    for i in range(16):
        bit = (15 - i) if predec else i
        if mask & (1 << bit):
            regs.append(f"D{i}" if i < 8 else f"A{i-8}")
    return '/'.join(regs) if regs else '(none)'

def disasm_one(off):
    """Disassemble one instruction at ROM offset. Return (text, next_off)."""
    pc = ROM_BASE + off
    w = rw(off)
    nxt = off + 2
    top = (w >> 12) & 0xF
    
    # ---- Group 4: Misc ----
    if w == 0x4E71: return "NOP", nxt
    if w == 0x4E75: return "RTS", nxt
    if w == 0x4E73: return "RTE", nxt
    if w == 0x4E76: return "TRAPV", nxt
    if (w & 0xFFF0) == 0x4E40: return f"TRAP    #{w&0xF}", nxt
    if (w & 0xFFF8) == 0x4E50:
        d = s16(rw(nxt)); return f"LINK    A{w&7},#${d&0xFFFF:04X}", nxt+2
    if (w & 0xFFF8) == 0x4E58: return f"UNLK    A{w&7}", nxt
    if (w & 0xFFF0) == 0x4E60:
        if w & 8: return f"MOVE.L  USP,A{w&7}", nxt
        return f"MOVE.L  A{w&7},USP", nxt
    
    # EXT.W / EXT.L (MUST come before MOVEM/SWAP/PEA)
    if (w & 0xFFF8) == 0x4880: return f"EXT.W   D{w&7}", nxt
    if (w & 0xFFF8) == 0x48C0: return f"EXT.L   D{w&7}", nxt
    # SWAP (MUST come before PEA)
    if (w & 0xFFF8) == 0x4840: return f"SWAP    D{w&7}", nxt

    # JMP
    if (w & 0xFFC0) == 0x4EC0:
        m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,4,nxt); return f"JMP     {ea}", nxt
    # JSR
    if (w & 0xFFC0) == 0x4E80:
        m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,4,nxt); return f"JSR     {ea}", nxt
    # LEA
    if (w & 0xF1C0) == 0x41C0:
        ar=(w>>9)&7; m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,4,nxt)
        return f"LEA     {ea},A{ar}", nxt
    # PEA
    if (w & 0xFFC0) == 0x4840:
        m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,4,nxt); return f"PEA     {ea}", nxt

    # MOVEM to memory (reg→mem): 0100 1000 1S MMMRRR, mode != 0
    if (w & 0xFB80) == 0x4880:
        m=(w>>3)&7
        if m not in (0,):  # mode 0 is EXT (handled above)
            sl = '.L' if w & 0x40 else '.W'
            r=w&7; mask=rw(nxt); nxt+=2
            predec = (m==4)
            ea,nxt = ea_str(m,r,4,nxt)
            return f"MOVEM{sl} {regmask_str(mask,predec)},{ea}", nxt

    # MOVEM from memory (mem→reg): 0100 1100 1S MMMRRR
    if (w & 0xFB80) == 0x4C80:
        sl = '.L' if w & 0x40 else '.W'
        m=(w>>3)&7; r=w&7; mask=rw(nxt); nxt+=2
        sb = 4 if w & 0x40 else 2
        ea,nxt = ea_str(m,r,sb,nxt)
        return f"MOVEM{sl} {ea},{regmask_str(mask,False)}", nxt

    # MOVE to SR/from SR/CCR
    if (w & 0xFFC0) == 0x40C0:
        m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,2,nxt); return f"MOVE.W  SR,{ea}", nxt
    if (w & 0xFFC0) == 0x44C0:
        m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,2,nxt); return f"MOVE.W  {ea},CCR", nxt
    if (w & 0xFFC0) == 0x46C0:
        m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,2,nxt); return f"MOVE.W  {ea},SR", nxt

    # CLR
    if (w & 0xFF00) == 0x4200:
        sz=(w>>6)&3; m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,sz_bytes(sz),nxt)
        return f"CLR{sz_char(sz)}    {ea}", nxt
    # NEG
    if (w & 0xFF00) == 0x4400:
        sz=(w>>6)&3; m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,sz_bytes(sz),nxt)
        return f"NEG{sz_char(sz)}    {ea}", nxt
    # NOT
    if (w & 0xFF00) == 0x4600:
        sz=(w>>6)&3; m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,sz_bytes(sz),nxt)
        return f"NOT{sz_char(sz)}    {ea}", nxt
    # TST
    if (w & 0xFF00) == 0x4A00:
        sz=(w>>6)&3; m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,sz_bytes(sz),nxt)
        return f"TST{sz_char(sz)}    {ea}", nxt
    # NEGX
    if (w & 0xFF00) == 0x4000:
        sz=(w>>6)&3; m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,sz_bytes(sz),nxt)
        return f"NEGX{sz_char(sz)}   {ea}", nxt

    # ---- MOVEQ ----
    if (w & 0xF100) == 0x7000:
        return f"MOVEQ   #${w&0xFF:02X},D{(w>>9)&7}", nxt

    # ---- Bcc / BRA / BSR ----
    if top == 6:
        cond = (w>>8)&0xF; disp8 = w & 0xFF
        if disp8 == 0:
            disp = s16(rw(nxt)); target = pc+2+disp; nxt+=2
        elif disp8 == 0xFF:
            disp = s32(rl(nxt)); target = pc+2+disp; nxt+=4
        else:
            disp = s8(disp8); target = pc+2+disp
        mn = "BRA" if cond==0 else ("BSR" if cond==1 else f"B{CC[cond]}")
        return f"{mn:8s}${target:06X}", nxt

    # ---- DBcc ----
    if (w & 0xF0C0) == 0x50C8:
        cond=(w>>8)&0xF; dr=w&7; disp=s16(rw(nxt)); target=pc+2+disp; nxt+=2
        return f"DB{CC[cond]}    D{dr},${target:06X}", nxt

    # ---- Scc ----
    if (w & 0xF0C0) == 0x50C0 and ((w>>3)&7) != 1:
        cond=(w>>8)&0xF; m=(w>>3)&7; r=w&7
        ea,nxt=ea_str(m,r,1,nxt)
        return f"S{CC[cond]}     {ea}", nxt

    # ---- ADDQ / SUBQ ----
    if top == 5 and ((w >> 6) & 3) != 3:
        sz=(w>>6)&3; data=(w>>9)&7
        if data==0: data=8
        op = "SUBQ" if w & 0x0100 else "ADDQ"
        m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,sz_bytes(sz),nxt)
        return f"{op}{sz_char(sz)}  #{data},{ea}", nxt

    # ---- MOVE / MOVEA ----
    if top in (1, 2, 3):
        sz_map = {1:0, 3:1, 2:2}
        sz = sz_map[top]; sb = sz_bytes(sz)
        src_m=(w>>3)&7; src_r=w&7; dst_r=(w>>9)&7; dst_m=(w>>6)&7
        src,nxt = ea_str(src_m, src_r, sb, nxt)
        dst,nxt = ea_str(dst_m, dst_r, sb, nxt)
        if dst_m == 1:
            return f"MOVEA{sz_char(sz)} {src},{dst}", nxt
        return f"MOVE{sz_char(sz)}  {src},{dst}", nxt

    # ---- ORI / ANDI / SUBI / ADDI / EORI / CMPI ----
    if top == 0 and not (w & 0x0100):
        op_type = (w>>9)&7
        ops = {0:'ORI',1:'ANDI',2:'SUBI',3:'ADDI',5:'EORI',6:'CMPI'}
        if op_type in ops:
            op = ops[op_type]; sz=(w>>6)&3
            if sz == 3:
                return f"DC.W    ${w:04X}", nxt
            sb = sz_bytes(sz)
            if sb == 1:
                imm = rw(nxt) & 0xFF; nxt+=2; imm_s = f"#${imm:02X}"
            elif sb == 2:
                imm = rw(nxt); nxt+=2; imm_s = f"#${imm:04X}"
            else:
                imm = rl(nxt); nxt+=4; imm_s = f"#${imm:08X}"
            m=(w>>3)&7; r=w&7
            if m==7 and r==4:
                if sz==0: return f"{op}     {imm_s},CCR", nxt
                return f"{op}     {imm_s},SR", nxt
            ea,nxt = ea_str(m,r,sb,nxt)
            return f"{op}{sz_char(sz)}   {imm_s},{ea}", nxt

    # ---- BTST/BCHG/BCLR/BSET (register) ----
    if top == 0 and (w & 0x0100) and ((w>>6)&3) != 3:
        bops = ['BTST','BCHG','BCLR','BSET']
        bt = (w>>6)&3; dr=(w>>9)&7; m=(w>>3)&7; r=w&7
        ea,nxt = ea_str(m,r,1,nxt)
        return f"{bops[bt]}    D{dr},{ea}", nxt

    # ---- BTST/BCHG/BCLR/BSET (immediate) ----
    if (w & 0xFF00) == 0x0800:
        bops = ['BTST','BCHG','BCLR','BSET']
        bt=(w>>6)&3; bit=rw(nxt)&0xFF; nxt+=2; m=(w>>3)&7; r=w&7
        ea,nxt = ea_str(m,r,1,nxt)
        return f"{bops[bt]}    #{bit},{ea}", nxt

    # ---- CMP / CMPA / CMPM / EOR ----
    if (w & 0xF000) == 0xB000:
        sz=(w>>6)&3; dr=(w>>9)&7; m=(w>>3)&7; r=w&7
        if sz == 3:
            sz2 = 2 if w & 0x0100 else 1
            ea,nxt = ea_str(m,r,sz_bytes(sz2),nxt)
            return f"CMPA{sz_char(sz2)} {ea},A{dr}", nxt
        if w & 0x0100:
            if m == 1:  # CMPM
                return f"CMPM{sz_char(sz)} (A{r})+,(A{dr})+", nxt
            ea,nxt = ea_str(m,r,sz_bytes(sz),nxt)
            return f"EOR{sz_char(sz)}    D{dr},{ea}", nxt
        ea,nxt = ea_str(m,r,sz_bytes(sz),nxt)
        return f"CMP{sz_char(sz)}    {ea},D{dr}", nxt

    # ---- ADD / ADDA / ADDX ----
    if (w & 0xF000) == 0xD000:
        sz=(w>>6)&3; dr=(w>>9)&7; m=(w>>3)&7; r=w&7
        if sz == 3:
            sz2 = 2 if w & 0x0100 else 1
            ea,nxt = ea_str(m,r,sz_bytes(sz2),nxt)
            return f"ADDA{sz_char(sz2)} {ea},A{dr}", nxt
        if w & 0x0100:
            if m == 0: return f"ADDX{sz_char(sz)} D{r},D{dr}", nxt
            if m == 1: return f"ADDX{sz_char(sz)} -(A{r}),-(A{dr})", nxt
            ea,nxt = ea_str(m,r,sz_bytes(sz),nxt)
            return f"ADD{sz_char(sz)}    D{dr},{ea}", nxt
        ea,nxt = ea_str(m,r,sz_bytes(sz),nxt)
        return f"ADD{sz_char(sz)}    {ea},D{dr}", nxt

    # ---- SUB / SUBA / SUBX ----
    if (w & 0xF000) == 0x9000:
        sz=(w>>6)&3; dr=(w>>9)&7; m=(w>>3)&7; r=w&7
        if sz == 3:
            sz2 = 2 if w & 0x0100 else 1
            ea,nxt = ea_str(m,r,sz_bytes(sz2),nxt)
            return f"SUBA{sz_char(sz2)} {ea},A{dr}", nxt
        if w & 0x0100:
            if m == 0: return f"SUBX{sz_char(sz)} D{r},D{dr}", nxt
            if m == 1: return f"SUBX{sz_char(sz)} -(A{r}),-(A{dr})", nxt
            ea,nxt = ea_str(m,r,sz_bytes(sz),nxt)
            return f"SUB{sz_char(sz)}    D{dr},{ea}", nxt
        ea,nxt = ea_str(m,r,sz_bytes(sz),nxt)
        return f"SUB{sz_char(sz)}    {ea},D{dr}", nxt

    # ---- AND / MULU / MULS / EXG ----
    if (w & 0xF000) == 0xC000:
        sz=(w>>6)&3; dr=(w>>9)&7; m=(w>>3)&7; r=w&7
        if sz == 3:
            ea,nxt = ea_str(m,r,2,nxt)
            if w & 0x0100: return f"MULS.W  {ea},D{dr}", nxt
            return f"MULU.W  {ea},D{dr}", nxt
        if w & 0x0100:
            opmode = (w>>3)&0x1F
            if opmode == 0x08: return f"EXG     D{dr},D{r}", nxt
            if opmode == 0x09: return f"EXG     A{dr},A{r}", nxt
            if opmode == 0x11: return f"EXG     D{dr},A{r}", nxt
            ea,nxt = ea_str(m,r,sz_bytes(sz),nxt)
            return f"AND{sz_char(sz)}    D{dr},{ea}", nxt
        ea,nxt = ea_str(m,r,sz_bytes(sz),nxt)
        return f"AND{sz_char(sz)}    {ea},D{dr}", nxt

    # ---- OR / DIVU / DIVS ----
    if (w & 0xF000) == 0x8000:
        sz=(w>>6)&3; dr=(w>>9)&7; m=(w>>3)&7; r=w&7
        if sz == 3:
            ea,nxt = ea_str(m,r,2,nxt)
            if w & 0x0100: return f"DIVS.W  {ea},D{dr}", nxt
            return f"DIVU.W  {ea},D{dr}", nxt
        if w & 0x0100:
            ea,nxt = ea_str(m,r,sz_bytes(sz),nxt)
            return f"OR{sz_char(sz)}     D{dr},{ea}", nxt
        ea,nxt = ea_str(m,r,sz_bytes(sz),nxt)
        return f"OR{sz_char(sz)}     {ea},D{dr}", nxt

    # ---- Shift/Rotate ----
    if (w & 0xF000) == 0xE000:
        sz=(w>>6)&3
        if sz == 3:
            direction = 'L' if w & 0x0100 else 'R'
            stype = (w>>9)&3; stypes=['AS','LS','ROX','RO']
            m=(w>>3)&7; r=w&7; ea,nxt=ea_str(m,r,2,nxt)
            return f"{stypes[stype]}{direction}.W   {ea}", nxt
        direction = 'L' if w & 0x0100 else 'R'
        stype = (w>>3)&3; stypes=['AS','LS','ROX','RO']
        if w & 0x0020:
            cnt = f"D{(w>>9)&7}"
        else:
            cnt = (w>>9)&7
            if cnt==0: cnt=8
            cnt = f"#{cnt}"
        return f"{stypes[stype]}{direction}{sz_char(sz)}   {cnt},D{w&7}", nxt

    return f"DC.W    ${w:04X}", nxt


def disasm_range(start_off, end_off, label=None):
    if label:
        print(f"\n{'='*72}")
        print(f"  {label}")
        print(f"{'='*72}")
    off = start_off
    lines = []
    while off < end_off:
        pc = ROM_BASE + off
        raw_off = off
        text, next_off = disasm_one(off)
        nbytes = next_off - off
        raw_bytes = ' '.join(f'{ROM[raw_off+i]:02X}{ROM[raw_off+i+1]:02X}' for i in range(0, nbytes, 2))
        line = f"  {pc:06X} [{off:05X}]  {raw_bytes:24s} {text}"
        lines.append(line)
        print(line)
        off = next_off
    return lines


# ============================================================
# TASK 1: Full decode of function context
# ============================================================
print("=" * 72)
print("  TASK 1: Decode around BltBitMap call ($FCA000-$FCA100)")
print("=" * 72)

# First find the likely function start by scanning backward for LINK
# The function at $FCA0EC ends with UNLK A5 / RTS, so it uses LINK A5
# Search backward from ~$A060 for LINK A5
print("\n--- Scanning backward for LINK A5 from $FCA060 ---")
for scan_off in range(0xA060, 0x9B00, -2):
    w = rw(scan_off)
    if w == 0x4E55:  # LINK A5
        disp = s16(rw(scan_off+2))
        pc = ROM_BASE + scan_off
        print(f"  Found LINK A5,#${disp&0xFFFF:04X} at PC=${pc:06X} (offset ${scan_off:05X})")
        if scan_off < 0x9F00:
            break

# Now decode the full BltBitMap public function
# Per repo memory: BltBitMap public = $FC9C08-$FCA0EC
disasm_range(0x9C08, 0xA0EE, "BltBitMap public function ($FC9C08 - $FCA0EC)")

print("\n--- Function end ---")

# Decode the ascending pass sub ($FCA0EE-$FCA218)
disasm_range(0xA0EE, 0xA21A, "BltBitMap ascending pass sub ($FCA0EE)")

# Decode the descending pass sub ($FCA21A-...)
disasm_range(0xA21A, 0xA350, "BltBitMap descending pass sub ($FCA21A)")


# ============================================================
# TASK 2: References to $5C40
# ============================================================
print("\n" + "=" * 72)
print("  TASK 2: Search ROM for references to $5C40")
print("=" * 72)

# Search for 00005C40 (long)
print("\n--- 32-bit $00005C40 references ---")
target = bytes([0x00, 0x00, 0x5C, 0x40])
pos = 0
while True:
    pos = ROM.find(target, pos)
    if pos < 0: break
    pc = ROM_BASE + pos
    print(f"\n  Found at offset ${pos:05X} (PC=${pc:06X}):")
    disasm_range(max(0, pos - 8), min(ROM_SIZE, pos + 10))
    pos += 1

# Also search for $5C4C which is known boot block JSR target  
print("\n--- Also checking $5C4C (known boot block entry) ---")
target2 = bytes([0x00, 0x00, 0x5C, 0x4C])
pos = 0
while True:
    pos = ROM.find(target2, pos)
    if pos < 0: break
    pc = ROM_BASE + pos
    print(f"\n  Found at offset ${pos:05X} (PC=${pc:06X}):")
    disasm_range(max(0, pos - 6), min(ROM_SIZE, pos + 10))
    pos += 1

# Search for JSR $5C40 (4EB9 00005C40)
print("\n--- JSR $00005C40 pattern ---")
jsr_pat = bytes([0x4E, 0xB9, 0x00, 0x00, 0x5C, 0x40])
pos = 0
while True:
    pos = ROM.find(jsr_pat, pos)
    if pos < 0: break
    print(f"  Found at offset ${pos:05X} (PC={ROM_BASE+pos:06X})")
    disasm_range(max(0, pos-4), min(ROM_SIZE, pos+10))
    pos += 1

# Search for MOVE.L #$xxx,$5C40 - look for 5C40 as dest in any instruction
# Also check if there's code that does LEA $5C40,An or MOVE.L An,$5C40
print("\n--- Any word-aligned $5C40 in ROM that isn't just data ---")
tw = bytes([0x5C, 0x40])
pos = 0
count = 0
while True:
    pos = ROM.find(tw, pos)
    if pos < 0: break
    if pos % 2 == 0:
        # Skip if part of 00005C40 (already found)
        if not (pos >= 2 and ROM[pos-2]==0 and ROM[pos-1]==0):
            pc = ROM_BASE + pos
            # Quick context
            if count < 10:
                print(f"\n  $5C40 at offset ${pos:05X} (PC=${pc:06X}):")
                disasm_range(max(0, pos-6), min(ROM_SIZE, pos+6))
            count += 1
    pos += 2
if count > 10:
    print(f"  ... total {count} word-aligned $5C40 occurrences")

# ============================================================
# TASK 3: Loop structure analysis
# ============================================================
print("\n" + "=" * 72)
print("  TASK 3: Loop structure analysis")
print("=" * 72)

# Now that we have the full BltBitMap function decoded, let's look at the 
# structure more carefully. The JSR at $FCA0BA calls $FC5CAC (BltBitMap_inner).
# Let's check all backward branches in the BltBitMap function for loops.

print("\n--- BltBitMap function: all branch targets ---")
branches = []
for off in range(0x9C08, 0xA0EE, 2):
    w = rw(off)
    pc = ROM_BASE + off
    is_branch = False
    target = None
    mnem = ""
    
    if (w & 0xF000) == 0x6000:
        cond = (w>>8)&0xF
        disp8 = w & 0xFF
        if disp8 == 0:
            disp = s16(rw(off+2))
            target = pc + 2 + disp
        elif disp8 == 0xFF:
            disp = s32(rl(off+2))
            target = pc + 2 + disp
        else:
            disp = s8(disp8)
            target = pc + 2 + disp
        mn = "BRA" if cond==0 else ("BSR" if cond==1 else f"B{CC[cond]}")
        is_branch = True
        mnem = mn
    
    if (w & 0xF0C0) == 0x50C8:
        cond = (w>>8)&0xF; dr=w&7
        disp = s16(rw(off+2)); target = pc+2+disp
        mnem = f"DB{CC[cond]}"
        is_branch = True
    
    if is_branch and target is not None:
        direction = "BACK" if target < pc else "FWD"
        if target >= 0xFC9C08 and target <= 0xFCA0EE:
            in_func = " (in BltBitMap)"
        else:
            in_func = ""
        branches.append((pc, target, mnem, direction, in_func))

for pc, target, mn, dirn, inf in branches:
    if dirn == "BACK":
        print(f"  ** {pc:06X}: {mn:8s} -> ${target:06X}  [{dirn}]{inf}")

print("\n--- All backward branches (potential loops) ---")
for pc, target, mn, dirn, inf in branches:
    if dirn == "BACK":
        print(f"  Loop: ${pc:06X} branches back to ${target:06X} via {mn}")
        # Decode the loop body
        tgt_off = target - ROM_BASE
        src_off = pc - ROM_BASE
        if src_off - tgt_off < 0x100:  # Small enough to show
            print(f"  Loop body:")
            disasm_range(tgt_off, src_off + 4)
            print()

# Also look for calls into BltBitMap_inner from BltBitMap
print("\n--- All JSR/BSR calls within BltBitMap ($FC9C08-$FCA0EC) ---")
for off in range(0x9C08, 0xA0EE, 2):
    w = rw(off)
    pc = ROM_BASE + off
    # JSR absolute long
    if w == 0x4EB9:
        addr = rl(off+2)
        print(f"  {pc:06X}: JSR     ($%08X)" % addr)
    # BSR
    if (w & 0xFF00) == 0x6100:
        disp8 = w & 0xFF
        if disp8 == 0:
            disp = s16(rw(off+2)); target = pc+2+disp
        elif disp8 == 0xFF:
            disp = s32(rl(off+2)); target = pc+2+disp
        else:
            disp = s8(disp8); target = pc+2+disp
        print(f"  {pc:06X}: BSR     ${target:06X}")

# Find the specific instruction at $FCA0BA
print("\n--- Instruction at exactly $FCA0BA ---")
off = 0xA0BA
w = rw(off)
print(f"  Raw bytes at $A0BA: {ROM[off]:02X} {ROM[off+1]:02X} {ROM[off+2]:02X} {ROM[off+3]:02X} {ROM[off+4]:02X} {ROM[off+5]:02X}")
text, noff = disasm_one(off)
print(f"  Decoded: {text}")
print(f"  Instruction size: {noff-off} bytes")
print(f"  Return address: PC=${ROM_BASE+noff:06X}")

print("\nDone.")
