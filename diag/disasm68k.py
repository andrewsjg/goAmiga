#!/usr/bin/env python3
"""Disassemble 68000/68020 code from a binary file (needs the capstone module).

    diag/disasm68k.py FILE [BASE [OFFSET [LENGTH]]] [--020]

BASE is the address the file's first byte loads at (hex), OFFSET and LENGTH
select part of it (hex, from the start of the file). Memory dumps from the
emulator, bootblocks and hunks all work.
"""
import sys
import capstone

args = [a for a in sys.argv[1:] if not a.startswith("--")]
mode = capstone.CS_MODE_M68K_020 if "--020" in sys.argv else capstone.CS_MODE_M68K_000
data = open(args[0], "rb").read()
base = int(args[1], 16) if len(args) > 1 else 0
off = int(args[2], 16) if len(args) > 2 else 0
length = int(args[3], 16) if len(args) > 3 else len(data) - off
md = capstone.Cs(capstone.CS_ARCH_M68K, mode)
code = data[off:off + length]
pos = 0
while pos < len(code):
    done = False
    for ins in md.disasm(code[pos:], base + off + pos):
        words = " ".join("%02X%02X" % (ins.bytes[i], ins.bytes[i + 1]) for i in range(0, len(ins.bytes), 2))
        print("%06X  %-20s %s %s" % (ins.address, words, ins.mnemonic, ins.op_str))
        pos += ins.size
        done = True
    if pos < len(code):  # capstone stops at an undecodable word
        print("%06X  %02X%02X                 dc.w" % (base + off + pos, code[pos], code[pos + 1] if pos + 1 < len(code) else 0))
        pos += 2
