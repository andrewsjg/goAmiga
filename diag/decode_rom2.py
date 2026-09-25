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

def read_long(addr):
    off = addr - ROM_BASE
    if 0 <= off < ROM_SIZE-3:
        return (data[off] << 24) | (data[off+1] << 16) | (data[off+2] << 8) | data[off+3]
    return None

# Decode FC0D14 - Level 3 interrupt handler
print("=== FC0D14 Level 3 Handler ===")
addr = 0xFC0D14
for i in range(20):
    w = read_word(addr)
    if w is None:
        break
    print(f"  {addr:08X}: {w:04X}")
    addr += 2

# Also check FC1230-FC1260 area (where Level 3 interrupt triggered)
print("\n=== FC1230-FC1260 (near where Level 3 fired) ===")
addr = 0xFC1230
for i in range(20):
    w = read_word(addr)
    if w is None:
        break
    print(f"  {addr:08X}: {w:04X}")
    addr += 2

# Check FC0EC0 area (RTE target)
print("\n=== FC0EB0-FC0ED0 area ===")
addr = 0xFC0EB0
for i in range(12):
    w = read_word(addr)
    if w is None:
        break
    print(f"  {addr:08X}: {w:04X}")
    addr += 2

# Check FC12FA area (interrupt vector install)
print("\n=== FC12E0-FC1310 (interrupt init) ===")
addr = 0xFC12E0
for i in range(24):
    w = read_word(addr)
    if w is None:
        break
    print(f"  {addr:08X}: {w:04X}")
    addr += 2
