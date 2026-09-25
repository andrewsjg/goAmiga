#!/usr/bin/env python3
"""Decode 68000 machine code from Kickstart 1.3 ROM - corrected version."""

import struct

ROM_PATH = "/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom"
ROM_BASE = 0xFC0000

with open(ROM_PATH, "rb") as f:
    rom = f.read()

def rw(o):
    if 0 <= o and o+2 <= len(rom): return struct.unpack_from(">H", rom, o)[0]
    return None
def rl(o):
    if 0 <= o and o+4 <= len(rom): return struct.unpack_from(">I", rom, o)[0]
    return None
def rb(o):
    if 0 <= o < len(rom): return rom[o]
    return None
def s8(v): return v-256 if v>=128 else v
def s16(v): return v-0x10000 if v>=0x8000 else v
def s32(v): return v-0x100000000 if v>=0x80000000 else v

CC = {0:"T",1:"F",2:"HI",3:"LS",4:"CC",5:"CS",6:"NE",7:"EQ",8:"VC",9:"VS",10:"PL",11:"MI",12:"GE",13:"LT",14:"GT",15:"LE"}
SZ = {0:".B",1:".W",2:".L"}

def ea(mode, reg, off, size=1):
    if mode==0: return f"D{reg}",off,0
    if mode==1: return f"A{reg}",off,0
    if mode==2: return f"(A{reg})",off,0
    if mode==3: return f"(A{reg})+",off,0
    if mode==4: return f"-(A{reg})",off,0
    if mode==5:
        w=rw(off);
        if w is None: return "???",off,0
        d=s16(w); return f"${w:04X}(A{reg})",off+2,1
    if mode==6:
        w=rw(off)
        if w is None: return "???",off,0
        xr=(w>>12)&7; xt="A" if w&0x8000 else "D"; xs=".L" if w&0x0800 else ".W"
        d8=s8(w&0xFF); return f"${w&0xFF:02X}(A{reg},{xt}{xr}{xs})",off+2,1
    if mode==7:
        if reg==0:
            w=rw(off);
            if w is None: return "???",off,0
            a=s16(w)&0xFFFFFF; return f"${a:06X}.W",off+2,1
        if reg==1:
            l=rl(off);
            if l is None: return "???",off,0
            return f"${l:08X}",off+4,2
        if reg==2:
            w=rw(off)
            if w is None: return "???",off,0
            d=s16(w); pc=ROM_BASE+off; t=(pc+d)&0xFFFFFF
            return f"${t:06X}(PC)",off+2,1
        if reg==3:
            w=rw(off)
            if w is None: return "???",off,0
            xr=(w>>12)&7; xt="A" if w&0x8000 else "D"; xs=".L" if w&0x0800 else ".W"
            return f"${w&0xFF:02X}(PC,{xt}{xr}{xs})",off+2,1
        if reg==4:
            if size==0:
                w=rw(off);
                if w is None: return "???",off,0
                return f"#${w&0xFF:02X}",off+2,1
            elif size==1:
                w=rw(off);
                if w is None: return "???",off,0
                return f"#${w:04X}",off+2,1
            else:
                l=rl(off);
                if l is None: return "???",off,0
                return f"#${l:08X}",off+4,2
    return f"???[m={mode},r={reg}]",off,0

def reglist(mask, reverse=False):
    regs=[]
    if reverse:
        for i in range(16):
            if mask&(1<<i):
                regs.append(f"A{7-i}" if i<8 else f"D{15-i}")
    else:
        for i in range(16):
            if mask&(1<<i):
                regs.append(f"D{i}" if i<8 else f"A{i-8}")
    # Compress
    parts=[]; i=0
    while i<len(regs):
        s=regs[i]; j=i
        while j+1<len(regs) and regs[j][0]==regs[j+1][0] and int(regs[j+1][1])==int(regs[j][1])+1:
            j+=1
        parts.append(f"{s}-{regs[j]}" if j>i else s)
        i=j+1
    return "/".join(parts) if parts else "???"

