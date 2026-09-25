#!/usr/bin/env python3
"""Find function entry points and callers for blitter-related addresses."""
import struct

rom = open('Kick13.rom', 'rb').read()
BASE = 0xFC0000

targets = {
    'FC5E12_BltBitMap_inner': 0xFC5E12,
    'FC577E_DrawImage_line': 0xFC577E,
    'FC5C58_BltClear_plane': 0xFC5C58,
    'FC7630_QBlit_handler': 0xFC7630,
    'FC553A_BltClear_main': 0xFC553A,
}

for name, target in targets.items():
    off = target - BASE
    entry = None
    for i in range(off - 2, max(0, off - 600), -2):
        w = struct.unpack_from('>H', rom, i)[0]
        if w == 0x4E75:
            entry = BASE + i + 2
            break

    print(f'\n=== {name} ===')
    print(f'  Target: ${target:06X}')
    if entry:
        print(f'  Function entry (after preceding RTS): ${entry:06X}')
    else:
        print(f'  Entry not found within 600 bytes')
        continue

    # Search for all callers
    callers = []
    for i in range(0, len(rom) - 5, 2):
        w = struct.unpack_from('>H', rom, i)[0]
        if w == 0x4EB9:  # JSR abs.L
            addr = struct.unpack_from('>I', rom, i + 2)[0]
            if addr == entry:
                callers.append((BASE + i, 'JSR', addr))
        elif w == 0x6100:  # BSR.W
            disp = struct.unpack_from('>h', rom, i + 2)[0]
            dest = BASE + i + 2 + disp
            if dest == entry:
                callers.append((BASE + i, 'BSR.W', dest))
        elif (w >> 8) == 0x61 and (w & 0xFF) not in (0x00, 0xFF):
            disp = w & 0xFF
            if disp & 0x80:
                disp -= 256
            dest = BASE + i + 2 + disp
            if dest == entry:
                callers.append((BASE + i, 'BSR.B', dest))

    if callers:
        print(f'  Callers of ${entry:06X}:')
        for caller, typ, dest in callers:
            print(f'    {typ} at ${caller:06X} -> ${dest:06X}')
    else:
        print(f'  No direct callers found for ${entry:06X}')
        # Try scanning for entry-2 (in case of alignment/data)
        for alt in [entry - 2, entry + 2, entry - 4, entry + 4]:
            alt_callers = []
            for i in range(0, len(rom) - 5, 2):
                w = struct.unpack_from('>H', rom, i)[0]
                if w == 0x4EB9:
                    addr = struct.unpack_from('>I', rom, i + 2)[0]
                    if addr == alt:
                        alt_callers.append((BASE + i, 'JSR', addr))
            if alt_callers:
                print(f'  But found callers for ${alt:06X}:')
                for c, t, d in alt_callers:
                    print(f'    {t} at ${c:06X} -> ${d:06X}')

# Also identify the graphics.library jump table
# Look for the jump table by finding known LVO offsets
print('\n\n=== Graphics Library Jump Table Search ===')
# The graphics library base is typically loaded from a library open
# Let's find SetRast LVO (-$EA from gfxbase) implementation
# Search for addresses that might be in a jump table pointing to our functions
# The jump table entries are typically JMP abs.L (4EF9 xxxx xxxx)

for name, target in targets.items():
    off = target - BASE
    # Find function entry
    entry = None
    for i in range(off - 2, max(0, off - 600), -2):
        w = struct.unpack_from('>H', rom, i)[0]
        if w == 0x4E75:
            entry = BASE + i + 2
            break
    if not entry:
        continue

    # Search for JMP to this entry
    for i in range(0, len(rom) - 5, 2):
        w = struct.unpack_from('>H', rom, i)[0]
        if w == 0x4EF9:  # JMP abs.L
            addr = struct.unpack_from('>I', rom, i + 2)[0]
            if addr == entry:
                print(f'  JMP at ${BASE + i:06X} -> ${entry:06X} ({name})')

# Also find the OwnBlitter/DisownBlitter/WaitBlit helper at FC5A58
print('\n=== WaitBlit helper at $FC5A58 ===')
# Disassemble
from capstone import Cs, CS_ARCH_M68K, CS_MODE_M68K_000
md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)
region = rom[0x5A56:0x5A7C]
for insn in md.disasm(region, 0xFC5A56):
    hex_bytes = ' '.join(f'{b:02x}' for b in insn.bytes)
    print(f'  ${insn.address:06X}: {hex_bytes:24s} {insn.mnemonic:10s} {insn.op_str}')

# Find the "nesting counter" pattern - ADDQ #1,$AA(A6) / BEQ / BSR $FC64C4
print('\n=== OwnBlitter helper at $FC64C4 ===')
region = rom[0x64C4:0x64EA]
for insn in md.disasm(region, 0xFC64C4):
    hex_bytes = ' '.join(f'{b:02x}' for b in insn.bytes)
    print(f'  ${insn.address:06X}: {hex_bytes:24s} {insn.mnemonic:10s} {insn.op_str}')

print('\n=== DisownBlitter helper at $FC64DA ===')
region = rom[0x64DA:0x6510]
for insn in md.disasm(region, 0xFC64DA):
    hex_bytes = ' '.join(f'{b:02x}' for b in insn.bytes)
    print(f'  ${insn.address:06X}: {hex_bytes:24s} {insn.mnemonic:10s} {insn.op_str}')
