# RESEARCH — heifbox

What the HEIF family of container formats actually contains, measured against
files written by other implementations, and what could not be measured.

## Method

Nothing below is written from a specification summary. Three things were used,
and where they disagreed the bytes won:

- **ImageMagick 1.17.6**, present on this machine, which reads and writes AVIF and
  reads HEIC and HEIF. It produced the fixtures and is the independent
  implementation every reading is checked against.
- **ffmpeg**, which also writes AVIF, used to produce a second, independently
  written file for the same content so the reader could be tested against two
  encoders rather than one.
- **The `ftyp` box and the box tree itself**, walked byte by byte, because the
  container is the thing being parsed and no summary of it was trusted.

## The gap, measured

A GitHub search for a HEIF or AVIF container reader in Go returns nothing:

```
"heif box"    language:Go   -> no results
"avif parse"  language:Go   -> no results
"bmff"        language:Go   -> itsjamie/bmff, 10 stars, unmaintained
"isobmff"     language:Go   -> Eyevinn/mp4ff, 660 stars, MP4 rather than HEIF
```

What exists is a binding to a C codec, not a reader of the format.
`Kagami/go-avif` (342 stars) opens with:

```go
// #cgo CFLAGS: -Wall -O2 -DNDEBUG
// #cgo LDFLAGS: -laom
// #include <stdlib.h>
// #include "av1.h"
import "C"
```

and `jdeng/goheif` (233 stars) binds libheif the same way. Both need a C
toolchain and a codec library, and both are encoders or decoders rather than
structure readers: they hand the whole file to C. Nothing in Go walks the box
tree and reports what a HEIF file declares about itself.

That distinction is what this project is built on. The declared structure —
dimensions, bit depth, colour profile, codec configuration, item list, thumbnail,
EXIF — is metadata, and it can be read and verified without decompressing
anything.

## What the container looks like

Measured from a 496-byte AVIF written by ImageMagick, `a_alpha.avif`, which
carries both a colour image and an alpha channel:

```
ftyp   @0    size=28
meta   @28   size=408
  hdlr   @40    size=33
  pitm   @73    size=14
  iloc   @87    size=52
  iinf   @139   size=56
    infe   @153   size=21
    infe   @174   size=21
  iprp   @195   size=215
    ipco   @203   size=177
      colr   @211   size=19
      av1C   @230   size=12
      ispe   @242   size=20
      pixi   @262   size=14
      auxC   @276   size=56
      av1C   @332   size=12
      ispe   @344   size=20
      pixi   @364   size=16
    ipma   @380   size=30
  iref   @410   size=26
    auxl   @422   size=14
mdat   @436   size=60
```

Every structural claim below comes from walking this and comparing it against
`identify`.

## The box header

Four bytes of size, four bytes of type, and two special sizes:

```
size == 0   the box runs to the end of its container
size == 1   a 64-bit size follows the type, and the header is 16 bytes
```

A reader that ignores the 64-bit case reads the high half of a length as the
type's first four bytes, and everything after it is garbage.

Several boxes are **full boxes**: a version byte and three flag bytes follow the
header before their own fields. `meta` is one, and so are `iinf`, `iref`, and
`ipma`. Treating a full box as a plain one shifts every field by four and the
first nested box header lands on the version and flags, which reads as a box
named `\x00\x00\x00\x00` with an enormous size.

## `ftyp`

The major brand, then a minor version, then a list of compatible brands.

```
a_64.avif      major=avif  compatible=avif mif1 miaf
a_128x96.avif  major=avif  compatible=avif mif1 miaf
a_alpha.avif   major=avif  compatible=avif mif1 miaf
ff.avif        major=avif  compatible=avif mif1 miaf     (written by ffmpeg)
```

All four files agree, which is worth recording as a limit: the brand list did
not vary between the two encoders or across the three ImageMagick outputs, so the
reader's handling of an unusual brand list is tested by construction rather than
by a file someone else wrote.

## `iloc`

The item location box, which is where each item's bytes live. It is the most
easily mis-parsed box in the format because four of its field widths are
themselves stored in the box:

```
version(1) offset_size_and_length_size(1) base_offset_and_index_size(1)
  offset_size        = high nibble
  length_size        = low nibble
  base_offset_size   = high nibble
  index_size         = low nibble
item_count(2 or 4, by version)
for each item:
  item_id(2 or 4, by version)
  construction_method(2)     present for version 1 and 2 only
  data_reference_index(2)
  base_offset(base_offset_size bytes)
  extent_count(2)
  for each extent:
    extent_index(index_size bytes)   present for version 1 and 2 when index_size is set
    extent_offset(offset_size bytes)
    extent_length(length_size bytes)
```

