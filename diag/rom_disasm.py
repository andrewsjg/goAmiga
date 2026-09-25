#!/usr/bin/env python3
"""Disassemble a region of Kickstart 1.3 ROM around $FC0D14."""
import struct, sys

ROM_PATH = "/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom"
ROM_BASE = 0xFC0000

with open(ROM_PATH, "rb") as f:
    rom = f.read()

def w(off):
    return struct.unpack(">H", rom[off:off+2])[0]

def sw(off):
    return struct.unpack(">h", rom[off:off+2])[0]

def l(off):
    return struct.unpack(">I", rom[off:off+4])[0]

def regmask(mask, predec=False):
    names = ["D0","D1","D2","D3","D4","D5","D6","D7",
             "A0","A1","A2","A3","A4","A5","A6","SP"]
    regs = []
    for bit in range(16):
        if mask & (1 << bit):
            regs.append(names[15 - bit] if predec else names[bit])
    return "/".join(regs)

# Print hex + disassembly for range
start_off = 0x0C80
end_off = 0x0D70
off = start_off

print(f"Disassembly of Kickstart 1.3 ROM: ${ROM_BASE+start_off:08X}-${ROM_BASE+end_off:08X}")
print(f"{'='*70}")
print()

while off < end_off:
    pc = ROM_BASE + off
    op = w(off)
    hexbytes = f"{op:04X}"
    
    # Mark the stuck address
    marker = " <<<< STUCK HERE" if off == 0x0D14 else ""
    
    # Try to decode common instructions
    
    # MOVEM.L regs,-(SP)
    if op == 0x48E7:
        mask = w(off+2)
        hexbytes = f"{op:04X} {mask:04X}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} MOVEM.L {regmask(mask, predec=True)},-(SP){marker}")
        off += 4; continue
    
    # MOVEM.L (SP)+,regs
    if op == 0x4CDF:
        mask = w(off+2)
        hexbytes = f"{op:04X} {mask:04X}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} MOVEM.L (SP)+,{regmask(mask)}{marker}")
        off += 4; continue
    
    # MOVEM.L d16(An),regs  (4CE8-4CEF)
    if (op & 0xFFF8) == 0x4CE8:
        an = op & 7
        mask = w(off+2)
        disp = sw(off+4)
        hexbytes = f"{op:04X} {mask:04X} {disp&0xFFFF:04X}"
        rn = "SP" if an == 7 else f"A{an}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} MOVEM.L ${disp:04X}({rn}),{regmask(mask)}{marker}")
        off += 6; continue
    
    # LEA xxx.L,An
    if (op & 0xF1FF) == 0x41F9:
        an = (op >> 9) & 7
        addr = l(off+2)
        hexbytes = f"{op:04X} {addr:08X}"
        rn = "SP" if an == 7 else f"A{an}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} LEA ${addr:08X},{rn}{marker}")
        off += 6; continue
    
    # MOVEA.L xxx.W,An
    if (op & 0xF1FF) == 0x2078:
        an = (op >> 9) & 7
        addr = sw(off+2)
        hexbytes = f"{op:04X} {addr&0xFFFF:04X}"
        rn = "SP" if an == 7 else f"A{an}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} MOVEA.L ${addr&0xFFFF:04X}.W,{rn}{marker}")
        off += 4; continue
    
    # MOVE.W d16(An),Dn
    if (op & 0xF000) == 0x3000 and (op & 0x0038) == 0x0028:
        dn = (op >> 9) & 7
        an = op & 7
        disp = sw(off+2)
        hexbytes = f"{op:04X} {disp&0xFFFF:04X}"
        rn = "SP" if an == 7 else f"A{an}"
        # Annotate known custom chip registers
        if an == 0:  # relative to A0 which is $DFF000
            names = {0x01C: "INTREQR", 0x01E: "INTENAR"}
            ann = names.get(disp & 0xFFFF, "")
            if ann:
                ann = f"  ; {ann}"
        else:
            ann = ""
        print(f"  ${pc:08X}:  {hexbytes:<20s} MOVE.W ${disp&0xFFFF:04X}({rn}),D{dn}{ann}{marker}")
        off += 4; continue
    
    # BTST #imm,Dn
    if (op & 0xFFC0) == 0x0800:
        dn = op & 7
        bit = w(off+2)
        hexbytes = f"{op:04X} {bit:04X}"
        # Annotate INTREQR bits
        intbits = {0:"TBE", 1:"DSKBLK", 2:"SOFT", 3:"PORTS", 4:"COPER",
                   5:"VERTB", 6:"BLIT", 7:"AUD0", 8:"AUD1", 9:"AUD2",
                   10:"AUD3", 11:"RBF", 12:"DSKSYN", 13:"EXTER", 14:"INTEN"}
        ann = intbits.get(bit, "")
        if ann:
            ann = f"  ; {ann}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} BTST #{bit},D{dn}{ann}{marker}")
        off += 4; continue
    
    # AND.W d16(An),Dn
    if (op & 0xF1F8) == 0xC068:
        dn = (op >> 9) & 7
        an = op & 7
        disp = sw(off+2)
        hexbytes = f"{op:04X} {disp&0xFFFF:04X}"
        rn = "SP" if an == 7 else f"A{an}"
        ann = ""
        if an == 0 and (disp & 0xFFFF) == 0x001E:
            ann = "  ; AND with INTENAR"
        print(f"  ${pc:08X}:  {hexbytes:<20s} AND.W ${disp&0xFFFF:04X}({rn}),D{dn}{ann}{marker}")
        off += 4; continue
    
    # BEQ.B
    if (op & 0xFF00) == 0x6700 and (op & 0xFF) != 0:
        disp = struct.unpack("b", bytes([op & 0xFF]))[0]
        target = pc + 2 + disp
        hexbytes = f"{op:04X}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} BEQ.B ${target:08X}{marker}")
        off += 2; continue
    
    # BEQ.W
    if op == 0x6700:
        disp = sw(off+2)
        target = pc + 2 + disp
        hexbytes = f"{op:04X} {disp&0xFFFF:04X}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} BEQ.W ${target:08X}{marker}")
        off += 4; continue
    
    # BRA.W
    if op == 0x6000:
        disp = sw(off+2)
        target = pc + 2 + disp
        hexbytes = f"{op:04X} {disp&0xFFFF:04X}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} BRA.W ${target:08X}{marker}")
        off += 4; continue
    
    # BRA.B
    if (op & 0xFF00) == 0x6000 and (op & 0xFF) != 0:
        disp = struct.unpack("b", bytes([op & 0xFF]))[0]
        target = pc + 2 + disp
        hexbytes = f"{op:04X}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} BRA.B ${target:08X}{marker}")
        off += 2; continue
    
    # RTE
    if op == 0x4E73:
        hexbytes = f"{op:04X}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} RTE{marker}")
        off += 2; continue
    
    # JSR (An)
    if (op & 0xFFF8) == 0x4E90:
        an = op & 7
        rn = "SP" if an == 7 else f"A{an}"
        hexbytes = f"{op:04X}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} JSR ({rn}){marker}")
        off += 2; continue
    
    # PEA d16(An)
    if (op & 0xFFF8) == 0x4868:
        an = op & 7
        disp = sw(off+2)
        hexbytes = f"{op:04X} {disp&0xFFFF:04X}"
        rn = "SP" if an == 7 else f"A{an}"
        print(f"  ${pc:08X}:  {hexbytes:<20s} PEA ${disp&0xFFFF:04X}({rn}){marker}")
        off += 4; continue
    
    # Default
    hexbytes = f"{op:04X}"
    print(f"  ${pc:08X}:  {hexbytes:<20s} DC.W ${op:04X}{marker}")
    off += 2

print()
print("="*70)
print("ANALYSIS:")
print()
print("DMACON=$02D0 decodes as:")
dmacon = 0x02D0
for bit, name in [(15,"SET/CLR"),(9,"DMAEN"),(8,"BPLEN"),(7,"COPEN"),
                   (6,"BLTEN"),(5,"SPREN"),(4,"DSKEN"),(3,"AUD3"),
                   (2,"AUD2"),(1,"AUD1"),(0,"AUD0")]:
    val = "ON" if dmacon & (1<<bit) else "off"
    print(f"  Bit {bit:2d} {name:8s} = {val}")
