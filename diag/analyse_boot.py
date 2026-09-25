#!/usr/bin/env python3
import struct

with open('Kick13.rom', 'rb') as f:
    data = f.read()

if len(data) == 512*1024:
    data = data[256*1024:]

print(f'ROM size: {len(data)} bytes ({len(data)//1024}KB)')
print(f'SSP: ${struct.unpack(">I", data[0:4])[0]:08X}')
print(f'PC:  ${struct.unpack(">I", data[4:8])[0]:08X}')
print()

def dump(start_addr, count=30):
    base = 0xFC0000
    off = start_addr - base
    for i in range(count):
        if off+i*2+1 >= len(data):
            break
        w = struct.unpack('>H', data[off+i*2:off+i*2+2])[0]
        print(f'  ${start_addr+i*2:08X}: ${w:04X}')

print('Cold start at $FC00D2:')
dump(0xFC00D2, 30)
print()
print('Area $FC0210-$FC0260 (warm start check?):')
dump(0xFC0210, 40)
print()
print('After RESET $FC05FA:')
dump(0xFC05F0, 20)