Reading `offset_size` and `base_offset_size` as the same value mislocates every
extent after the first item. The fixture's `iloc` declares
`offset_size=4, length_size=4`, so a reader that assumed those are always 4 would
pass on this file and fail on one that packs them differently — which is why the
size bytes are honoured rather than assumed.

## `iinf` and `infe`

`iinf` is a full box holding an item count and then one `infe` per item. `infe`
has three layouts by version, and only version 2 and later carry the item type:

```
infe version 2/3:
  item_id(2 or 4)
  item_protection_index(2)
  item_type(4)          -- 'av01', 'grid', 'Exif', 'mime', ...
  item_name            -- NUL-terminated string
  [content_type]       -- for 'mime'
  [content_encoding]   -- for 'mime'
```

The measured fixture has two `infe` boxes of 21 bytes each, and each carries an
`item_type`. The item type is what distinguishes a coded image (`av01`) from a
derived one (`grid`) or an embedded payload (`Exif`), so a reader that only
reports an item id cannot say what an item is.

## `iprp`, `ipco` and `ipma`

This is the part that decides everything a caller wants to know.

`ipco` is an **ordered list of properties**. `ipma` maps items to **indices into
that list**, so the same property object is shared by several items and the order
within `ipco` is what the indices refer to.

From `a_alpha.avif`, the properties in order:

```
[1] colr
[2] av1C   profile=2 level=0 bitdepth=1
[3] ispe   32x32
[4] pixi   channels=1 bits=12
[5] auxC   type='urn:mpeg:mpegB:cicp:systems:auxiliary:alpha'
[6] av1C   profile=2 level=0 bitdepth=1
[7] ispe   32x32
[8] pixi   channels=3 bits=12,12,12
```

and the associations:

```
item 1 -> colr, av1C[6](essential), ispe[7], pixi[8]
item 2 -> av1C[2](essential), ispe[3], pixi[4], auxC[5](essential)
```

**This is the whole reason the property association has to be parsed rather than
the properties read in order.** `ipco` holds two `ispe` and two `pixi` boxes. The
first `pixi` in the list, index 4, has **one channel** and belongs to item 2; the
second, index 8, has **three** and belongs to item 1. A reader that took the first
`ispe` or `pixi` it found and called it the image's would report the alpha
channel's geometry as the picture's, and on this file that would still be 32x32 —
which is why the test asserts the association, not just the value.

The `identify` cross-check:

```
$ identify -format 'geom=%wx%h channels=%[channels] depth=%z' a_alpha.avif
geom=32x32 channels=srgba depth=12
```

Three channels plus alpha, at 32x32. Item 1's `pixi` says three channels and item
2's says one; `auxC` on item 2 names the alpha auxiliary type. The reading agrees
with the other implementation.

### The essential bit

Each association carries a flag. `av1C` and `auxC` are marked essential on this
file, and `colr`, `ispe` and `pixi` are not. The flag's meaning is that a reader
which does not understand the property must fail rather than continue, so
dropping it discards the information that decides whether a file can be read at
all. The index and the flag share one byte in the 7-bit form and one 16-bit value
in the large form:

```
flags bit 0 clear:  index(7 bits, high bit is essential) x count(1 byte)
flags bit 0 set:    index(15 bits, high bit is essential) x count(2 bytes)
```

The fixture uses the 7-bit form with `flags=0`. The large form is implemented
because the flag can be set by another writer, and a reader that assumed the small
form would read the count byte as an item id.

### `pixi`

```
version/flags(4) num_channels(1) bits_per_channel[num_channels](1 each)
```

The fixture's alpha item declares one channel at 12 bits, and its colour item
three at 12 bits each. Note that `num_channels` counts channels present, so a
three-channel `pixi` on an RGBA image means the alpha is a **separate item** —
which on this file it is.

## `ispe` and the cross-check that matters

```
version/flags(4) width(4) height(4)
```

The fixture's `ispe` reads 32x32 and `identify` reports `geom=32x32`. For the
other fixtures:

```
a_64.avif       ispe 64x64      identify geom=64x64
a_128x96.avif   ispe 128x96     identify geom=128x96
ff.avif         (ffmpeg)        identify geom=64x64
```

