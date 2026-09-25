#!/usr/bin/env python3
"""Search Kickstart 1.3 ROM for strap module: BootPtr access and checksum code."""

import struct
import sys

ROM_BASE = 0xFC0000

with open('Kick13.rom', 'rb') as f:
    rom = f.read()

print(f"ROM size: {len(rom)} bytes (0x{len(rom):X})")

def read_word(offset):
    return struct.unpack_from('>H', rom, offset)[0]

def read_long(offset):
    return struct.unpack_from('>I', rom, offset)[0]

# ============================================================
# Part 1: Search for MOVE.L $42(An),Am  ->  JSR/JMP (Am)
# ============================================================
# MOVE.L (d16,An),Am: 0010 mmm 001 101 nnn  followed by displacement word
# Also search for MOVE.L $42(An),Dn: 0010 ddd 000 101 nnn

print("\n=== Searching for MOVE.L $42(An),Ax ===")
for i in range(0, len(rom) - 4, 2):
    w1 = read_word(i)
    w2 = read_word(i + 2)
    
    if w2 != 0x0042:
        continue
    if (w1 & 0xF000) != 0x2000:  # not MOVE.L
        continue
    
    src_reg = w1 & 0x07
    src_mode = (w1 >> 3) & 0x07
    dst_mode = (w1 >> 6) & 0x07
    dst_reg = (w1 >> 9) & 0x07
    
    if src_mode != 5:  # not (d16,An) mode
        continue
    
    addr = ROM_BASE + i
    
    if dst_mode == 1:  # to address register
        desc = f"MOVE.L $42(A{src_reg}),A{dst_reg}"
    elif dst_mode == 0:  # to data register
        desc = f"MOVE.L $42(A{src_reg}),D{dst_reg}"
    else:
        continue
    
    # Look ahead for JSR/JMP within next 16 words
    follow = ""
    for j in range(2, 20):
        off = i + j * 2
        if off >= len(rom) - 2:
            break
        wn = read_word(off)
        target_reg = dst_reg
        if dst_mode == 1:
            if wn == 0x4E90 + target_reg:
                follow = f"  -> JSR (A{target_reg}) at ${ROM_BASE + off:06X} (+{j*2})"
                break
            elif wn == 0x4ED0 + target_reg:
                follow = f"  -> JMP (A{target_reg}) at ${ROM_BASE + off:06X} (+{j*2})"
                break
    
    # Show context bytes
    ctx_before = rom[max(0,i-4):i].hex()
    ctx_after = rom[i+4:min(len(rom),i+20)].hex()
    
    print(f"  ${addr:06X}: {desc}{follow}")
    print(f"    bytes: [{ctx_before}] {rom[i:i+4].hex()} [{ctx_after}]")

# ============================================================
# Part 2: Disassemble ROM around $FE8400-$FE8600 (strap area)
# ============================================================
print("\n=== Hex dump of ROM $FE8400-$FE8600 (offset $28400-$28600) ===")
for off in range(0x28400, min(0x28600, len(rom)), 16):
    addr = ROM_BASE + off
    chunk = rom[off:off+16]
    hexstr = ' '.join(f'{b:02X}' for b in chunk)
    ascii_str = ''.join(chr(b) if 32 <= b < 127 else '.' for b in chunk)
    print(f"  ${addr:06X}: {hexstr}  {ascii_str}")

print("\n=== Hex dump of ROM $FE8800-$FE8A00 (offset $28800-$28A00) ===")
for off in range(0x28800, min(0x28A00, len(rom)), 16):
    addr = ROM_BASE + off
    chunk = rom[off:off+16]
    hexstr = ' '.join(f'{b:02X}' for b in chunk)
    ascii_str = ''.join(chr(b) if 32 <= b < 127 else '.' for b in chunk)
    print(f"  ${addr:06X}: {hexstr}  {ascii_str}")

# ============================================================
# Part 3: Search for JSR (An) and JMP (An) in the strap area
# ============================================================
print("\n=== JSR/JMP instructions in $FE8000-$FE9000 ===")
for off in range(0x28000, min(0x29000, len(rom)), 2):
    w = read_word(off)
    addr = ROM_BASE + off
    # JSR (An) = $4E90-$4E97
    if 0x4E90 <= w <= 0x4E97:
        reg = w - 0x4E90
        print(f"  ${addr:06X}: JSR (A{reg})")
    # JMP (An) = $4ED0-$4ED7
    elif 0x4ED0 <= w <= 0x4ED7:
        reg = w - 0x4ED0
        print(f"  ${addr:06X}: JMP (A{reg})")
    # JSR abs.L = $4EB9
    elif w == 0x4EB9:
        if off + 4 < len(rom):
            target = read_long(off + 2)
            print(f"  ${addr:06X}: JSR ${target:08X}")
    # JMP abs.L = $4EF9
    elif w == 0x4EF9:
        if off + 4 < len(rom):
            target = read_long(off + 2)
            print(f"  ${addr:06X}: JMP ${target:08X}")
    # RTS = $4E75
    elif w == 0x4E75:
        print(f"  ${addr:06X}: RTS")

