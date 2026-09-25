#!/usr/bin/env python3
import struct

with open('Kick13.rom', 'rb') as f:
    data = f.read()

base = 0xFC0000

def dump(start_addr, count=30):
    off = start_addr - base
    for i in range(count):
        if off+i*2+1 >= len(data):
            break
        w = struct.unpack('>H', data[off+i*2:off+i*2+2])[0]
        print(f'  ${start_addr+i*2:08X}: ${w:04X}')

print('From $FC0100 (OVL clear, chip RAM detection, warm start):')
dump(0xFC0100, 80)