def dis(offset):
    """Decode one instruction. Returns (text, next_offset)"""
    pc = ROM_BASE + offset
    op = rw(offset)
    if op is None: return "  ???",offset+2
    off = offset+2
    top = (op>>12)&0xF
    
    # EXT (must come before MOVEM since they overlap)
    if (op&0xFFF8)==0x4880: return f"  EXT.W D{op&7}",off
    if (op&0xFFF8)==0x48C0: return f"  EXT.L D{op&7}",off
    
    # SWAP
    if (op&0xFFF8)==0x4840: return f"  SWAP D{op&7}",off
    
    # MOVEM
    if (op&0xFB80)==0x4880:
        d=(op>>10)&1; sz=".L" if op&0x0040 else ".W"
        m=(op>>3)&7; r=op&7
        mask=rw(off)
        if mask is None: return f"  MOVEM{sz} ???",off
        off+=2
        e,off,_=ea(m,r,off,2 if sz==".L" else 1)
        rl_s=reglist(mask,reverse=(m==4))
        return (f"  MOVEM{sz} {rl_s},{e}" if d==0 else f"  MOVEM{sz} {e},{rl_s}"),off
    
    # LINK
    if (op&0xFFF8)==0x4E50:
        r=op&7; d=rw(off)
        if d is None: return f"  LINK A{r},???",off
        off+=2; return f"  LINK A{r},#${d:04X}  ; {s16(d)}",off
    
    # UNLK
    if (op&0xFFF8)==0x4E58: return f"  UNLK A{op&7}",off
    
    # RTS/RTE/NOP
    if op==0x4E75: return "  RTS",off
    if op==0x4E73: return "  RTE",off
    if op==0x4E71: return "  NOP",off
    
    # TRAP
    if (op&0xFFF0)==0x4E40: return f"  TRAP #{op&0xF}",off
    
    # MOVE USP
    if (op&0xFFF0)==0x4E60:
        d=(op>>3)&1; r=op&7
        return (f"  MOVE USP,A{r}" if d else f"  MOVE A{r},USP"),off
    
    # JSR
    if (op&0xFFC0)==0x4E80:
        m=(op>>3)&7; r=op&7; e,off,_=ea(m,r,off,2)
        return f"  JSR {e}",off
    
    # JMP
    if (op&0xFFC0)==0x4EC0:
        m=(op>>3)&7; r=op&7; e,off,_=ea(m,r,off,2)
        return f"  JMP {e}",off
    
    # LEA
    if (op&0xF1C0)==0x41C0:
        ar=(op>>9)&7; m=(op>>3)&7; r=op&7
        e,off,_=ea(m,r,off,2)
        return f"  LEA {e},A{ar}",off
    
    # PEA
    if (op&0xFFC0)==0x4840:
        m=(op>>3)&7; r=op&7; e,off,_=ea(m,r,off,2)
        return f"  PEA {e}",off
    
    # CLR
    if (op&0xFF00)==0x4200:
        sb=(op>>6)&3
        if sb<3:
            m=(op>>3)&7; r=op&7; e,off,_=ea(m,r,off,sb)
            return f"  CLR{SZ[sb]} {e}",off
    
    # NEG
    if (op&0xFF00)==0x4400:
        sb=(op>>6)&3
        if sb<3:
            m=(op>>3)&7; r=op&7; e,off,_=ea(m,r,off,sb)
            return f"  NEG{SZ[sb]} {e}",off
    
    # NEGX
    if (op&0xFF00)==0x4000:
        sb=(op>>6)&3
        if sb<3:
            m=(op>>3)&7; r=op&7; e,off,_=ea(m,r,off,sb)
            return f"  NEGX{SZ[sb]} {e}",off
    
    # NOT
    if (op&0xFF00)==0x4600:
        sb=(op>>6)&3
        if sb<3:
            m=(op>>3)&7; r=op&7; e,off,_=ea(m,r,off,sb)
            return f"  NOT{SZ[sb]} {e}",off
    
    # TST
    if (op&0xFF00)==0x4A00:
        sb=(op>>6)&3
        if sb<3:
            m=(op>>3)&7; r=op&7; e,off,_=ea(m,r,off,sb)
            return f"  TST{SZ[sb]} {e}",off
    
    # MOVE SR/CCR
    if (op&0xFFC0)==0x40C0:
        m=(op>>3)&7; r=op&7; e,off,_=ea(m,r,off,1)
        return f"  MOVE SR,{e}",off
    if (op&0xFFC0)==0x44C0:
        m=(op>>3)&7; r=op&7; e,off,_=ea(m,r,off,1)
        return f"  MOVE {e},CCR",off
    if (op&0xFFC0)==0x46C0:
        m=(op>>3)&7; r=op&7; e,off,_=ea(m,r,off,1)
        return f"  MOVE {e},SR",off
    
    # Bcc/BRA/BSR
    if top==6:
        cc=(op>>8)&0xF; d8=op&0xFF
        if d8==0:
            w=rw(off)
            if w is None: return f"  B{CC[cc]} ???",off
            off+=2; t=(pc+2+s16(w))&0xFFFFFF
        elif d8==0xFF:
            l=rl(off)
            if l is None: return f"  B{CC[cc]} ???",off
            off+=4; t=(pc+2+s32(l))&0xFFFFFF
        else:
            t=(pc+2+s8(d8))&0xFFFFFF
        mnem="BRA" if cc==0 else "BSR" if cc==1 else f"B{CC[cc]}"
        return f"  {mnem} ${t:06X}",off
    
    # DBcc
    if (op&0xF0F8)==0x50C8:
        cc=(op>>8)&0xF; r=op&7
        w=rw(off)
        if w is None: return f"  DB{CC[cc]} D{r},???",off
        off+=2; t=(pc+2+s16(w))&0xFFFFFF
        return f"  DB{CC[cc]} D{r},${t:06X}",off
    
    # Scc
    if (op&0xF0C0)==0x50C0 and ((op>>3)&7)!=1:
        cc=(op>>8)&0xF; m=(op>>3)&7; r=op&7
        e,off,_=ea(m,r,off,0)
        return f"  S{CC[cc]} {e}",off
    
    # ADDQ/SUBQ
    if top==5 and (op&0x00C0)!=0x00C0:
        data=(op>>9)&7; data=data if data else 8
        sb=(op>>6)&3; m=(op>>3)&7; r=op&7
        is_sub=(op>>8)&1
        e,off,_=ea(m,r,off,sb)
        return f"  {'SUBQ' if is_sub else 'ADDQ'}{SZ[sb]} #{data},{e}",off
    
    # MOVEQ
    if top==7 and not(op&0x0100):
        dr=(op>>9)&7; d=s8(op&0xFF)
        return f"  MOVEQ #${op&0xFF:02X},D{dr}  ; {d}",off
    
    # MOVE
    if top in (1,2,3):
        szm={1:0,2:2,3:1}; sb=szm[top]
        sm=(op>>3)&7; sr=op&7; dr=(op>>9)&7; dm=(op>>6)&7
        if dm==1:
            se,off,_=ea(sm,sr,off,sb)
            return f"  MOVEA{SZ[sb]} {se},A{dr}",off
        se,off,_=ea(sm,sr,off,sb)
        de,off,_=ea(dm,dr,off,sb)
        return f"  MOVE{SZ[sb]} {se},{de}",off
    
    # ADD/ADDA
    if top==0xD:
        dr=(op>>9)&7; om=(op>>6)&7; m=(op>>3)&7; r=op&7
        # Check ADDX first
        if om in (4,5,6) and m in (0,1):
            sb=om-4
            if m==0: return f"  ADDX{SZ[sb]} D{r},D{dr}",off
            else: return f"  ADDX{SZ[sb]} -(A{r}),-(A{dr})",off
        if om==3: e,off,_=ea(m,r,off,1); return f"  ADDA.W {e},A{dr}",off
        if om==7: e,off,_=ea(m,r,off,2); return f"  ADDA.L {e},A{dr}",off
        if om in (0,1,2): e,off,_=ea(m,r,off,om); return f"  ADD{SZ[om]} {e},D{dr}",off
        if om in (4,5,6): sb=om-4; e,off,_=ea(m,r,off,sb); return f"  ADD{SZ[sb]} D{dr},{e}",off
    
    # SUB/SUBA
    if top==9:
        dr=(op>>9)&7; om=(op>>6)&7; m=(op>>3)&7; r=op&7
        if om in (4,5,6) and m in (0,1):
            sb=om-4
            if m==0: return f"  SUBX{SZ[sb]} D{r},D{dr}",off
            else: return f"  SUBX{SZ[sb]} -(A{r}),-(A{dr})",off
        if om==3: e,off,_=ea(m,r,off,1); return f"  SUBA.W {e},A{dr}",off
        if om==7: e,off,_=ea(m,r,off,2); return f"  SUBA.L {e},A{dr}",off
        if om in (0,1,2): e,off,_=ea(m,r,off,om); return f"  SUB{SZ[om]} {e},D{dr}",off
        if om in (4,5,6): sb=om-4; e,off,_=ea(m,r,off,sb); return f"  SUB{SZ[sb]} D{dr},{e}",off
    
    # CMP/CMPA/EOR
    if top==0xB:
        dr=(op>>9)&7; om=(op>>6)&7; m=(op>>3)&7; r=op&7
        if om==3: e,off,_=ea(m,r,off,1); return f"  CMPA.W {e},A{dr}",off
        if om==7: e,off,_=ea(m,r,off,2); return f"  CMPA.L {e},A{dr}",off
        if om in (0,1,2): e,off,_=ea(m,r,off,om); return f"  CMP{SZ[om]} {e},D{dr}",off
        if om in (4,5,6):
            sb=om-4; e,off,_=ea(m,r,off,sb)
            return f"  EOR{SZ[sb]} D{dr},{e}",off
    
    # AND/MULU/MULS/EXG/ABCD
    if top==0xC:
        dr=(op>>9)&7; om=(op>>6)&7; m=(op>>3)&7; r=op&7
        if om==3: e,off,_=ea(m,r,off,1); return f"  MULU.W {e},D{dr}",off
        if om==7: e,off,_=ea(m,r,off,1); return f"  MULS.W {e},D{dr}",off
        if om in (0,1,2): e,off,_=ea(m,r,off,om); return f"  AND{SZ[om]} {e},D{dr}",off
        if om in (4,5,6):
            sb=om-4; e,off,_=ea(m,r,off,sb)
            return f"  AND{SZ[sb]} D{dr},{e}",off
    
    # OR/DIVU/DIVS
    if top==8:
        dr=(op>>9)&7; om=(op>>6)&7; m=(op>>3)&7; r=op&7
        if om==3: e,off,_=ea(m,r,off,1); return f"  DIVU.W {e},D{dr}",off
        if om==7: e,off,_=ea(m,r,off,1); return f"  DIVS.W {e},D{dr}",off
        if om in (0,1,2): e,off,_=ea(m,r,off,om); return f"  OR{SZ[om]} {e},D{dr}",off
        if om in (4,5,6):
            sb=om-4; e,off,_=ea(m,r,off,sb)
            return f"  OR{SZ[sb]} D{dr},{e}",off
    
    # BTST/BCHG/BCLR/BSET (immediate)
    if (op&0xFF00)==0x0800:
        bop=(op>>6)&3; ops=["BTST","BCHG","BCLR","BSET"]
        m=(op>>3)&7; r=op&7
        bn=rw(off)
        if bn is None: return f"  {ops[bop]} #?,???",off
        off+=2; e,off,_=ea(m,r,off,0)
        return f"  {ops[bop]} #{bn},{e}",off
    
    # BTST/BCHG/BCLR/BSET (register)
    if top==0 and (op&0x0100):
        bop=(op>>6)&3; ops=["BTST","BCHG","BCLR","BSET"]
        dr=(op>>9)&7; m=(op>>3)&7; r=op&7
        e,off,_=ea(m,r,off,0)
        return f"  {ops[bop]} D{dr},{e}",off
    
    # ORI/ANDI/SUBI/ADDI/EORI/CMPI
    if top==0 and not(op&0x0100):
        ot=(op>>9)&7; sb=(op>>6)&3; m=(op>>3)&7; r=op&7
        names={0:"ORI",1:"ANDI",2:"SUBI",3:"ADDI",5:"EORI",6:"CMPI"}
        if ot in names and sb<3:
            nm=names[ot]
            if sb==0: im=rw(off); off+=2; ims=f"#${im&0xFF:02X}" if im else "#?"
            elif sb==1: im=rw(off); off+=2; ims=f"#${im:04X}" if im else "#?"
            else: im=rl(off); off+=4; ims=f"#${im:08X}" if im else "#?"
            e,off,_=ea(m,r,off,sb)
            return f"  {nm}{SZ[sb]} {ims},{e}",off
    
    # ASL/ASR/LSL/LSR/ROL/ROR/ROXL/ROXR
    if top==0xE:
        if (op&0xC0)==0xC0:
            tb=(op>>9)&3; dr=(op>>8)&1
            types_r=["ASR","LSR","ROXR","ROR"]; types_l=["ASL","LSL","ROXL","ROL"]
            mn=(types_l if dr else types_r)[tb]
            m=(op>>3)&7; r=op&7; e,off,_=ea(m,r,off,1)
            return f"  {mn} {e}",off
        else:
            cr=(op>>9)&7; sb=(op>>6)&3; ir=(op>>5)&1; dr=(op>>8)&1
            tb=(op>>3)&3; dreg=op&7
            types_r=["ASR","LSR","ROXR","ROR"]; types_l=["ASL","LSL","ROXL","ROL"]
            mn=(types_l if dr else types_r)[tb]
            if ir: return f"  {mn}{SZ[sb]} D{cr},D{dreg}",off
            cnt=cr if cr else 8
            return f"  {mn}{SZ[sb]} #{cnt},D{dreg}",off
    
    return f"  DC.W ${op:04X}",off

