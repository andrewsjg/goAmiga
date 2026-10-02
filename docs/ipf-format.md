# The IPF disk image format, and how goAmiga decodes it

IPF (Interchangeable Preservation Format) is the disk image format of the
[Software Preservation Society](http://www.softpres.org/) (SPS). Unlike an
ADF, which keeps only the 512-byte sectors AmigaDOS can read, an IPF
describes what is physically on each track: the stream of magnetic flux
cells. Copy-protected originals therefore survive intact, including:
- non-standard sync words and sector formats;
- tracks longer than normal;
- "weak" bits that read differently each time.

This document describes the format and then how `amiga/ipf.go` turns it
back into a track the emulated disk controller can read. The record and
field layouts come from the SPS IPF documentation and from Keir Fraser's
public-domain IPF reader and writer in
[Disk-Utilities](https://github.com/keirf/Disk-Utilities) (`ipfinfo/ipf.txt`,
`ipfinfo/ipfinfo.c`, `libdisk/container/ipf.c`). They were checked against
a real SPS image: Supremacy (Virgin, 1990), disk 1.

## Background: MFM cells

An Amiga floppy track is about 100,000 *cells*. A cell is a 2 µs window
that does or doesn't hold a flux transition (1 or 0). Data is stored with
**MFM** (modified frequency modulation): every data bit becomes two cells,
a clock cell followed by the data cell. The clock cell is 1 only when the
data bits on both sides of it are 0:

| previous data bit | data bit | cells written |
|---|---|---|
| any | 1 | `0 1` |
| 1 | 0 | `0 0` |
| 0 | 0 | `1 0` |

That rule never produces two adjacent 1 cells, and never more than three 0
cells in a row. A **sync word** deliberately breaks the clock rule, so it
can't be mistaken for data. The AmigaDOS sync `$4489` is the data byte
`$A1` with one clock cell missing. The disk controller's DSKSYNC register
looks for it to know where data starts.

A track has no fixed origin except the **index pulse**, which comes once
per revolution from a hole in the disk hub.

## File layout

An IPF is a sequence of **records**. Every record starts with the same
12-byte header, and all numbers are big-endian 32-bit unsigned:

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | record type: four ASCII characters |
| 4 | 4 | length of the record in bytes, *including* this header |
| 8 | 4 | CRC-32 of the record |

The CRC is the standard CRC-32 (IEEE 802.3, as used by zip and PNG). It is
computed over the whole record, header included, with the CRC field set
to zero.

The records come in this order:

1. one `CAPS` record (length 12, no body), which identifies the file;
2. one `INFO` record describing the image;
3. one `IMGE` record per track;
4. one `DATA` record per track, each followed by that track's *extra
   data area*.

Tools may write all the IMGEs first and then all the DATAs. A DATA is
matched to its IMGE by a shared chunk ID, not by position.

### INFO (record length 96)

The body is 21 longwords:

| Body offset | Field | Notes |
|---|---|---|
| 0 | media type | 1 = floppy disk |
| 4 | encoder type | **1 = CAPS** (byte-oriented), **2 = SPS** (bit-oriented); see below |
| 8 | encoder revision | 1 |
| 12 | file key | the SPS release number |
| 16 | file revision | |
| 20 | origin | CRC of the original source data |
| 24, 28 | min, max cylinder | e.g. 0 and 83 |
| 32, 36 | min, max head | 0 and 1 |
| 40 | creation date | year×10000 + month×100 + day |
| 44 | creation time | h×10⁷ + min×10⁵ + s×10³ + ms |
| 48-60 | platforms | four longwords; the first is 1 for the Amiga |
| 64 | disk number | |
| 68 | creator ID | |
| 72-80 | reserved | |

Supremacy disk 1 example: encoder 1 (CAPS), cylinders 0-83, heads 0-1,
platform 1, created 2004-01-23.

### IMGE: one per track (record length 80)

The body is 17 longwords:

| Body offset | Field | Meaning |
|---|---|---|
| 0 | cylinder | |
| 4 | head | 0 or 1 |
| 8 | density type | 1 = noise (an unformatted track), 2 = auto (uniform cell timing); higher values name copy-protection schemes whose cells vary in length: 3 = Copylock, 6 = Speedlock, and others |
| 12 | signal type | 1 = 2 µs cells |
| 16 | track bytes | ⌈track bits ÷ 8⌉ |
| 20 | start byte | ⌊start bit ÷ 8⌋ |
| 24 | **start bit** | cell offset, from the index pulse, of the first block's first cell |
| 28 | data bits | cells in all the blocks' data streams |
| 32 | gap bits | cells in all the gaps |
| 36 | **track bits** | data bits + gap bits: the length of the track in cells |
| 40 | **block count** | blocks in the track, e.g. 11 for an AmigaDOS track |
| 44 | encoder process | |
| 48 | flags | bit 0: the track has weak ("flakey") bits |
| 52 | **chunk ID** | matches the DATA record for this track |
| 56-64 | reserved | |

Supremacy disk 1, cylinder 0, head 0: density 2, start bit 2459,
95,744 data cells + 4,512 gap cells = 100,256 cells, 11 blocks.

### DATA: one per track (record length 28), plus its extra area

The body is four longwords:

| Body offset | Field |
|---|---|
| 0 | **size** of the extra data area in bytes |
| 4 | size in bits (size × 8) |
| 8 | CRC-32 of the extra data area |
| 12 | chunk ID: the IMGE this describes |

The **extra data area**, `size` bytes, follows immediately after the record
and is *not* counted in the record's length. Its first part holds the
**block descriptors**, one per block (block count × 32 bytes), back to
back. The streams they point into follow, at offsets measured from the
start of the area.

### Block descriptors (32 bytes each)

| Offset | Field | CAPS encoder | SPS encoder |
|---|---|---|---|
| 0 | **block bits** | cells in the block's data | same |
| 4 | **gap bits** | cells in the gap after it | same |
| 8 | — | block size in bytes (⌈block bits ÷ 8⌉) | **gap offset**: where the gap streams start (0 = none) |
| 12 | — | gap size in bytes | cell type (1 = 2 µs) |
| 16 | encoder type | 1 = MFM | same |
| 20 | **flags** | ignored (treat as 0) | bit 0 forward gap stream, bit 1 backward gap stream, bit 2 stream lengths in bits |
| 24 | **gap value** | the byte that fills the gap | used when there are no gap streams |
| 28 | **data offset** | where the data stream starts | same |

A track is its blocks in order. Each block is a data stream of `block
bits` cells followed by a gap of `gap bits` cells. Either can be empty. An
AmigaDOS track has one block per sector, and the long gap before the index
is attached to the last block. The total of all blocks is the track's
`track bits`.

## Streams

### Data streams

A data stream is a sequence of **elements** ended by a zero byte. Each
element starts with a head byte:

```
bit  7 6 5 | 4 3 2 1 0
     width |   type
```

- **width:** how many bytes follow holding the element's count, big-endian
  (0-7).
- **type:**

  | type | name | sample | becomes |
  |---|---|---|---|
  | 0 | end | — | ends the stream |
  | 1 | sync | yes | the sample's bits **are cells**, copied as they are |
  | 2 | data | yes | the sample's bits are data, **MFM-encoded** (2 cells per bit) |
  | 3 | gap | yes | gap data, MFM-encoded like data |
  | 4 | raw | yes | cells, copied as they are |
  | 5 | flakey | **no** | weak bits: 2 cells per bit, random each read |

The **count** is a number of bytes for the CAPS encoder. For the SPS
encoder it's a number of bits when the block's flags have bit 2 set. In
that case the sample still occupies whole bytes (⌈bits ÷ 8⌉), so the next
element starts on a byte boundary.

For data, gap and flakey elements the count is of *data* bits, each worth
two cells; for sync and raw elements it is of cells.

The AmigaDOS sync pair `$4489 $4489` written as cells is `21 04 44 89 44
89`: a sync element (type 1) with a 1-byte count of 4 bytes.

### Gaps

A gap of `gap bits` cells follows each block's data. The CAPS encoder
always fills it with the **gap value** byte, MFM-encoded, repeated as
often as needed (with a partial repeat at the end).

The SPS encoder can instead give **gap streams**, starting at the gap
offset. A forward stream comes first if flags bit 0 is set, then a backward
one if bit 1 is set, each ended by a zero byte. Gap streams use the same
element format with only two types:
- 1 = stream length: how many data bits the next sample fills;
- 2 = data: the sample to repeat.

Their counts are always in bits. A data element with no length before it
is the stream's **loop sample**, repeated to fill whatever is left; one of
length 0 forbids looping.

- **Forward stream:** fills the gap from the end of the block's data
  onwards. It is truncated if too long, and its loop sample repeats if it's
  too short. With no explicit loop sample, its last sample loops.
- **Backward stream:** fills the gap from the start of the next block,
  backwards. It's encoded forwards, so the decoder lays it out
  right-aligned. Its automatic loop sample is its *first* sample.
- **Both:** each keeps its fixed part at its own end of the gap, and the
  loop samples meet in the middle. If the fixed parts are too long
  together, they're trimmed evenly.

All gap samples are data, MFM-encoded.

## Rebuilding a track

1. Read records, checking each CRC, and each DATA area's CRC. Keep each
   IMGE by chunk ID.
2. For each DATA, take its IMGE. If the block count or track bits is 0,
   the track is unformatted: no data, no sync words.
3. Allocate `track bits` cells. Set a write cursor to `start bit`.
4. For each block, in order:
   - Remember where the block starts. Decode the data stream at
     `area[data offset]`, writing cells at the cursor (wrapping round the
     end of the track). When MFM-encoding, the clock cell depends on the
     previous *cell written*, which carries over across element and block
     boundaries.
   - Set the cursor to block start + `block bits`; the descriptor is
     authoritative.
   - Write the gap, as above.
   - Set the cursor to block start + `block bits` + `gap bits`.
5. Note every flakey run's cell range.

Because the cursor wraps, the gap after the last block runs on past the end
of the buffer into the cells before `start bit`. Index position 0 ends up
somewhere inside a gap, as on a real disk.

Supremacy, disk 1, cylinder 1, head 0 shows what protection looks like
here. The track is one block of 65,744 cells:
- a 1-byte gap element ($00, 16 cells);
- the sync pair (32 cells);
- one data element of **4,105 bytes** (65,680 cells), one long sector.

A 34,512-cell gap of $00 fills the rest. The track carries a single
`$4489 $4489`, where an AmigaDOS track has eleven. No ADF can represent it.

## How goAmiga uses it

**Parsing** (`ParseIPF`, `decodeIPFTrack`). Each track becomes a
`bitTrack`: the cells packed eight to a byte, the number of cells, and the
weak ranges. Decoded tracks take about 12 KB each.

**Reading.** Every track is read at cell level (`DiskDrive.readTrack`). An
ADF track is first generated as 6,250 MFM words; an IPF track supplies its
cells directly.
- The head's position, in cells, follows emulated time: a cell passes every
  227⁄16 CPU cycles (2 µs). It's reduced modulo the track's own length.
- With WORDSYNC set, the controller shifts cells through a 16-bit window
  until it matches DSKSYNC, at any bit alignment. Data starts with the next
  cell, and DSKSYN fires after that many cells' time. A track with no sync
  word (unformatted, or the wrong sync) never starts, as on hardware.
  Without WORDSYNC, reading starts at the head.
- Words go to chip RAM 16 cells at a time from there. With WORDSYNC, each
  sync word that passes during the transfer re-aligns the word boundary,
  as Paula's does, so sectors recorded at different bit offsets each
  arrive aligned. DSKBLK fires after the transfer's cell time.
- Weak cells get fresh random values before every read (`reroll`), so a
  protection check that reads a track twice and expects a difference sees
  one.

**Disks.** `ReadDiskImage` accepts `.ipf`, or a zip containing one (an ADF
in the zip is preferred, then an IPF; with several disks, the one whose
name says boot or disk 1). IPF disks are always
write-protected, and save states store the IPF file and parse it again
on load.

**What isn't modelled:**
- **Variable cell timing** (density types 3 and up). Some protections
  write a track with slightly longer or shorter cells and time how long it
  takes to read. goAmiga reads every cell at 2 µs, so protections that
  only check the data (most) pass, and timing checks may not.
- **Revolution length.** The index pulse comes every 6,250 word times
  whatever the track's length, so a protection that measures a long track
  against the index won't see the difference.
- **Writing** to an IPF disk.

## Testing

- **Real image:** `TestParseIPFSupremacy` (`GOAMIGA_IPF=...`) parses an
  image and decodes AmigaDOS sectors at any bit alignment. On Supremacy
  disk 1, track 0 yields all eleven sectors with good checksums, and a
  `DOS` bootblock.
- **Synthetic images:** the unit tests build IPF files in code
  (`synthIPF`):
  - a CAPS track (gap, sync and data elements, the start bit, MFM clocking
    and the gap value);
  - an SPS track (bit-length elements at odd alignments, and forward plus
    backward gap streams);
  - weak cells;
  - a corrupted stream failing its CRC;
  - read DMA finding a sync word at an odd bit offset.
- **The game:** `TestGameDiag` with `GOAMIGA_GAME` set to the Supremacy zip
  boots it from the bootblock, through the game's own loader, to the title
  picture and the opponent-selection screen.

Getting that far also exposed a **blitter** bug. The loader decodes each
track's MFM with the blitter, using a B shift of 15 over a blit 4 words
wide and 513 lines tall. The real blitter's shift history carries from the
last word of one line into the first word of the next, but goAmiga's
cleared it at every line. So the first word of every line came out wrong,
the loader's checksum failed, and it retried forever. The history now
carries across lines, A's is the *masked* word (what ALWM = 0 relies on),
and the fill carry starts each line at FCI (`TestBlitShiftCarriesAcrossLines`).
