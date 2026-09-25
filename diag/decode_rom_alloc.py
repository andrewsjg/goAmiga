#!/usr/bin/env python3
"""Decode FD149E (AllocRaster/temp alloc), FC6CDC (BltClear?), FC6CA0, FC6C18"""
import capstone, struct

with open('Kick13.rom', 'rb') as f:
    rom = f.read()

ROM_BASE = 0xFC0000
md = capstone.Cs(capstone.CS_ARCH_M68K, capstone.CS_MODE_M68K_000)

EXEC_LVO = {-0x0C6: "AllocMem", -0x0D2: "FreeMem", -0x06C: "Alert",
             -0x078: "Disable", -0x07E: "Enable", -0x228: "CopyMem",
             -0x0BA: "Allocate", -0x0C0: "Deallocate", -0x162: "BltClear"}
GFX_LVO = {-0x01E: "BltBitMap", -0x162: "BltClear", -0x11A: "WaitBlit",
            -0x1BC: "InitBitMap", -0x1F6: "ClipBlit"}

def read_word(off):
    return struct.unpack_from('>H', rom, off)[0] if 0 <= off < len(rom)-1 else None

def disasm(start_off, end_off, title):
    print(f"\n{'='*80}")
    print(f"  {title}")
    print(f"  ROM ${start_off:04X}-${end_off:04X}  PC=${start_off+ROM_BASE:08X}-${end_off+ROM_BASE:08X}")
    print(f"{'='*80}")
    code = rom[start_off:end_off]
    decoded = {}
    for insn in md.disasm(code, start_off + ROM_BASE):
        decoded[insn.address] = insn
    addr = start_off + ROM_BASE
    end = end_off + ROM_BASE
    while addr < end:
        if addr in decoded:
            insn = decoded[addr]
            raw = ' '.join(f'{b:02X}' for b in insn.bytes)
            ann = ""
            import re
            if insn.mnemonic == 'jsr' and 'a6' in insn.op_str:
                m = re.search(r'-?\$([0-9a-fA-F]+)\(a6\)', insn.op_str)
                if m:
                    v = int(m.group(1), 16)
                    if v > 0x7FFF: v -= 0x10000
                    else: v = -v
                    name = EXEC_LVO.get(v, "") or GFX_LVO.get(v, "")
                    if name: ann = f"  ; {name}"
            print(f"  {addr:08X}: {raw:<24s} {insn.mnemonic:<8s} {insn.op_str}{ann}")
            if insn.mnemonic == 'rts':
                print(f"  {'─'*70}")
            addr += insn.size
        else:
            w = read_word(addr - ROM_BASE)
            print(f"  {addr:08X}: {w>>8:02X} {w&0xFF:02X}                     dc.w     ${w:04X}")
            addr += 2

# FD149E - AllocRaster (temp bitmap allocation)
disasm(0x1149E, 0x114F0, "FD149E - AllocRaster (temp bitmap alloc)")

# FC6CDC - BltClear wrapper?
disasm(0x6CDC, 0x6DB6, "FC6CDC - BltClear/fill function?")

# FC6CA0 - InitBitMap?
disasm(0x6CA0, 0x6CDC, "FC6CA0 - InitBitMap?")

# FC6C18 - BltBitMap call?
disasm(0x6C18, 0x6CA0, "FC6C18 - internal BltBitMap?")

# FD3C4C - FreeBitMap/FreeRaster?
disasm(0x13C4C, 0x13CA0, "FD3C4C - FreeRaster?")

# FD3D50 - another free function
disasm(0x13D50, 0x13DA0, "FD3D50 - another free?")

# Now specifically search for where template is FILLED in FC9B38 flow:
# FC9B38 calls:
# 1. FD3B4E (byte_offset = muls(width_words, num_rows))  → D5 = buffer size
# 2. FD149E (AllocRaster? push $2, push D5) → D6 = allocated buffer ptr
# 3. FC6CDC (push 0, push D5, push D6) → clear/fill the buffer
# 4. FC6CA0 twice → InitBitMap for source and dest temp
# 5. FC6C18 → BltBitMap between source and temp

# So the FILL happens in FC6CDC! Let's look at it again more carefully
print("\n\n" + "#"*80)
print("# KEY FINDING: The fill of the temp buffer is done by FC6CDC")
print("# which is called with args: (buffer_ptr, buffer_size, 0)")
print("# The 0 means fill with zero! Not $FF!")
print("#"*80)

# Wait - let me re-check FC9B76:
# 42 A7 = CLR.L -(A7) = push 0
# 2F 05 = MOVE.L D5,-(A7) = push buffer_size
# 2F 06 = MOVE.L D6,-(A7) = push buffer_ptr
# 4EB9 FC6CDC = JSR FC6CDC
# So args: FC6CDC(buffer_ptr, buffer_size, 0)
# The 0 could be the fill pattern!

# But wait - the question says $FF is written, not $00
# Let me check if there's a SECOND fill elsewhere
print("\nRechecking FC9B38 flow around FC6CDC call:")
print("  FC9B70: CLR.L -(A7)     ; push fill_value = 0")
print("  FC9B72: MOVE.L D5,-(A7) ; push buffer_size")
print("  FC9B74: MOVE.L D6,-(A7) ; push buffer_ptr")
print("  FC9B76: JSR FC6CDC      ; fill buffer with 0")
print("")
print("So the helper at FC9B38 CLEARS the temp buffer to $00, NOT fills with $FF!")
print("The $FF fill you observed might be from the blitter itself")
print("(channel A data register set to the minterm pattern)")
