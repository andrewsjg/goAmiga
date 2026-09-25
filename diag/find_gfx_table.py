#!/usr/bin/env python3
"""Find graphics library function table entries."""
import struct

rom = open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom', 'rb').read()
BASE = 0xFC0000

# Search for address $FCB11E which referenced BltClear ($FC54C4)
print("=== What's at $FCB11E area ===")
off = 0xB11E
for i in range(off - 20, off + 30, 2):
    val = struct.unpack_from('>I', rom, i)[0]
    if BASE <= val < BASE + len(rom):
        print(f"  ${BASE+i:06X}: ${val:08X}")

# Search for graphics library resident tag
# A resident structure starts with RTC_MATCHWORD = 0x4AFC
print("\n=== Searching for Resident tags (0x4AFC) ===")
for i in range(0, len(rom) - 25, 2):
    w = struct.unpack_from('>H', rom, i)[0]
    if w == 0x4AFC:
        # matchTag should point back to itself
        match_tag = struct.unpack_from('>I', rom, i + 2)[0]
        if match_tag == BASE + i:
            end_skip = struct.unpack_from('>I', rom, i + 6)[0]
            flags = rom[i + 10]
            version = rom[i + 11]
            node_type = rom[i + 12]
            pri = rom[i + 13]
            name_ptr = struct.unpack_from('>I', rom, i + 14)[0]
            # Read name string
            if BASE <= name_ptr < BASE + len(rom):
                name_off = name_ptr - BASE
                name = ""
                for j in range(name_off, min(name_off + 30, len(rom))):
                    if rom[j] == 0:
                        break
                    name += chr(rom[j])
            else:
                name = "?"
            print(f"  ${BASE+i:06X}: Resident '{name}' v{version} type={node_type} pri={pri}")
            
            # If this is graphics.library, dump its init table
            if 'graphics' in name.lower():
                init_ptr = struct.unpack_from('>I', rom, i + 18)[0]
                print(f"    Init routine: ${init_ptr:06X}")
                # The structure table and function table are set up by InitResident
                # Let's look at the function table
                # Look for a struct InitStruct call pattern
                # Actually, the library uses MakeFunctions with a function table
                # The function table is typically referenced from the init code
                # Let's look near the init routine
                
                # For V1.3 graphics.library, the function table is usually 
                # an array of relative word offsets from the table base
                # Find it by looking near the resident structure
                
                # Search for function dispatch table
                # Graphics library function tables use negative word offsets
                # SetRast is LVO -$EA
                # BltBitMap is LVO -$1E
                # BltClear is LVO -$12C
                # BltTemplate is LVO -$24
                print(f"    Searching for funcInit table near resident...")

# Look for where BltBitMap LVO implementation address is stored
# Search for FC9C08 (which is called from FC5C8A for BltBitMap dispatch)
print("\n=== Searching for $FC9C08 (BltBitMap?) references ===")
target = 0xFC9C08
for i in range(0, len(rom) - 5, 2):
    w = struct.unpack_from('>H', rom, i)[0]
    if w == 0x4EB9:  # JSR
        addr = struct.unpack_from('>I', rom, i + 2)[0]
        if addr == target:
            print(f"  JSR at ${BASE+i:06X} -> ${target:06X}")
    elif w == 0x4EF9:  # JMP
        addr = struct.unpack_from('>I', rom, i + 2)[0]
        if addr == target:
            print(f"  JMP at ${BASE+i:06X} -> ${target:06X}")

# Search for $FCEEE0 references (also called from BltBitMap area)
print("\n=== Searching for $FCEEE0 references ===")
target = 0xFCEEE0
for i in range(0, len(rom) - 5, 2):
    w = struct.unpack_from('>H', rom, i)[0]
    if w in (0x4EB9, 0x4EF9):
        addr = struct.unpack_from('>I', rom, i + 2)[0]
        if addr == target:
            typ = "JSR" if w == 0x4EB9 else "JMP"
            print(f"  {typ} at ${BASE+i:06X} -> ${target:06X}")

# Let me check what's at $FC9C08 
from capstone import Cs, CS_ARCH_M68K, CS_MODE_M68K_000
md = Cs(CS_ARCH_M68K, CS_MODE_M68K_000)

print("\n=== Function $FC9C08 (BltBitMap dispatch?) ===")
region = rom[0x9C08:0x9C08+120]
for insn in md.disasm(region, 0xFC9C08):
    hex_bytes = ' '.join(f'{b:02x}' for b in insn.bytes)
    print(f'  ${insn.address:06X}: {hex_bytes:24s} {insn.mnemonic:10s} {insn.op_str}')

print("\n=== Function $FCEEE0 (BltBitMapRastPort?) ===")
region = rom[0xEEE0:0xEEE0+120]
for insn in md.disasm(region, 0xFCEEE0):
    hex_bytes = ' '.join(f'{b:02x}' for b in insn.bytes)
    print(f'  ${insn.address:06X}: {hex_bytes:24s} {insn.mnemonic:10s} {insn.op_str}')
