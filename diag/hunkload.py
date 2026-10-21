#!/usr/bin/env python3
"""Load an Amiga hunk executable (a library, a program) as an image in
memory, every hunk at its own base and the relocations applied, so that
absolute references can be followed and disassembled.

    python3 diag/hunkload.py FILE OUT.bin [BASE]

Hunk i loads at BASE + its offset in the image (hunks follow one another,
longword aligned); BASE is hex, default 0x100000. Prints each hunk's
address and size.
"""
import struct
import sys


def load(path, base=0x100000):
    d = open(path, "rb").read()
    p = 0

    def L():
        nonlocal p
        v = struct.unpack(">L", d[p:p + 4])[0]
        p += 4
        return v

    assert L() == 0x3F3, "not a hunk executable"
    while L() != 0:
        pass
    L()
    first, last = L(), L()
    sizes = [(L() & 0x3FFFFFFF) * 4 for _ in range(last - first + 1)]
    addrs, a = [], base
    for s in sizes:
        addrs.append(a)
        a += (s + 3) & ~3
    img = bytearray(a - base)
    h = -1
    while p < len(d):
        t = L() & 0x3FFFFFFF
        if t in (0x3E9, 0x3EA):  # code, data
            h += 1
            n = L() * 4
            off = addrs[h] - base
            img[off:off + n] = d[p:p + n]
            p += n
        elif t == 0x3EB:  # bss
            h += 1
            L()
        elif t == 0x3EC:  # reloc32
            while True:
                c = L()
                if c == 0:
                    break
                target = L()
                for _ in range(c):
                    o = addrs[h] - base + L()
                    v = struct.unpack(">L", img[o:o + 4])[0] + addrs[target]
                    img[o:o + 4] = struct.pack(">L", v & 0xFFFFFFFF)
        elif t in (0x3F7, 0x3FC):  # reloc32short: word counts and offsets
            def W():
                nonlocal p
                v = struct.unpack(">H", d[p:p + 2])[0]
                p += 2
                return v
            start = p
            while True:
                c = W()
                if c == 0:
                    break
                target = W()
                for _ in range(c):
                    o = addrs[h] - base + W()
                    v = struct.unpack(">L", img[o:o + 4])[0] + addrs[target]
                    img[o:o + 4] = struct.pack(">L", v & 0xFFFFFFFF)
            p += (p - start) & 2  # longword aligned
        elif t == 0x3F0:  # symbols
            while True:
                c = L()
                if c == 0:
                    break
                p += 4 * (c & 0xFFFFFF) + 4
        elif t == 0x3F1:  # debug
            p += 4 * L()
        elif t == 0x3F2:  # end of hunk
            pass
        else:
            raise ValueError("hunk type %x" % t)
    return bytes(img), addrs, sizes


if __name__ == "__main__":
    base = int(sys.argv[3], 16) if len(sys.argv) > 3 else 0x100000
    img, addrs, sizes = load(sys.argv[1], base)
    open(sys.argv[2], "wb").write(img)
    for i, (a, s) in enumerate(zip(addrs, sizes)):
        print("hunk %d at $%06X, %d bytes" % (i, a, s))
