#!/usr/bin/env python3
"""Read an s5 (System V) filesystem in a partition of an RDB hard disk image,
as Amiga Unix (AMIX 2.1) lays it out (1 KB blocks, 4-byte-aligned superblock).

    python3 diag/s5.py parts amix.hdf               (the RDB's partitions)
    python3 diag/s5.py super amix.hdf 1             (slice 1's superblock)
    python3 diag/s5.py ls    amix.hdf 1 /etc        (a directory)
    python3 diag/s5.py cat   amix.hdf 1 /etc/vfstab
    python3 diag/s5.py check amix.hdf 1             (walk every file and
                                                     count blocks and links)
"""
import struct
import sys

BS = 1024


def be32(b, o):
    return struct.unpack_from(">I", b, o)[0]


def be16(b, o):
    return struct.unpack_from(">H", b, o)[0]


def partitions(d):
    rdb = None
    for i in range(16):
        if d[i * 512:i * 512 + 4] == b"RDSK":
            rdb = i * 512
            break
    if rdb is None:
        raise SystemExit("no RDB")
    cylblocks = be32(d, rdb + 144)
    out = []
    nxt = be32(d, rdb + 28)
    while nxt != 0xFFFFFFFF:
        p = nxt * 512
        name = d[p + 37:p + 37 + d[p + 36]].decode("latin-1")
        low, high = be32(d, p + 128 + 36), be32(d, p + 128 + 40)
        out.append((name, low * cylblocks * 512, (high - low + 1) * cylblocks * 512, be32(d, p + 20)))
        nxt = be32(d, p + 16)
    return out


class S5:
    def __init__(self, d, base):
        self.d, self.base = d, base
        sb = self.sb = d[base + 512:base + 1024]
        self.isize, self.fsize = be16(sb, 0), be32(sb, 4)
        self.magic, self.type = be32(sb, 504), be32(sb, 508)

    def block(self, n):
        o = self.base + n * BS
        return self.d[o:o + BS]

    def inode(self, ino):
        o = self.base + 2 * BS + (ino - 1) * 64
        b = self.d[o:o + 64]
        addr = [int.from_bytes(b[12 + 3 * i:15 + 3 * i], "big") for i in range(13)]
        return dict(mode=be16(b, 0), nlink=be16(b, 2), uid=be16(b, 4), gid=be16(b, 6),
                    size=be32(b, 8), addr=addr, mtime=be32(b, 56))

    def blocks(self, ino):
        i = self.inode(ino)
        n = (i["size"] + BS - 1) // BS
        out = [a for a in i["addr"][:10]][:n]

        def ind(b, level):
            res = []
            blk = self.block(b)
            for k in range(BS // 4):
                e = be32(blk, 4 * k)
                if level == 1:
                    res.append(e)
                else:
                    res += ind(e, level - 1) if e else []
            return res
        for level in (1, 2, 3):
            if len(out) >= n:
                break
            out += ind(i["addr"][9 + level], level)
        return out[:n]

    def read(self, ino):
        i = self.inode(ino)
        return b"".join(self.block(b) for b in self.blocks(ino))[:i["size"]]

    def entries(self, ino):
        data = self.read(ino)
        for k in range(0, len(data), 16):
            n = be16(data, k)
            if n:
                yield data[k + 2:k + 16].rstrip(b"\0").decode("latin-1"), n

    def lookup(self, path):
        ino = 2
        for part in [p for p in path.split("/") if p]:
            for name, n in self.entries(ino):
                if name == part:
                    ino = n
                    break
            else:
                raise SystemExit("not found: " + path)
        return ino


def main():
    cmd, image = sys.argv[1], sys.argv[2]
    d = open(image, "rb").read()
    parts = partitions(d)
    if cmd == "parts":
        for i, (name, off, size, flags) in enumerate(parts):
            print(f"slice {i + 1}: {name:6s} offset {off:#x} size {size >> 20} MB flags {flags}")
        return
    fs = S5(d, parts[int(sys.argv[3]) - 1][1])
    if cmd == "super":
        print(f"isize {fs.isize} fsize {fs.fsize} magic {fs.magic:#x} type {fs.type} "
              f"tfree {be32(fs.sb, 432)} tinode {be16(fs.sb, 436)} nfree {be16(fs.sb, 8)} "
              f"state+time {(be32(fs.sb, 500) + be32(fs.sb, 420)) & 0xFFFFFFFF:#x}")
    elif cmd == "ls":
        for name, n in fs.entries(fs.lookup(sys.argv[4])):
            i = fs.inode(n)
            print(f"{n:6d} {i['mode']:06o} {i['nlink']:3d} {i['uid']:5d} {i['gid']:5d} {i['size']:9d} {name}")
    elif cmd == "cat":
        sys.stdout.buffer.write(fs.read(fs.lookup(sys.argv[4])))
    elif cmd == "check":
        used, links, seen = set(), {}, set()

        def walk(ino):
            for name, n in fs.entries(ino):
                links[n] = links.get(n, 0) + 1
                if name in (".", ".."):
                    continue
                i = fs.inode(n)
                if n in seen:
                    continue
                seen.add(n)
                if i["mode"] & 0o170000 in (0o020000, 0o060000):
                    continue
                for b in fs.blocks(n):
                    if b in used:
                        print("block used twice:", b, name)
                    used.add(b)
                if i["mode"] & 0o170000 == 0o040000:
                    walk(n)
        seen.add(2)
        for b in fs.blocks(2):
            used.add(b)
        walk(2)
        bad = [n for n, c in links.items() if fs.inode(n)["nlink"] != c]
        print(f"{len(seen)} inodes, {len(used)} data blocks, {len(bad)} link counts wrong")
        for n in bad[:10]:
            print("  inode", n, "nlink", fs.inode(n)["nlink"], "entries", links[n])


if __name__ == "__main__":
    main()
