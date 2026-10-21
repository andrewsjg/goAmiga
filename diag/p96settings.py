#!/usr/bin/env python3
"""Make a Picasso96 2.0 settings file for goAmiga's uaegfx RTG card.

Picasso96 2.0 offers only the screen modes in SYS:Devs/Picasso96Settings,
and only those for the board type in the file's STHD chunk. The presets
it ships (Devs/Picasso96Settings.31 etc.) say "no board" (0); this sets
uaegfx's (1) and names the resolutions "uaegfx:WxH", as Picasso96Mode's
"Attach settings to" would.

    python3 diag/p96settings.py PRESET OUT

e.g. PRESET = Picasso96Install/Devs/Picasso96Settings.31 from Picasso96.lha.
Copy OUT to SYS:Devs/Picasso96Settings on the Amiga and reboot.
"""
import struct
import sys


def attach(b: bytes, board: str = "uaegfx", board_type: int = 1) -> bytes:
    out = bytearray(b)
    if out[:4] != b"FORM" or out[8:12] != b"P96S":
        sys.exit("not a Picasso96 settings file (FORM P96S)")
    p = 12
    while p + 8 <= len(out):
        cid = bytes(out[p:p + 4])
        size = struct.unpack(">I", out[p + 4:p + 8])[0]
        body = p + 8
        if cid == b"STHD":
            struct.pack_into(">I", out, body, board_type)
        elif cid == b"BDNM":
            out[body:body + size] = board.encode().ljust(size, b"\0")[:size]
        elif cid == b"RSHD":
            name = bytes(out[body + 14:body + 36]).split(b"\0")[0].decode("latin-1")
            if ":" in name:
                new = (board + ":" + name.split(":", 1)[1]).encode()
                out[body + 14:body + 36] = new.ljust(22, b"\0")[:22]
        p = body + size + (size & 1)
    return bytes(out)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    with open(sys.argv[1], "rb") as f:
        data = f.read()
    with open(sys.argv[2], "wb") as f:
        f.write(attach(data))
