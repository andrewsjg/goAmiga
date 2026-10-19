#!/usr/bin/env python3
"""List and extract files from an Amiga ADF or plain hardfile (OFS or FFS).

A plain hardfile (one partition, no RDB) works too: its root block is in
the middle, as for a floppy.

    python3 diag/adf.py list Workbench/Workbench3.2.adf
    python3 diag/adf.py find Workbench/Workbench3.2.adf Clock
    python3 diag/adf.py cat  Workbench/Workbench3.2.adf S/Startup-Sequence
    python3 diag/adf.py get  Workbench/Workbench3.2.adf Utilities/Clock /tmp/Clock
    python3 diag/adf.py where Workbench/Workbench3.2.adf S/Startup-Sequence
        (prints the byte offsets in the image of each data block)
"""
import struct
import sys

BS = 512


def blk(d, n):
    return d[n * BS:(n + 1) * BS]


def be(b, off):
    return struct.unpack(">L", b[off:off + 4])[0]


def name(b):
    return b[433:433 + b[432]].decode("latin-1")


def ffs(d):
    return d[3] & 1  # DOS\1, \3, \5, \7 are FFS


def entries(d, dirblock):
    """Yield (name, block, sec_type) for a directory's hash table."""
    b = blk(d, dirblock)
    for i in range(72):
        h = be(b, 24 + 4 * i)
        while h:
            e = blk(d, h)
            st = struct.unpack(">l", e[508:512])[0]
            yield name(e), h, st
            h = be(e, 496)


def root(d):
    """The root block: in the middle of the partition (880 on a floppy)."""
    return (len(d) // BS - 1 + 2) // 2


def walk(d, dirblock=None, prefix=""):
    if dirblock is None:
        dirblock = root(d)
    for n, h, st in sorted(entries(d, dirblock)):
        path = prefix + n
        if st == 2:  # directory
            yield path + "/", h, st
            yield from walk(d, h, path + "/")
        else:
            yield path, h, st


def lookup(d, path):
    cur = root(d)
    for part in path.strip("/").split("/"):
        for n, h, st in entries(d, cur):
            if n.lower() == part.lower():
                cur = h
                break
        else:
            raise SystemExit("not found: " + path)
    return cur


def data_blocks(d, header):
    """Data block numbers of a file, in order (via the header and extension blocks)."""
    out = []
    b = blk(d, header)
    while True:
        n = be(b, 8)  # high_seq: entries used in this block's table
        table = [be(b, 308 - 4 * i) for i in range(n)]
        out += [t for t in table if t]
        ext = be(b, 504)
        if not ext:
            return out
        b = blk(d, ext)


def read_file(d, header):
    size = be(blk(d, header), 324)
    data = b""
    for n in data_blocks(d, header):
        b = blk(d, n)
        data += b if ffs(d) else b[24:24 + be(b, 12)]
    return data[:size]


def main():
    cmd, image = sys.argv[1], sys.argv[2]
    d = open(image, "rb").read()
    if cmd == "list":
        for path, h, st in walk(d):
            size = "" if st == 2 else be(blk(d, h), 324)
            print(f"{path:50s} {size}")
    elif cmd == "find":
        for path, h, st in walk(d):
            if sys.argv[3].lower() in path.lower():
                print(path)
    elif cmd == "cat":
        sys.stdout.write(read_file(d, lookup(d, sys.argv[3])).decode("latin-1"))
    elif cmd == "get":
        open(sys.argv[4], "wb").write(read_file(d, lookup(d, sys.argv[3])))
    elif cmd == "where":
        h = lookup(d, sys.argv[3])
        hdr = 0 if ffs(d) else 24
        for n in data_blocks(d, h):
            print(n * BS + hdr)


if __name__ == "__main__":
    main()