def decode(start, end, label="", stop_rts=False):
    if label: print(f"\n{'='*70}\n  {label}\n{'='*70}")
    o=start
    while o<end:
        a=ROM_BASE+o
        line,no=dis(o)
        nb=no-o
        h=""
        for i in range(0,min(nb,10),2):
            w=rw(o+i)
            if w is not None: h+=f"{w:04X} "
        print(f"${a:06X}  {h:<24s}{line}")
        o=no
        if stop_rts and "RTS" in line: break

# =====================================================================
print("\n" + "#"*70)
print("# TASK 1: BltBitMap_public epilogue at $FCA0C0")
print("#"*70)
decode(0xA0C0, 0xA0F0, "BltBitMap_public epilogue ($FCA0C0-$FCA0EC)", stop_rts=True)

# =====================================================================
print("\n" + "#"*70)
print("# TASK 2: BltBitMap_public entry at $FC9C08")
print("#"*70)
decode(0x9C08, 0x9CA0, "BltBitMap_public entry ($FC9C08)")

# =====================================================================
print("\n" + "#"*70)
print("# TASK 3: ClipRect BltBitMap loop at $FCEF00")
print("#"*70)
decode(0xEEE0, 0xEFC0, "Function at $FCEEE0 (ClipRect blit loop)")