# ============================================================
# Part 4: Simple disassembly of 68k code around found locations
# ============================================================
def disasm_range(start_off, end_off, label=""):
    """Very basic 68000 disassembler for key instructions."""
    if label:
        print(f"\n=== {label} ===")
    
    off = start_off
    while off < end_off and off < len(rom) - 2:
        addr = ROM_BASE + off
        w = read_word(off)
        size = 2  # default
        desc = f"DC.W ${w:04X}"
        
        # NOP
        if w == 0x4E71:
            desc = "NOP"
        # RTS
        elif w == 0x4E75:
            desc = "RTS"
        # RTE
        elif w == 0x4E73:
            desc = "RTE"
        # TRAP #n
        elif (w & 0xFFF0) == 0x4E40:
            desc = f"TRAP #{w & 0x0F}"
        # JSR (An)
        elif 0x4E90 <= w <= 0x4E97:
            desc = f"JSR (A{w - 0x4E90})"
        # JMP (An)
        elif 0x4ED0 <= w <= 0x4ED7:
            desc = f"JMP (A{w - 0x4ED0})"
        # JSR abs.L
        elif w == 0x4EB9 and off + 4 < len(rom):
            target = read_long(off + 2)
            desc = f"JSR ${target:08X}"
            size = 6
        # JMP abs.L
        elif w == 0x4EF9 and off + 4 < len(rom):
            target = read_long(off + 2)
            desc = f"JMP ${target:08X}"
            size = 6
        # BSR.W
        elif w == 0x6100 and off + 2 < len(rom):
            disp = struct.unpack_from('>h', rom, off + 2)[0]
            target = addr + 2 + disp
            desc = f"BSR.W ${target:06X}"
            size = 4
        # BSR.B
        elif (w & 0xFF00) == 0x6100 and (w & 0xFF) != 0:
            disp = w & 0xFF
            if disp & 0x80:
                disp -= 256
            target = addr + 2 + disp
            desc = f"BSR.B ${target:06X}"
        # BRA.W
        elif w == 0x6000 and off + 2 < len(rom):
            disp = struct.unpack_from('>h', rom, off + 2)[0]
            target = addr + 2 + disp
            desc = f"BRA.W ${target:06X}"
            size = 4
        # BRA.B
        elif (w & 0xFF00) == 0x6000 and (w & 0xFF) != 0:
            disp = w & 0xFF
            if disp & 0x80:
                disp -= 256
            target = addr + 2 + disp
            desc = f"BRA.B ${target:06X}"
        # Bcc.B
        elif (w & 0xF000) == 0x6000 and (w & 0xFF) != 0:
            cc = (w >> 8) & 0x0F
            cc_names = ['BRA','BSR','BHI','BLS','BCC','BCS','BNE','BEQ',
                       'BVC','BVS','BPL','BMI','BGE','BLT','BGT','BLE']
            disp = w & 0xFF
            if disp & 0x80:
                disp -= 256
            target = addr + 2 + disp
            desc = f"{cc_names[cc]}.B ${target:06X}"
        # Bcc.W
        elif (w & 0xF0FF) == 0x6000 and (w & 0x0F00) != 0:
            cc = (w >> 8) & 0x0F
            cc_names = ['BRA','BSR','BHI','BLS','BCC','BCS','BNE','BEQ',
                       'BVC','BVS','BPL','BMI','BGE','BLT','BGT','BLE']
            disp = struct.unpack_from('>h', rom, off + 2)[0]
            target = addr + 2 + disp
            desc = f"{cc_names[cc]}.W ${target:06X}"
            size = 4
        # MOVEA.L #imm,An
        elif (w & 0xF1FF) == 0x207C and off + 4 < len(rom):
            reg = (w >> 9) & 7
            val = read_long(off + 2)
            desc = f"MOVEA.L #${val:08X},A{reg}"
            size = 6
        # MOVE.L #imm,Dn
        elif (w & 0xF1FF) == 0x203C and off + 4 < len(rom):
            reg = (w >> 9) & 7
            val = read_long(off + 2)
            desc = f"MOVE.L #${val:08X},D{reg}"
            size = 6
        # MOVE.L (d16,An),An or Dn
        elif (w & 0xF000) == 0x2000:
            src_reg = w & 7
            src_mode = (w >> 3) & 7
            dst_mode = (w >> 6) & 7
            dst_reg = (w >> 9) & 7
            if src_mode == 5 and off + 2 < len(rom):
                disp = struct.unpack_from('>h', rom, off + 2)[0]
                if dst_mode == 0:
                    desc = f"MOVE.L ${disp:04X}(A{src_reg}),D{dst_reg}"
                elif dst_mode == 1:
                    desc = f"MOVEA.L ${disp:04X}(A{src_reg}),A{dst_reg}"
                size = 4
            elif src_mode == 7 and src_reg == 0 and off + 2 < len(rom):
                # MOVE.L abs.W, ...
                abs_addr = struct.unpack_from('>h', rom, off + 2)[0]
                if dst_mode == 0:
                    desc = f"MOVE.L ${abs_addr:04X}.W,D{dst_reg}"
                elif dst_mode == 1:
                    desc = f"MOVEA.L ${abs_addr:04X}.W,A{dst_reg}"
                size = 4
            elif src_mode == 0:
                if dst_mode == 0:
                    desc = f"MOVE.L D{src_reg},D{dst_reg}"
                elif dst_mode == 1:
                    desc = f"MOVEA.L D{src_reg},A{dst_reg}"
            elif src_mode == 1:
                if dst_mode == 0:
                    desc = f"MOVE.L A{src_reg},D{dst_reg}"
                elif dst_mode == 1:
                    desc = f"MOVEA.L A{src_reg},A{dst_reg}"
            elif src_mode == 2:
                if dst_mode == 0:
                    desc = f"MOVE.L (A{src_reg}),D{dst_reg}"
                elif dst_mode == 1:
                    desc = f"MOVEA.L (A{src_reg}),A{dst_reg}"
        # LEA (d16,An),Am
        elif (w & 0xF1F8) == 0x41E8 and off + 2 < len(rom):
            dst_reg = (w >> 9) & 7
            src_reg = w & 7
            disp = struct.unpack_from('>h', rom, off + 2)[0]
            desc = f"LEA ${disp:04X}(A{src_reg}),A{dst_reg}"
            size = 4
        # MOVEQ #imm,Dn
        elif (w & 0xF100) == 0x7000:
            reg = (w >> 9) & 7
            val = w & 0xFF
            if val & 0x80:
                val -= 256
            desc = f"MOVEQ #${val & 0xFF:02X},D{reg}"
        # CLR.L Dn
        elif (w & 0xFFF8) == 0x4280:
            desc = f"CLR.L D{w & 7}"
        # ADD.L Dn,(An)+ etc - we'll skip complex ones
        # ADDQ
        elif (w & 0xF100) == 0x5000:
            data = (w >> 9) & 7
            if data == 0: data = 8
            sz = (w >> 6) & 3
            sz_names = ['.B', '.W', '.L']
            mode = (w >> 3) & 7
            reg = w & 7
            if sz < 3 and mode == 0:
                desc = f"ADDQ{sz_names[sz]} #{data},D{reg}"
            elif sz < 3 and mode == 1:
                desc = f"ADDQ{sz_names[sz]} #{data},A{reg}"
        # SUBQ
        elif (w & 0xF100) == 0x5100:
            data = (w >> 9) & 7
            if data == 0: data = 8
            sz = (w >> 6) & 3
            sz_names = ['.B', '.W', '.L']
            mode = (w >> 3) & 7
            reg = w & 7
            if sz < 3 and mode == 0:
                desc = f"SUBQ{sz_names[sz]} #{data},D{reg}"
            elif sz < 3 and mode == 1:
                desc = f"SUBQ{sz_names[sz]} #{data},A{reg}"
        # DBcc
        elif (w & 0xF0F8) == 0x50C8 and off + 2 < len(rom):
            cc = (w >> 8) & 0x0F
            reg = w & 7
            cc_names = ['DBT','DBRA','DBHI','DBLS','DBCC','DBCS','DBNE','DBEQ',
                       'DBVC','DBVS','DBPL','DBMI','DBGE','DBLT','DBGT','DBLE']
            disp = struct.unpack_from('>h', rom, off + 2)[0]
            target = addr + 2 + disp
            desc = f"{cc_names[cc]} D{reg},${target:06X}"
            size = 4
        # MOVEM
        elif (w & 0xFFF8) == 0x48E0 and off + 2 < len(rom):
            reg = w & 7
            mask = read_word(off + 2)
            desc = f"MOVEM.L regs,-(A{reg})  mask=${mask:04X}"
            size = 4
        elif (w & 0xFFF8) == 0x4CD0 and off + 2 < len(rom):
            reg = w & 7
            mask = read_word(off + 2)
            desc = f"MOVEM.L (A{reg})+,regs  mask=${mask:04X}"
            size = 4
        # CMPI
        elif (w & 0xFF00) == 0x0C00 and off + 2 < len(rom):
            sz = (w >> 6) & 3
            if sz == 2 and off + 4 < len(rom):  # .L
                val = read_long(off + 2)
                mode = (w >> 3) & 7
                reg = w & 7
                if mode == 0:
                    desc = f"CMPI.L #${val:08X},D{reg}"
                size = 6
        # CMP.L
        elif (w & 0xF1C0) == 0xB080:
            src_mode = (w >> 3) & 7
            src_reg = w & 7
            dst_reg = (w >> 9) & 7
            if src_mode == 0:
                desc = f"CMP.L D{src_reg},D{dst_reg}"
        # TST.L
        elif (w & 0xFFC0) == 0x4A80:
            mode = (w >> 3) & 7
            reg = w & 7
            if mode == 0:
                desc = f"TST.L D{reg}"
        # ADD.L
        elif (w & 0xF000) == 0xD000:
            sz = (w >> 6) & 3
            if sz == 2:  # .L
                src_mode = (w >> 3) & 7
                src_reg = w & 7
                dst_reg = (w >> 9) & 7
                dir_bit = (w >> 8) & 1
                if dir_bit == 0 and src_mode == 2:
                    desc = f"ADD.L (A{src_reg}),D{dst_reg}"
                elif dir_bit == 0 and src_mode == 3:
                    desc = f"ADD.L (A{src_reg})+,D{dst_reg}"
                elif dir_bit == 0 and src_mode == 0:
                    desc = f"ADD.L D{src_reg},D{dst_reg}"
        # UNLK
        elif (w & 0xFFF8) == 0x4E58:
            desc = f"UNLK A{w & 7}"
        # LINK
        elif (w & 0xFFF8) == 0x4E50 and off + 2 < len(rom):
            disp = struct.unpack_from('>h', rom, off + 2)[0]
            desc = f"LINK A{w & 7},#${disp & 0xFFFF:04X}"
            size = 4
        # SWAP
        elif (w & 0xFFF8) == 0x4840:
            desc = f"SWAP D{w & 7}"
        # EXT
        elif (w & 0xFFF8) == 0x4880:
            desc = f"EXT.W D{w & 7}"
        elif (w & 0xFFF8) == 0x48C0:
            desc = f"EXT.L D{w & 7}"
        # MOVE.W
        elif (w & 0xF000) == 0x3000:
            src_mode = (w >> 3) & 7
            src_reg = w & 7
            dst_mode = (w >> 6) & 7
            dst_reg = (w >> 9) & 7
            if src_mode == 7 and src_reg == 4 and off + 2 < len(rom):
                val = read_word(off + 2)
                if dst_mode == 0:
                    desc = f"MOVE.W #${val:04X},D{dst_reg}"
                size = 4
            elif src_mode == 5 and off + 2 < len(rom):
                disp = struct.unpack_from('>h', rom, off + 2)[0]
                if dst_mode == 0:
                    desc = f"MOVE.W ${disp & 0xFFFF:04X}(A{src_reg}),D{dst_reg}"
                size = 4
        
        hexbytes = ' '.join(f'{b:02X}' for b in rom[off:off+size])
        print(f"  ${addr:06X}: {hexbytes:24s} {desc}")
        off += size

# Disassemble the interesting ranges
disasm_range(0x28400, 0x28600, "Disasm $FE8400-$FE8600")
disasm_range(0x28800, 0x28A00, "Disasm $FE8800-$FE8A00")

# ============================================================
# Part 5: Search for checksum patterns
# Look for ADD.L (An)+,Dn loops (typical for checksum)
# Also search for the value 256 ($100) used as loop count for 1024-byte boot block
# ============================================================
print("\n=== Searching for checksum loop patterns in strap area ($FE8000-$FE9000) ===")
for off in range(0x28000, min(0x29000, len(rom)), 2):
    w = read_word(off)
    addr = ROM_BASE + off
    # ADD.L (An)+,Dn = 1101 ddd 010 011 aaa
    if (w & 0xF1F8) == 0xD098:
        dn = (w >> 9) & 7
        an = w & 7
        print(f"  ${addr:06X}: ADD.L (A{an})+,D{dn}")
    # ADDX - for extended precision checksum
    elif (w & 0xF1F8) == 0xD180:
        dy = (w >> 9) & 7
        dx = w & 7
        print(f"  ${addr:06X}: ADDX.L D{dx},D{dy}")
