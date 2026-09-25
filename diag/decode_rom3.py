#!/usr/bin/env python3
with open('Kick13.rom', 'rb') as f:
    data = f.read()

ROM_BASE = 0xFC0000
ROM_SIZE = 0x40000

def read_word(addr):
    off = addr - ROM_BASE
    if 0 <= off < ROM_SIZE-1:
        return (data[off] << 8) | data[off+1]
    return None

areas = [
    ("FC00D8", 0xFC00D8, 12),
    ("FC060C", 0xFC060C, 10),
    ("FC5EC8", 0xFC5EC8, 12),
    ("FCAFA0", 0xFCAFA0, 12),
]

for name, base, count in areas:
    print(f"\n=== {name} area ===")
    addr = base
    for i in range(count):
        w = read_word(addr)
        if w is None:
            break
        print(f"  {addr:08X}: {w:04X}")
        addr += 2