# =====================================================================
print("\n" + "#"*70)
print("# TASK 4: Caller at $FC5CA0 (calls $FCEEE0)")
print("#"*70)
# Let's look at what's around $FC5CA0
decode(0x5C70, 0x5CC0, "Code around $FC5CA0 (caller of ClipRect loop)")

# Also decode the subroutine called at $FCEFFC from within the loop
decode(0xEFFC, 0xF080, "Subroutine at $FCEFFC (called from loop)")

# =====================================================================
print("\n" + "#"*70)
print("# TASK 5: VBLANK handlers")
print("#"*70)

# The AddIntServer at $FE541A adds vector 5 (VBLANK) with handler at $0130(A6)
# This is Intuition's VBLANK handler. A6 = IntuitionBase here.
# The actual server code pointer is at IS_CODE offset in the Interrupt struct
# Let's look at the code after the AddIntServer call
decode(0x2540A, 0x25480, "Intuition AddIntServer VBLANK ($FE541A context)")

# Also look at the other VBLANK AddIntServer at $FE8EC0  
decode(0x28EB0, 0x28F00, "Graphics AddIntServer VBLANK ($FE8EC0 context)")

# Decode graphics VBLANK handler - the handler address is at $0078(A2) 
# which is stored at IS_CODE in the interrupt struct. Let's check the LEA at $FE8EB6
# LEA $0078(A2),A0 then MOVE.L A0,$000E(A1) — so handler code offset stored in struct
# The actual handler code is at IntuitionBase+$0078... we need to find the actual routine

