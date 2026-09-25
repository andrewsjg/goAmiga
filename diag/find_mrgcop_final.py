#!/usr/bin/env python3
"""Final: Clean disassembly of MrgCop real implementation at $FCA5B4."""
import struct

ROM_PATH = "/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom"
ROM_BASE = 0xFC0000

with open(ROM_PATH, "rb") as f:
    rom = f.read()

def rw(off): return struct.unpack_from(">H", rom, off)[0]
def rl(off): return struct.unpack_from(">I", rom, off)[0]
def rsw(off): return struct.unpack_from(">h", rom, off)[0]

print("=" * 72)
print("MrgCop in Kickstart 1.3 ROM")
print("=" * 72)
print()
print("Function table entry (LVO -210, index 34): $FC582C")
print("  -> Wrapper pushes A1 (View*) and calls real implementation")
print()
print("Real implementation: $FCA5B4")
print()

# Full hex dump + disasm of the real MrgCop at $FCA5B4
# Function runs until RTS, including subfunctions called via BSR (PC-relative)
off = 0xA5B4 - ROM_BASE  # Wait that's wrong, offset IS 0xA5B4
off = 0xA5B4

# Read until we find the UNLK/RTS pattern
print("=== Disassembly of MrgCop ($FCA5B4) ===")
print()

