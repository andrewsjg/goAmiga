#!/usr/bin/env python3
import struct

with open('Kick13.rom', 'rb') as f:
    data = f.read()

base = 0xFC0000

def word_at(addr):
    off = addr - base
    return struct.unpack('>H', data[off:off+2])[0]

def long_at(addr):
    off = addr - base
    return struct.unpack('>I', data[off:off+4])[0]

def dump(start_addr, count=30):
    off = start_addr - base
    for i in range(count):
        if off+i*2+1 >= len(data):
            break
        w = struct.unpack('>H', data[off+i*2:off+i*2+2])[0]
        print(f'  ${start_addr+i*2:08X}: ${w:04X}')

# Find all branch/jump instructions that could lead to FC021A
target = 0xFC021A
print(f'Looking for branches TO ${target:08X}:')
for off in range(0, len(data)-3, 2):
    w = struct.unpack('>H', data[off:off+2])[0]
    addr = base + off
    # BCC.B (short branch): $64XX
    op = w >> 8
    if op in [0x60, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67,
              0x68, 0x69, 0x6A, 0x6B, 0x6C, 0x6D, 0x6E, 0x6F]:
        disp = w & 0xFF
        if disp == 0:  # word displacement
            continue
        if disp >= 0x80:
            disp -= 0x100
        dest = addr + 2 + disp
        if dest == target:
            print(f'  Branch from ${addr:08X}: ${w:04X}')
    # BCC.W (word branch): $60XX where low byte = 0
    # Actually for word displacement: opcode word has displacement=0x00
    elif op in [0x60, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67,
                0x68, 0x69, 0x6A, 0x6B, 0x6C, 0x6D, 0x6E, 0x6F] and (w & 0xFF) == 0:
        if off+3 < len(data):
            disp = struct.unpack('>h', data[off+2:off+4])[0]
            dest = addr + 2 + disp
            if dest == target:
                print(f'  Branch.W from ${addr:08X}: ${w:04X} disp=${disp:04X}')

print()
print(f'Area around FC021A (warm start check):')
dump(0xFC0200, 50)
print()
print(f'Area $FC0600-$FC0640 (after RESET handler):')
dump(0xFC0600, 30)
