# heifbox

Reads the HEIF family of container formats in Go, with no dependencies outside
the standard library: HEIC, AVIF, and anything else built on the ISO base media
file format's box structure.

It reads what a file declares about itself — the box tree, the item list, the
item locations, the properties each item carries, and the relationships between
items — and returns embedded payloads such as Exif unmodified. It does not
decompress image data.

## Why

There is no HEIF or AVIF container reader in Go. What exists is a binding to a C
codec:

```go
// Kagami/go-avif, 342 stars
// #cgo LDFLAGS: -laom
// #include "av1.h"
import "C"

// jdeng/goheif, 233 stars — binds libheif the same way
```

Both need a C toolchain and a codec library present, and both hand the file to C
rather than reading the format. A search for `heif box`, `avif parse` or a
maintained BMFF walker in Go returns nothing usable.

The metadata a caller usually wants is in the container, not in the compressed
data: the picture's dimensions, its bit depth, its colour profile, the codec
configuration, which items exist and what each is for. That is what this package
reads, and it can be checked against another implementation without decoding
anything.

## What works

Every reading below is checked against ImageMagick 1.17.6, which reads and writes
these files and is the independent implementation this package is measured
against. A second file for the same content was written by ffmpeg so two encoders
could be tested rather than one.

```
fixture          reported    identify says    agree
a_64.avif        64x64       64x64            yes
a_128x96.avif    128x96      128x96           yes
a_alpha.avif     32x32       32x32 srgba      yes
ff.avif (ffmpeg) 64x64       64x64            yes
```

```
$ heifbox -file testdata/a_alpha.avif
file       : testdata/a_alpha.avif
major brand: avif
compatible : avif mif1 miaf
family     : recognised HEIF brand
tree       :
  ftyp size=28
  meta size=408
    hdlr size=33
    pitm size=14
    iloc size=52
    iinf size=56
      infe size=21
      infe size=21
    iprp size=215
      ipco size=177
        colr size=19
        av1C size=12
        ispe size=20
        pixi size=14
        auxC size=56
        av1C size=12
        ispe size=20
        pixi size=16
      ipma size=30
    iref size=26
      auxl size=14
  mdat size=60
items      : 2
  item 1  type=av01
      bytes 463..496 (33 bytes)
      [1] colr  nclx primaries=1 transfer=13 matrix=6 range=full
      [6] av1C  profile=2 level=0 highBitdepth=true monochrome=false  [essential]
      [7] ispe  32x32
      [8] pixi  channels=3 bits=[12 12 12]
  item 2  type=av01
      bytes 444..463 (19 bytes)
      [2] av1C  profile=2 level=0 highBitdepth=true monochrome=true  [essential]
      [3] ispe  32x32
      [4] pixi  channels=1 bits=[12]
      [5] auxC  urn:mpeg:mpegB:cicp:systems:auxiliary:alpha  [essential]
      ref auxl -> [1]
primary    : item 1  32x32  payload=33 bytes
             colour   nclx primaries=1 transfer=13 matrix=6 range=full
             channels 3 at [12 12 12] bits
             av1      profile=2 level=0 highBitdepth=true
```

Two items, one colour and one alpha, with the second declaring itself the
auxiliary alpha of the first. The two extents fill the `mdat` payload end to end
with no gap and no overlap — 444..463 and 463..496 inside a payload of
444..496 — which is the check that the item locations are being read correctly
rather than merely plausibly.

## What it reads

- **The box tree**, to a bounded depth, with every size validated against the
  bytes present.
- **`ftyp`**: the major brand and the compatible brands.
- **`meta`** and its children: `hdlr`, `pitm`, `iloc`, `iinf`, `iprp` (`ipco` and
  `ipma`), `iref`.
- **Items**: their id, type, name, protection index, byte extents, the properties
  each one carries, and the references between them.
- **Properties**: `ispe`, `pixi`, `colr` (both `nclx` and the ICC forms), `av1C`,
  `hvcC`, `auxC`, `irot`, `imir`.
- **Payloads**, as stored: the coded image data or an embedded Exif payload, with
  the Exif header offset validated before it is applied.

## Usage

```go
data, err := os.ReadFile("picture.avif")
if err != nil {
    return err
}
f, err := heifbox.Parse(data)
if err != nil {
    return err
}

img, err := f.PrimaryImage()
if err != nil {
    return err
}
fmt.Println(img.Width, img.Height)      // from ispe
fmt.Println(len(img.Payload))           // the coded bytes, not decoded

// The alpha channel is a separate item that says so.
items, _ := f.Items()
for _, it := range items {
    if aux := it.FindProperty("auxC"); aux != nil {
        v, _ := aux.Decode()
        fmt.Println("auxiliary image for:", v.AuxiliaryType.Type)
    }
}
```

