#!/usr/bin/env python3
"""Verify MFM encode/decode roundtrip for sector 0 of WB1.3 ADF."""
import struct

with open('Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf', 'rb') as f:
    adf = f.read()

sec0 = adf[0:512]

def mfm_odd(val):
    return (val >> 1) & 0x55555555

def mfm_even(val):
    return val & 0x55555555

def decode_pair(odd, even):
    return ((odd & 0x55555555) << 1) | (even & 0x55555555)

# Build block-split data (how our Go code does it)
data_longs = []
for i in range(128):
    off = i * 4
    v = struct.unpack('>I', sec0[off:off+4])[0]
    data_longs.append(v)

# Block-split: all odd halves first, then all even halves
data_mfm_odd = [mfm_odd(v) for v in data_longs]
data_mfm_even = [mfm_even(v) for v in data_longs]
data_block = data_mfm_odd + data_mfm_even

# Decode: pair data_block[i] with data_block[i+128]
print("=== Block-split decode (how ROM reads data block) ===")
for i in range(4):
    decoded = decode_pair(data_block[i], data_block[i+128])
    expected = data_longs[i]
    print(f'  Data[{i}]: decoded=${decoded:08X} expected=${expected:08X} match={decoded == expected}')

# The ROM decode function at $FEAC62 reads header with BSR $FEAE7C (long reader)
# Let's check: does the ROM data block decoder use block-split or sequential pairs?
# From the ROM code, after verifying header checksum, it reads data at:
#   $FEAD42: BSR $FEAE7C (read long) x 128 for odd half
#            BSR $FEAE7C (read long) x 128 for even half  
#   OR does it interleave?

# Let's also verify the header interleaved format
info = 0xFF << 24 | 0 << 16 | 0 << 8 | 11
header_longs = [info, 0, 0, 0, 0]

# Interleaved header
header_il = []
for v in header_longs:
    header_il.append(mfm_odd(v))
    header_il.append(mfm_even(v))

# XOR checksum of header (10 longs, masked)
hdr_chk = 0
for v in header_il:
    hdr_chk ^= (v & 0x55555555)
print(f"\n=== Header ===")
print(f"  Info word: ${info:08X}")
print(f"  Header MFM (10 longs): {' '.join(f'${v:08X}' for v in header_il)}")
print(f"  Header checksum: ${hdr_chk:08X}")
print(f"  Checksum odd: ${mfm_odd(hdr_chk):08X}, even: ${mfm_even(hdr_chk):08X}")

# Data checksum
data_chk = 0
for v in data_block:
    data_chk ^= (v & 0x55555555)
print(f"\n=== Data block ===")
print(f"  Data checksum: ${data_chk:08X}")

# Print expected bytes
print(f"\n=== Expected first 16 bytes of sector 0 ===")
print(f"  {sec0[:16].hex()}")
print(f"  = {'DOS\\x00' if sec0[:4] == b'DOS\\x00' else sec0[:4]}")