end_off = off + 0x200  # Read up to 512 bytes
while off < end_off and off < len(rom) - 6:
    w = rw(off)
    addr = ROM_BASE + off
    raw = rom[off:off+8]
    
    # LINK An,#d16
    if 0x4E50 <= w <= 0x4E57:
        d = rsw(off+2)
        an = w & 7
        hx = f"{w:04X}{d&0xFFFF:04X}"
        print(f"  ${addr:08X}: {hx:<16s} LINK A{an},#${d&0xFFFF:04X}")
        off += 4; continue
    
    # UNLK An
    if 0x4E58 <= w <= 0x4E5F:
        print(f"  ${addr:08X}: {w:04X}             UNLK A{w&7}")
        off += 2; continue
        
    # MOVEM.L regs,-(SP)
    if w == 0x48E7:
        mask = rw(off+2)
        regs = []
        for i in range(8):
            if mask & (0x8000 >> i): regs.append(f"D{i}")
        for i in range(8):
            if mask & (0x0080 >> i): regs.append(f"A{i}")
        print(f"  ${addr:08X}: {w:04X}{mask:04X}         MOVEM.L {'/'.join(regs)},-(SP)")
        off += 4; continue
    
    # MOVEM.L (SP)+,regs
    if w == 0x4CDF:
        mask = rw(off+2)
        regs = []
        for i in range(8):
            if mask & (1 << i): regs.append(f"D{i}")
        for i in range(8):
            if mask & (0x100 << i): regs.append(f"A{i}")
        print(f"  ${addr:08X}: {w:04X}{mask:04X}         MOVEM.L (SP)+,{'/'.join(regs)}")
        off += 4; continue
    
    # MOVEM.L (d16,A5),regs  - 4CED
    if w == 0x4CED:
        mask = rw(off+2)
        d = rsw(off+4)
        regs = []
        for i in range(8):
            if mask & (1 << i): regs.append(f"D{i}")
        for i in range(8):
            if mask & (0x100 << i): regs.append(f"A{i}")
        print(f"  ${addr:08X}: {w:04X}{mask:04X}{d&0xFFFF:04X}     MOVEM.L ({d}(A5)),{'/'.join(regs)}")
        off += 6; continue
        
    # RTS
    if w == 0x4E75:
        print(f"  ${addr:08X}: 4E75             RTS")
        off += 2
        # Check if this is the function end (after UNLK)
        # Could be subroutine return, continue a bit more
        if off > 0xA5B4 + 4:
            # Check if next instruction starts a new function (LINK/MOVEM)
            nw = rw(off)
            if nw == 0x4E75 or off >= end_off:
                break
            # If there's more code after, it might be a subroutine
            print(f"  -------- (possible sub-function boundary) --------")
        continue
    
    # RTE
    if w == 0x4E73:
        print(f"  ${addr:08X}: 4E73             RTE")
        off += 2; continue
        
    # NOP
    if w == 0x4E71:
        print(f"  ${addr:08X}: 4E71             NOP")
        off += 2; continue
    
    # JSR abs.L
    if w == 0x4EB9:
        a = rl(off+2)
        print(f"  ${addr:08X}: 4EB9{a:08X}   JSR ${a:08X}")
        off += 6; continue
    
    # JMP abs.L
    if w == 0x4EF9:
        a = rl(off+2)
        print(f"  ${addr:08X}: 4EF9{a:08X}   JMP ${a:08X}")
        off += 6; continue
    
    # JSR (d16,A6)  - Exec library calls
    if w == 0x4EAE:
        d = rsw(off+2)
        print(f"  ${addr:08X}: 4EAE{d&0xFFFF:04X}         JSR {d}(A6)")
        off += 4; continue
    
    # JSR (d16,PC) = 4EBA d16
    if w == 0x4EBA:
        d = rsw(off+2)
        target = ROM_BASE + off + 2 + d
        print(f"  ${addr:08X}: 4EBA{d&0xFFFF:04X}         JSR ${target:08X}(PC)")
        off += 4; continue
    
    # BSR
    if (w & 0xFF00) == 0x6100:
        disp8 = w & 0xFF
        if disp8 == 0:
            d = rsw(off+2)
            target = ROM_BASE + off + 2 + d
            print(f"  ${addr:08X}: {w:04X}{d&0xFFFF:04X}         BSR ${target:08X}")
            off += 4
        else:
            if disp8 > 127: disp8 -= 256
            target = ROM_BASE + off + 2 + disp8
            print(f"  ${addr:08X}: {w:04X}             BSR.S ${target:08X}")
            off += 2
        continue
    
    # Bcc
    if (w & 0xF000) == 0x6000:
        cc = (w >> 8) & 0xF
        cc_names = ["BRA","BSR","BHI","BLS","BCC","BCS","BNE","BEQ",
                     "BVC","BVS","BPL","BMI","BGE","BLT","BGT","BLE"]
        disp8 = w & 0xFF
        if disp8 == 0:
            d = rsw(off+2)
            target = ROM_BASE + off + 2 + d
            print(f"  ${addr:08X}: {w:04X}{d&0xFFFF:04X}         {cc_names[cc]} ${target:08X}")
            off += 4
        else:
            if disp8 > 127: disp8 -= 256
            target = ROM_BASE + off + 2 + disp8
            print(f"  ${addr:08X}: {w:04X}             {cc_names[cc]}.S ${target:08X}")
            off += 2
        continue
    
    # MOVE.L #imm,Dn
    if (w & 0xF1FF) == 0x203C:
        dn = (w >> 9) & 7
        imm = rl(off+2)
        print(f"  ${addr:08X}: {w:04X}{imm:08X}   MOVE.L #${imm:08X},D{dn}")
        off += 6; continue
    
    # MOVEA.L #imm,An
    if (w & 0xF1FF) == 0x207C:
        an = (w >> 9) & 7
        imm = rl(off+2)
        print(f"  ${addr:08X}: {w:04X}{imm:08X}   MOVEA.L #${imm:08X},A{an}")
        off += 6; continue
    
    # MOVEA.L (d16,An),Am
    if (w & 0xF1F8) == 0x2068:
        am = (w >> 9) & 7
        an = w & 7
        d = rsw(off+2)
        print(f"  ${addr:08X}: {w:04X}{d&0xFFFF:04X}         MOVEA.L ${d&0xFFFF:04X}(A{an}),A{am}")
        off += 4; continue
    
    # MOVEA.L (An),Am
    if (w & 0xF1F8) == 0x2050:
        am = (w >> 9) & 7
        an = w & 7
        print(f"  ${addr:08X}: {w:04X}             MOVEA.L (A{an}),A{am}")
        off += 2; continue
    
    # MOVE.L (d16,An),Dm
    if (w & 0xF1F8) == 0x2028:
        dm = (w >> 9) & 7
        an = w & 7
        d = rsw(off+2)
        print(f"  ${addr:08X}: {w:04X}{d&0xFFFF:04X}         MOVE.L ${d&0xFFFF:04X}(A{an}),D{dm}")
        off += 4; continue
    
    # MOVE.L Dn,(d16,Am)
    if (w & 0xF1C0) == 0x2140:
        am = (w >> 9) & 7
        src_mode = (w >> 3) & 7
        src_reg = w & 7
        if src_mode == 0:  # Dn
            d = rsw(off+2)
            print(f"  ${addr:08X}: {w:04X}{d&0xFFFF:04X}         MOVE.L D{src_reg},${d&0xFFFF:04X}(A{am})")
            off += 4; continue
    
    # MOVE.L An,-(SP) 
    if (w & 0xFFF8) == 0x2F08:
        print(f"  ${addr:08X}: {w:04X}             MOVE.L A{w&7},-(SP)")
        off += 2; continue
    
    # MOVE.L Dn,-(SP)
    if (w & 0xFFF8) == 0x2F00:
        print(f"  ${addr:08X}: {w:04X}             MOVE.L D{w&7},-(SP)")
        off += 2; continue
    
    # MOVE.L (d16,An),-(SP)
    if (w & 0xFFF8) == 0x2F28:
        an = w & 7
        d = rsw(off+2)
        print(f"  ${addr:08X}: {w:04X}{d&0xFFFF:04X}         MOVE.L ${d&0xFFFF:04X}(A{an}),-(SP)")
        off += 4; continue
    
    # PEA (d16,An)
    if (w & 0xFFF8) == 0x4868:
        an = w & 7
        d = rsw(off+2)
        print(f"  ${addr:08X}: {w:04X}{d&0xFFFF:04X}         PEA ${d&0xFFFF:04X}(A{an})")
        off += 4; continue
    
    # PEA (d16,A5) specifically: 486D
    if w == 0x486D:
        d = rsw(off+2)
        print(f"  ${addr:08X}: 486D{d&0xFFFF:04X}         PEA ${d&0xFFFF:04X}(A5)")
        off += 4; continue
    
    # CLR.L (d16,An)
    if (w & 0xFFF8) == 0x42A8:
        an = w & 7
        d = rsw(off+2)
        print(f"  ${addr:08X}: {w:04X}{d&0xFFFF:04X}         CLR.L ${d&0xFFFF:04X}(A{an})")
        off += 4; continue
    
    # CLR.L (d16,A5): 42AD
    if w == 0x42AD:
        d = rsw(off+2)
        print(f"  ${addr:08X}: 42AD{d&0xFFFF:04X}         CLR.L ${d&0xFFFF:04X}(A5)")
        off += 4; continue
    
    # CLR.W (d16,A5): 426D
    if w == 0x426D:
        d = rsw(off+2)
        print(f"  ${addr:08X}: 426D{d&0xFFFF:04X}         CLR.W ${d&0xFFFF:04X}(A5)")
        off += 4; continue
    
    # CLR.L -(SP)
    if w == 0x42A7:
        print(f"  ${addr:08X}: 42A7             CLR.L -(SP)")
        off += 2; continue
    
    # MOVE.W (d16,An),(d16,Am) - 3B6A: move.w d(a2),d(a5) 
    if w == 0x3B6A:
        src_d = rsw(off+2)
        dst_d = rsw(off+4)
        print(f"  ${addr:08X}: 3B6A{src_d&0xFFFF:04X}{dst_d&0xFFFF:04X}     MOVE.W ${src_d&0xFFFF:04X}(A2),${dst_d&0xFFFF:04X}(A5)")
        off += 6; continue
    
    # MOVEA.L (An),Am: 2652 = MOVEA.L (A2),A3; etc
    if (w & 0xF1F8) == 0x2050:
        am = (w >> 9) & 7
        an = w & 7
        print(f"  ${addr:08X}: {w:04X}             MOVEA.L (A{an}),A{am}")
        off += 2; continue
    
    # MOVEQ
    if (w & 0xF100) == 0x7000:
        dn = (w >> 9) & 7
        imm = w & 0xFF
        if imm > 127: imm -= 256
        print(f"  ${addr:08X}: {w:04X}             MOVEQ #${imm&0xFF:02X},D{dn}")
        off += 2; continue
    
    # MOVE.W (d16,An),D0: 302B = MOVE.W d(A3),D0, 302A = MOVE.W d(A2),D0
    if (w & 0xFFF8) == 0x3028:
        an = w & 7
        d = rsw(off+2)
        print(f"  ${addr:08X}: {w:04X}{d&0xFFFF:04X}         MOVE.W ${d&0xFFFF:04X}(A{an}),D0")
        off += 4; continue
    
    # LEA (d16,A7),A7
    if w == 0x4FEF:
        d = rsw(off+2)
        print(f"  ${addr:08X}: 4FEF{d&0xFFFF:04X}         LEA ${d&0xFFFF:04X}(SP),SP")
        off += 4; continue
    
    # ANDI.L #imm,D0
    if w == 0x0280:
        imm = rl(off+2)
        print(f"  ${addr:08X}: 0280{imm:08X}   ANDI.L #${imm:08X},D0")
        off += 6; continue
    
    # TST.L Dn
    if (w & 0xFFF8) == 0x4A80:
        print(f"  ${addr:08X}: {w:04X}             TST.L D{w&7}")
        off += 2; continue
    
    # TST.W (d16,An) 
    if (w & 0xFFF8) == 0x4A68:
        an = w & 7
        d = rsw(off+2)
        print(f"  ${addr:08X}: {w:04X}{d&0xFFFF:04X}         TST.W ${d&0xFFFF:04X}(A{an})")
        off += 4; continue
    
    # ADDQ.L #n,An
    if (w & 0xF1F8) == 0x5088:
        data = (w >> 9) & 7
        if data == 0: data = 8
        an = w & 7
        print(f"  ${addr:08X}: {w:04X}             ADDQ.L #{data},A{an}")
        off += 2; continue
    
    # MOVE.L (An),Dm  
    if (w & 0xF1F8) == 0x2010:
        dm = (w >> 9) & 7
        an = w & 7
        print(f"  ${addr:08X}: {w:04X}             MOVE.L (A{an}),D{dm}")
        off += 2; continue
    
    # MOVEA.L Dm,An
    if (w & 0xF1F8) == 0x2040:
        an = (w >> 9) & 7
        dm = w & 7
        print(f"  ${addr:08X}: {w:04X}             MOVEA.L D{dm},A{an}")
        off += 2; continue
    
    # MOVEA.L Am,An
    if (w & 0xF1F8) == 0x2048:
        an_dst = (w >> 9) & 7
        an_src = w & 7
        print(f"  ${addr:08X}: {w:04X}             MOVEA.L A{an_src},A{an_dst}")
        off += 2; continue
    
    # MOVE.W Dn,Dm
    if (w & 0xF1F8) == 0x3000:
        dm = (w >> 9) & 7
        dn = w & 7
        print(f"  ${addr:08X}: {w:04X}             MOVE.W D{dn},D{dm}")
        off += 2; continue
    
    # Default: show hex
    print(f"  ${addr:08X}: {w:04X}             DC.W ${w:04X}")
    off += 2

print()
print("=" * 72)
print("SUMMARY")
print("=" * 72)
print(f"MrgCop LVO wrapper:      $FC582C")
print(f"MrgCop real impl:        $FCA5B4")
print(f"  LINK A5,#-32  (32 bytes local vars)")
print(f"  MOVEM.L D2-D5/A3-A4,-(SP)")
print(f"  A2 = View* (from stack param)")
print(f"  Iterates ViewPort chain via (A2)->first VP then VP->Next")
print(f"  Calls sub-functions via PC-relative BSR for each VP")
print(f"  Sub $FCA56E used for copper list building")
print(f"  Sub $FD1470 used for other processing")