An earlier probe read `ispe` at a fixed offset taken from a previous file and
produced a width of 64 on a file whose real width was 64 but whose colour type
byte it had mistaken for a dimension. It agreed by coincidence. The lesson is the
one this project keeps relearning: a value that happens to match is not evidence
that the offset is right.

## `iref` and auxiliary items

`iref` is a full box holding one child per relationship type, and each child is a
**full box whose own type names the relationship**:

```
iref
  auxl:  from_item_id, to_item_id[]     -- item is an auxiliary of another
  thmb:  from_item_id, to_item_id[]     -- item is a thumbnail of another
  dimg:  ...                            -- derived image
  cdsc:  ...                            -- content describes
```

The measured fixture has one `auxl` box of 14 bytes. The alpha item names the
colour item as the image it is auxiliary to, which is how a reader knows to
combine them into one picture rather than report two.

## `colr`, `av1C`, `auxC`

```
colr: colour_type(4) then, for 'nclx', a CICP triple and a full-range flag,
      or for 'rICC'/'prof', an ICC profile
av1C: 4 bytes -- marker and version, then seq_profile, seq_level_idx,
      and the high_bitdepth, twelve_bit and monochrome flags
auxC: aux_type (NUL-terminated string), then optional subtype bytes
```

The fixture's `auxC` names
`urn:mpeg:mpegB:cicp:systems:auxiliary:alpha`, which is the standard alpha
auxiliary type and what makes the second item's purpose unambiguous.

An early probe printed `av1C` as `seq_profile=0 seq_level=0 high_bitdepth=0`
because it read four bytes starting eight bytes before the box's own body. The
real values are `profile=2 bitdepth=1`. Both numbers are plausible and only the
cross-check against the offset settles which is right.

## Grid items

A `grid` item composes several coded images into one picture and is common for
AVIF, because it is how an encoder splits a large image. Its payload is:

```
version(1) flags(1) rows_minus_one(1) columns_minus_one(1)
output_width(2)  output_height(2)     -- 0 means derive from the tiles
```

No fixture on this machine contains a grid item: ImageMagick and ffmpeg both wrote
single-tile files for the sizes used. **The grid layout above therefore has no
file written by another implementation behind it, and the project's own rule says
a box in that position does not go in the supported list.** It is reported as
present, with its raw bytes, and not decoded — recorded here so the decision is
visible rather than quietly taken.

## What could not be measured

**HEIC and HEIF.** ImageMagick reports HEIC and HEIF as read-only —
`convert gt.png gt.heic` fails with `no encode delegate for this image format
'HEIC'`. There is no HEIC writer on this machine, no HEIC file in the system
font or share directories, and no `MP4Box`, `heif-enc` or `heif-convert`. A HEIC
file's structure uses the same boxes as AVIF, so the reader will walk one, but
**no HEIC fixture exists to test it against**, and the README says so rather than
listing HEIC as supported.

**The `hvcC` box** (HEVC codec configuration, what a HEIC file carries where AVIF
carries `av1C`) has the same problem: no HEIC file means no `hvcC` to read.
Its layout is implemented from the specification with no fixture, which by this
project's rule makes it unverified — and that is how it is reported.

**Decoding.** No AV1 or HEVC decompression. The payload location and the declared
geometry are read; the pixels are not produced.

## Prior art

| project | stars | language | what it is |
|---|---|---|---|
| `Kagami/go-avif` | 342 | Go + cgo | libaom binding, needs a C toolchain |
| `jdeng/goheif` | 233 | Go + cgo | libheif binding, needs a C toolchain |
| `Eyevinn/mp4ff` | 660 | Go | MP4 boxes; not HEIF's item structure |
| `itsjamie/bmff` | 10 | Go | generic BMFF walking, unmaintained |
| `structure` (Rust) | — | Rust | a full HEIF reader in another language |
| ImageMagick | — | C | reads and writes AVIF; the reference used here |

For a dependency-free reader of the HEIF item structure in Go, there is nothing.

## Sources

- ImageMagick 1.17.6 on this machine: `identify`, `convert -list format`.
- ffmpeg, for the second AVIF writer.
- The ISO/IEC 14496-12 box structure and the AVIF item-property definitions,
  checked against the bytes of every fixture rather than taken on trust.
- The fixtures themselves: `a_64.avif`, `a_128x96.avif`, `a_alpha.avif` from
  ImageMagick, and a fourth from ffmpeg.
