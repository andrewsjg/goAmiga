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
    ("FC00C0", 0xFC00C0, 24),   # before delay loop
    ("FC00E0", 0xFC00E0, 16),   # after delay loop
    ("FCAF90", 0xFCAF90, 20),   # before/around VPOS wait
    ("FCAFB0", 0xFCAFB0, 20),   # after VPOS wait
    ("FC0500", 0xFC0500, 10),   # exec scheduler call site
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