The command-line tool:

```
heifbox -file Font.avif              summary, tree and items
heifbox -file F.avif -tree           the box tree alone
heifbox -file F.avif -items          items and properties alone
heifbox -file F.avif -json           the same as JSON
heifbox -file F.avif -payload 1 -out image.bin    extract an item's coded bytes
```

## Three things about this format that cost a wrong parse

Each of these produced a reading that looked successful and was not. They are
written down because a reader of this code will meet the same three.

**`iref`'s children are not full boxes, and each carries a count.** `iref` itself
has a version and flags; its children do not. And the layout is
`from_item_ID, reference_count, to_item_ID[reference_count]`. The fixture's
payload is `00 02 00 01 00 01` — from 2, count 1, to [1] — which reads as a second
bogus reference to the same item if the count field is skipped, and as
`from 1 -> [1, 1]` if the child is treated as a full box. Confirmed against
ISO/IEC 14496-12:2015 section 8.11.12.2 after the bytes left it ambiguous.

**`colr` is not a full box. `iloc` is.** `colr`'s first four payload bytes are its
colour type, `nclx` on every fixture here; skipping four bytes as if it had a
version reads the CICP values as the type and reports `\x00\x01\x00\r`. `iloc` is
the other way round: it has a version and flags, and its four size fields come
immediately after them. Reading those at offset four takes them from the item id
instead and every extent lands in the wrong place.

**`ipco` is a shared, ordered list and `ipma` holds indices into it.** The
fixture has two `ispe` and two `pixi` boxes. The first `pixi` has one channel and
belongs to the alpha item; the second has three and belongs to the colour item.
Taking the first of each type reports the alpha channel's properties as the
picture's. The `essential` flag lives on the association rather than on the
property, because two items share one property box.

## What is not supported

Each of these is a decision with a reason, not a gap that went unexamined.

- **No AV1 and no HEVC decoding.** The payload is located and its declared
  geometry reported; the pixels are not produced. A decoder is a codec, not a
  container, and claiming one in passing would be dishonest.
- **No writing.** A writer needs every box the format can contain emitted
  correctly, and the reader is the half with a verifiable answer.
- **`hvcC` is decoded from the specification with no fixture.** No HEIC file and
  no HEIC encoder exists on the machine this was developed on, so there is no
  file written by another implementation to check the reading against. The code
  is there because a HEIC file needs it; `RESEARCH.md` records that it is
  unverified. HEIC's other boxes are the same ones AVIF uses, which are tested.
- **Grid items are reported, not decoded.** A `grid` item composes several coded
  images into one picture and is common for AVIF. No fixture here contains one —
  both encoders wrote single-tile files — so per this project's own rule it does
  not go in the supported list. Its presence and raw bytes are reported.
- **Construction methods 1 and 2 in `iloc` return `ErrUnsupported`.** Method 1
  addresses the `idat` box and method 2 another item's data. Every file measured
  here uses method 0, which addresses the file directly; returning file offsets
  for the other two would hand back the wrong bytes silently.
- **The `name` table is not read**, so CPAL-style label resolution is absent.
  Item names come from `infe` and are returned as stored.

## Testing

```
go test ./...
```

**16 tests at the top level, 24 including subtests; exit code 0, race-clean,
`go vet` clean, `gofmt` clean.**

Several tests exist to catch specific mistakes this package made and fixed, and
each is named for the bug it plants:

| test | the bug it catches |
|---|---|
| `TestItemReference` | treating an `iref` child as a full box, and skipping `reference_count` |
| `TestColourIsNotAFullBox` | listing `colr` among the full boxes |
| `TestIlocSizeFieldsAreRead` | reading `iloc`'s field widths at the wrong offset |
| `TestExtentsTileThePayload` | any extent-base mistake, via the gap-free join |
| `TestEssentialFlag` | dropping the flag, or sharing it between items |
| `TestItemsAndProperties` | taking the first `pixi`-of-a-type instead of following `ipma` |
| `TestPrimaryImageDimensions` | any misread of `ispe`, against ImageMagick's numbers |
| `Test64BitBoxSize` | reading a 64-bit box size as 32 bits |
| `TestDepthLimit` | unbounded recursion through nested boxes |
| `TestTruncatedFileIsRejected` | slicing past the end instead of refusing |
| `TestItemDataRejectsOtherConstructionMethods` | returning wrong bytes instead of refusing |

The suite was mutated to confirm it bites: five of the bugs above were
reintroduced one at a time and the corresponding test failed every time.
`DOD.md` records the evidence.

## Regenerating the fixtures

`testdata/GENERATED.md` has the exact commands. The fixtures are 300–500 bytes
each and were produced by ImageMagick and ffmpeg.

## Licence

MIT. See `LICENSE`.