# The $FC45FC call: AddIntServer with vector 3 and handler at $FC4684
decode(0x4680, 0x46E0, "Interrupt handler at $FC4684 (vector 3)")

# Let's also look at $FC4678 (vector 13 handler)
decode(0x4670, 0x4690, "Interrupt handler at $FC4678 (vector 13)")

# Look for the Intuition VBLANK ISR code
# The LEA $0130(A6),A1 before AddIntServer suggests the Interrupt struct starts at IBase+$130
# IS_CODE is at offset $12 in the Interrupt struct, so the code pointer is at IBase+$130+$12 = IBase+$142
# But the MOVE.L at $FC45F2 shows: MOVE.L #$FC4684,$12(A1) — so the code is at $FC4684
# Similarly we need to find what code pointer Intuition stored

# The Intuition setup at $FE540E: MOVEQ #5,D0 (VBLANK vector)
# LEA $0130(A6),A1 — A1 = &IBase->Interrupt struct at offset $130
# The code already stored the IS_CODE pointer somewhere before this
# Let's look at the broader context
decode(0x253E0, 0x25440, "Intuition VBLANK setup context ($FE53E0)")

# =====================================================================
print("\n" + "#"*70)
print("# KEY: What calls $FCEEE0 (the ClipRect loop function)?")
print("#"*70)
# We found JSR $FCEEE0 at $FC5CA0. Let's see more context
decode(0x5C40, 0x5CB0, "BltBitMap_inner context around $FC5CA0")

