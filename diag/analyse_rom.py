#!/usr/bin/env python3
import struct

rom = open('Kick13.rom', 'rb').read()

def r16(addr):
    off = addr - 0xFC0000
    return struct.unpack('>H', rom[off:off+2])[0]

def r32(addr):
    off = addr - 0xFC0000
    return struct.unpack('>I', rom[off:off+4])[0]

print("=== FC021C-FC0268 ===")
for i in range(28):
    addr = 0xFC021C + i*2
    print(f"  ${addr:06X}: {r16(addr):04X}")

print()
print("=== FC027C-FC02B0 ===")
for i in range(28):
    addr = 0xFC027C + i*2
    print(f"  ${addr:06X}: {r16(addr):04X}")

# Key instruction decoding
print()
print("=== Key decoded instructions ===")

# FC027E: 200E = MOVE.L A6, D0
addr = 0xFC027E
print(f"  ${addr:06X}: {r16(addr):04X}  (200E = MOVE.L A6, D0? -- A6=ExecBase)")

# FC0280: 4680 = NEG.L D0
addr = 0xFC0280
print(f"  ${addr:06X}: {r16(addr):04X}  (4680 = NEG.L D0)")

# FC0282-FC0285: 2D40 0026 = MOVE.L D0, ($26,A6)
addr = 0xFC0282
print(f"  ${addr:06X}: {r16(addr):04X} {r16(addr+2):04X}  (2D40 0026 = MOVE.L D0, ($26,A6) => ExecBase.MemTop)")

# FC0286: 200C = MOVE.L A4, D0
addr = 0xFC0286
print(f"  ${addr:06X}: {r16(addr):04X}  (200C = MOVE.L A4, D0)")

# FC0288: 6602 = BNE.S +2
addr = 0xFC0288
print(f"  ${addr:06X}: {r16(addr):04X}  (6602 = BNE.S $FC028C)")

# FC028A: 200B = MOVE.L A3, D0  
addr = 0xFC028A
print(f"  ${addr:06X}: {r16(addr):04X}  (200B = MOVE.L A3, D0)")

# FC028C: 2E40 = ?
addr = 0xFC028C
v1 = r16(addr)
print(f"  ${addr:06X}: {v1:04X}  (2E40 = MOVE.L D0,A7? => stack ptr from chip mem top)")

# FC028E: 2D40 0036
addr = 0xFC028E
print(f"  ${addr:06X}: {r16(addr):04X} {r16(addr+2):04X}  (2D40 0036 = MOVE.L D0,($36,A6) => MemHi)")

# FC0292: 0480 0000 1800
addr = 0xFC0292
print(f"  ${addr:06X}: {r16(addr):04X} {r16(addr+2):04X} {r16(addr+4):04X}  (0480 00001800 = SUB.L #$1800,D0)")

# FC0298: 2D40 003A
addr = 0xFC0298
print(f"  ${addr:06X}: {r16(addr):04X} {r16(addr+2):04X}  (2D40 003A = MOVE.L D0,($3A,A6) => SysBase...)")

# Then several more MOVE.Ls before AddMemList calls
# What are D0, D3 at the time of AddMemList call at FC02A4-FC02AB?

# IMPORTANT: BSR at FC02A4 calls FC3122 (AllocAbs/InitCode or similar)
# FA02A8 calls AddMemList?
bsr1_disp = r16(0xFC02A6)
bsr1_target = 0xFC02A8 + (bsr1_disp if bsr1_disp < 0x8000 else bsr1_disp - 0x10000)
print(f"\n  BSR at $FC02A4 displacement=${bsr1_disp:04X} -> target ${bsr1_target:06X}")

bsr2_disp = r16(0xFC02AA)
bsr2_target = 0xFC02AC + (bsr2_disp if bsr2_disp < 0x8000 else bsr2_disp - 0x10000)
print(f"  BSR at $FC02A8 displacement=${bsr2_disp:04X} -> target ${bsr2_target:06X}")

# Check what's at FC02AC
print(f"\n  After second BSR, code at $FC02AC: {r16(0xFC02AC):04X} {r16(0xFC02AE):04X} {r16(0xFC02B0):04X}")

# Let's see what's at the AddMemList target (FC0548?)
target2 = bsr2_target
print(f"\n=== Target of second BSR: ${target2:06X}-${target2+0x40:06X} ===")
for i in range(32):
    addr = target2 + i*2
    print(f"  ${addr:06X}: {r16(addr):04X}")
