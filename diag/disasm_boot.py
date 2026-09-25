#!/usr/bin/env python3
"""Disassemble disk boot handler from Kick13.rom"""
import struct, sys

ROM = '/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom'
with open(ROM, 'rb') as f:
    rom = f.read()

def r16(off): return struct.unpack('>H', rom[off:off+2])[0]
def r32(off): return struct.unpack('>I', rom[off:off+4])[0]
def s16(off): return struct.unpack('>h', rom[off:off+2])[0]
def sb(v):    return struct.unpack('>b', bytes([v & 0xFF]))[0]

BASE = 0xFC0000
START = 0xFC237E
END   = 0xFC23C0

pc = START - BASE  # file offset

def hx(start, end):
    return ' '.join(f'{rom[i]:02X}' for i in range(start, end))

while pc <= END - BASE:
    addr = BASE + pc
    op = r16(pc)
    s = pc

    # MOVE.L #imm,-(A7)
    if op == 0x2F3C:
        imm = r32(pc+2); pc += 6
        print(f'{addr:08X}  {hx(s,pc):<28s}  MOVE.L  #${imm:08X},-(A7)')

    # CMPI.L #imm,(A7)+
    elif op == 0x0C9F:
        imm = r32(pc+2); pc += 6
        print(f'{addr:08X}  {hx(s,pc):<28s}  CMPI.L  #${imm:08X},(A7)+')

    # BEQ.S with 8-bit displacement
    elif (op & 0xFF00) == 0x6700 and (op & 0xFF) != 0:
        disp = sb(op & 0xFF); pc += 2
        target = addr + 2 + disp
        print(f'{addr:08X}  {hx(s,pc):<28s}  BEQ.S   ${target:08X}')

    # BEQ.W with 16-bit displacement
    elif op == 0x6700:
        disp = s16(pc+2); pc += 4
        target = addr + 2 + disp
        print(f'{addr:08X}  {hx(s,pc):<28s}  BEQ.W   ${target:08X}')

    # MOVEA.L #imm,A7
    elif op == 0x2E7C:
        imm = r32(pc+2); pc += 6
        print(f'{addr:08X}  {hx(s,pc):<28s}  MOVEA.L #${imm:08X},A7')

    # CLR.L -(A7)
    elif op == 0x42A7:
        pc += 2
        print(f'{addr:08X}  {hx(s,pc):<28s}  CLR.L   -(A7)')

    # CLR.W -(A7)
    elif op == 0x4267:
        pc += 2
        print(f'{addr:08X}  {hx(s,pc):<28s}  CLR.W   -(A7)')

    # NOT.L (A7)
    elif op == 0x4697:
        pc += 2
        print(f'{addr:08X}  {hx(s,pc):<28s}  NOT.L   (A7)')

    # MOVEM.L regs,-(A7)
    elif op == 0x48E7:
        mask = r16(pc+2); pc += 4
        regs = []
        for i in range(8):
            if mask & (1 << (15-i)): regs.append(f'D{i}')
        for i in range(8):
            if mask & (1 << (7-i)): regs.append(f'A{i}')
        print(f'{addr:08X}  {hx(s,pc):<28s}  MOVEM.L {"/".join(regs)},-(A7)')

    # LEA d16(A7),A5
    elif op == 0x4BEF:
        d = s16(pc+2); pc += 4
        print(f'{addr:08X}  {hx(s,pc):<28s}  LEA     {d}(A7),A5')

    # LEA d16(A7),A7
    elif op == 0x4FEF:
        d = s16(pc+2); pc += 4
        print(f'{addr:08X}  {hx(s,pc):<28s}  LEA     {d}(A7),A7')

    # MOVEA.L A7,A4
    elif op == 0x284F:
        pc += 2
        print(f'{addr:08X}  {hx(s,pc):<28s}  MOVEA.L A7,A4')

    # CLR.L d16(A4)
    elif op == 0x42AC:
        d = s16(pc+2); pc += 4
        print(f'{addr:08X}  {hx(s,pc):<28s}  CLR.L   {d}(A4)')

    # MOVE.L (A5)+,D3
    elif op == 0x261D:
        pc += 2
        print(f'{addr:08X}  {hx(s,pc):<28s}  MOVE.L  (A5)+,D3')

    # MOVE.L D3,d16(A4)
    elif op == 0x2943:
        d = s16(pc+2); pc += 4
        print(f'{addr:08X}  {hx(s,pc):<28s}  MOVE.L  D3,{d}(A4)')

    # MOVE.L A5,d16(A4)
    elif op == 0x294D:
        d = s16(pc+2); pc += 4
        print(f'{addr:08X}  {hx(s,pc):<28s}  MOVE.L  A5,{d}(A4)')

    # MOVE USP,A0
    elif op == 0x4E68:
        pc += 2
        print(f'{addr:08X}  {hx(s,pc):<28s}  MOVE    USP,A0')

    # MOVE.L A0,d16(A4)
    elif op == 0x2948:
        d = s16(pc+2); pc += 4
        print(f'{addr:08X}  {hx(s,pc):<28s}  MOVE.L  A0,{d}(A4)')

    # BSR with 16-bit displacement
    elif op == 0x6100:
        disp = s16(pc+2); pc += 4
        target = addr + 2 + disp
        print(f'{addr:08X}  {hx(s,pc):<28s}  BSR     ${target:08X}')

    # BSR.S with 8-bit displacement
    elif (op & 0xFF00) == 0x6100 and (op & 0xFF) != 0:
        disp = sb(op & 0xFF); pc += 2
        target = addr + 2 + disp
        print(f'{addr:08X}  {hx(s,pc):<28s}  BSR.S   ${target:08X}')

    # TST.B D0
    elif op == 0x4A00:
        pc += 2
        print(f'{addr:08X}  {hx(s,pc):<28s}  TST.B   D0')

    else:
        pc += 2
        print(f'{addr:08X}  {hx(s,pc):<28s}  DC.W    ${op:04X}')

# --- JMP at FC30FA ---
print()
print("--- JMP at FC30FA ---")
off = 0x30FA
d = rom[off:off+6]
tgt = struct.unpack('>I', d[2:6])[0]
print(f'00FC30FA  {" ".join(f"{b:02X}" for b in d):<28s}  JMP     ${tgt:08X}')