# Also: find ALL callers of FCEEE0
print("\n--- ALL callers of $FCEEE0 ---")
for so in range(0, 0x40000, 2):
    w=rw(so)
    if w is None: continue
    # JSR abs.L (4EB9)
    if w==0x4EB9:
        a=rl(so+2)
        if a is not None and a==0xFCEEE0:
            print(f"  JSR $FCEEE0 at ${ROM_BASE+so:06X}")
    # BSR.W (6100)
    if w==0x6100:
        d=rw(so+2)
        if d is not None:
            t=(ROM_BASE+so+2+s16(d))&0xFFFFFF
            if t==0xFCEEE0:
                print(f"  BSR.W $FCEEE0 at ${ROM_BASE+so:06X}")
    # BSR.B
    if (w&0xFF00)==0x6100 and (w&0xFF)!=0:
        d8=s8(w&0xFF); t=(ROM_BASE+so+2+d8)&0xFFFFFF
        if t==0xFCEEE0:
            print(f"  BSR.B $FCEEE0 at ${ROM_BASE+so:06X}")

# And specifically, who calls $FCEFFC (the inner subroutine from the loop)
print("\n--- ALL callers of $FCEFFC ---")
for so in range(0, 0x40000, 2):
    w=rw(so)
    if w is None: continue
    if w==0x4EB9:
        a=rl(so+2)
        if a is not None and a==0xFCEFFC:
            print(f"  JSR $FCEFFC at ${ROM_BASE+so:06X}")
    if w==0x6100:
        d=rw(so+2)
        if d is not None:
            t=(ROM_BASE+so+2+s16(d))&0xFFFFFF
            if t==0xFCEFFC:
                print(f"  BSR.W $FCEFFC at ${ROM_BASE+so:06X}")

# =====================================================================
print("\n" + "#"*70)
print("# TASK 5b: BeginRefresh/EndRefresh -- decode key callers")
print("#"*70)

# BeginRefresh at $FC60E2 — this is in graphics.library
decode(0x60C0, 0x6160, "Graphics BeginRefresh context ($FC60E2)")

# Look at the wider function containing beginRefresh usage near FC6DA6
decode(0x6D80, 0x6DD0, "BeginRefresh at $FC6DA6 context")

#  Check who calls $FD3D3C (the JSR target from ClipRect function at FCEEFC)
print("\n--- ALL callers of $FD3D3C ---")
for so in range(0, 0x40000, 2):
    w=rw(so)
    if w is None: continue
    if w==0x4EB9:
        a=rl(so+2)
        if a is not None and a==0xFD3D3C:
            print(f"  JSR $FD3D3C at ${ROM_BASE+so:06X}")

print("\nDone.")
