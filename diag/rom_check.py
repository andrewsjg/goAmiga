import struct
rom = open('Kick13.rom','rb').read()
base = 0xFC0000

def w(off):
    return struct.unpack_from('>H', rom, off)[0]

def l(off):
    return struct.unpack_from('>I', rom, off)[0]

print('=== Vector init table at ROM+0x05B6 (46 words) ===')
for i in range(0, 48, 2):
    val = w(0x05B6 + i)
    addr_in_chipRAM = 8 + i
    print(f'  chip_RAM[${addr_in_chipRAM:03X}] <- ROM[${0x05B6+i:04X}] = ${val:04X}')

print()
print('=== Bus error vector as LONG: ===')
long_val = l(0x05B6)
print(f'  chip_RAM[$008] (bus_err_vec) = ${long_val:08X}')
long_val2 = l(0x05B8)
print(f'  chip_RAM[$00A] (addr_err_vec part2) = ${long_val2:08X}')
addr_err = (l(0x05BA) << 16) | (l(0x05BC))
# Actually chip_RAM[$8..B] = 4 bytes = 2 consecutive word copies
chip8 = (w(0x05B6) << 16) | w(0x05B8)
chip_c = (w(0x05BA) << 16) | w(0x05BC)
print(f'  chip_RAM[$008..00B] as long = ${chip8:08X} (bus error vector)')
print(f'  chip_RAM[$00C..00F] as long = ${chip_c:08X} (address error vector)')

print()
print('=== BSR-table at FC0816 ===')
for i in range(0, 24, 2):
    val = w(0x0816 + i)
    addr = 0xFC0816 + i
    print(f'  ${addr:08X}: ${val:04X}')

print()
# Decode the BSR.B at FC0816
op = w(0x0816)
print(f'BSR.B at FC0816: opcode=${op:04X}')
# BSR format: opcode = 0x6100 for BSR WORD, but BSR.B uses lower byte for displacement
# BSR.B = 0x61XX where XX != 0x00 (0x00 means BSR.W with next word as displacement)
if (op & 0xFF00) == 0x6100 and (op & 0xFF) != 0:
    disp = op & 0xFF
    if disp >= 0x80:
        disp -= 0x100
    print(f'  BSR.B with displacement={disp} (${disp & 0xFF:02X})')
    target = (0xFC0816 + 2) + disp
    print(f'  Target = ${target:08X}')
else:
    print(f'  Not BSR.B? opcode=${op:04X}')
