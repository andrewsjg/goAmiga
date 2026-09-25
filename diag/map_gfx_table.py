#!/usr/bin/env python3
"""Map graphics library function table to LVO offsets."""
import struct

rom = open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb').read()
BASE = 0xFC0000

gfx_funcs = {
    -0x1E: 'BltBitMap', -0x24: 'BltTemplate', -0x2A: 'ClearEOL',
    -0x30: 'ClearScreen', -0x36: 'TextLength', -0x3C: 'Text',
    -0x42: 'SetFont', -0x48: 'OpenFont', -0x4E: 'CloseFont',
    -0x54: 'AskSoftStyle', -0x5A: 'SetSoftStyle',
    -0x60: 'AddBob', -0x66: 'AddVSprite', -0x6C: 'DoCollision',
    -0x72: 'DrawGList', -0x78: 'InitGels', -0x7E: 'InitMasks',
    -0x84: 'RemIBob', -0x8A: 'RemVSprite', -0x90: 'SetCollision',
    -0x96: 'SortGList', -0x9C: 'AddAnimOb', -0xA2: 'Animate',
    -0xA8: 'GetGBuffers', -0xAE: 'InitGMasks', -0xB4: 'DrawEllipse',
    -0xBA: 'AreaEllipse', -0xC0: 'LoadRGB4', -0xC6: 'InitRastPort',
    -0xCC: 'InitVPort', -0xD2: 'MrgCop', -0xD8: 'MakeVPort',
    -0xDE: 'LoadView', -0xE4: 'WaitBlit', -0xEA: 'SetRast',
    -0xF0: 'Move', -0xF6: 'Draw', -0xFC: 'AreaMove',
    -0x102: 'AreaDraw', -0x108: 'AreaEnd', -0x10E: 'WaitTOF',
    -0x114: 'QBlit', -0x11A: 'InitArea', -0x120: 'SetRGB4',
    -0x126: 'QBSBlit', -0x12C: 'BltClear', -0x132: 'RectFill',
    -0x138: 'BltPattern', -0x13E: 'ReadPixel', -0x144: 'WritePixel',
    -0x14A: 'Flood', -0x150: 'PolyDraw', -0x156: 'SetAPen',
    -0x15C: 'SetBPen', -0x162: 'SetDrMd', -0x168: 'InitView',
    -0x16E: 'CBump', -0x174: 'CMove', -0x17A: 'CWait',
    -0x180: 'VBeamPos', -0x186: 'InitBitMap', -0x18C: 'ScrollRaster',
    -0x192: 'WaitBOVP', -0x198: 'GetSprite', -0x19E: 'FreeSprite',
    -0x1A4: 'ChangeSprite', -0x1AA: 'MoveSprite', -0x1B0: 'LockLayerRom',
    -0x1B6: 'UnlockLayerRom', -0x1BC: 'SyncSBitMap', -0x1C2: 'CopySBitMap',
    -0x1C8: 'OwnBlitter', -0x1CE: 'DisownBlitter', -0x1D4: 'InitTmpRas',
    -0x1DA: 'AskFont', -0x1E0: 'AddFont', -0x1E6: 'RemFont',
    -0x1EC: 'AllocRaster', -0x1F2: 'FreeRaster',
    -0x228: 'ClipBlit', -0x25E: 'BltBitMapRastPort',
    -0x27C: 'BltMaskBitMapRastPort',
}

# Find the function table
# We know from the previous scan that $FCB11E has $FC54C4 (BltClear)
# The table seems to be at 4-byte intervals
# Let's find where it starts by scanning backwards from $FCB11E
off = 0xB11E
while off > 0xB000:
    off -= 4
    val = struct.unpack_from('>I', rom, off)[0]
    if not (BASE <= val < BASE + len(rom)):
        off += 4  # go back to first valid entry
        break

print(f'Table starts at ${BASE+off:06X}')

# Count entries
count = 0
for j in range(off, min(off + 600, len(rom)), 4):
    v = struct.unpack_from('>I', rom, j)[0]
    if BASE <= v < BASE + len(rom):
        count += 1
    else:
        break

print(f'Table has {count} entries\n')

# Map entries to LVOs
# First entry = LVO -$1E (BltBitMap), each subsequent -6
lvo = -0x1E
for idx in range(count):
    addr = struct.unpack_from('>I', rom, off + idx * 4)[0]
    func_name = gfx_funcs.get(lvo, '')
    marker = ''
    if func_name:
        marker = f'  <-- {func_name}'
    print(f'  [{idx:3d}] LVO -${(-lvo):03X}: ${addr:06X}{marker}')
    lvo -= 6
