# Test fixtures

Every fixture here was written by another implementation. None was produced by
`heifbox`. That is the point: the tests check this package against files it did
not create and against another implementation's reading of them.

## The four files

| file | writer | content | size |
|---|---|---|---|
| `a_64.avif` | ImageMagick | 64x64 gradient, no alpha | 328 B |
| `a_128x96.avif` | ImageMagick | 128x96 gradient, no alpha | 341 B |
| `a_alpha.avif` | ImageMagick | 32x32 gradient with a 70% alpha channel | 496 B |
| `ff.avif` | ffmpeg | 64x64, no alpha | 349 B |

`a_alpha.avif` is the most interesting one. It is the only fixture with two
items, and it is what tests the property association, the auxiliary reference and
the extent arithmetic.

## How they were made

`convert` is ImageMagick 1.17.6 with AVIF support.

```sh
convert -size 64x64   gradient:red-blue                          a_64.avif
convert -size 128x96  gradient:red-blue                          a_128x96.avif
convert -size 32x32   xc:red -alpha set -channel A -evaluate set 70% a_alpha.avif

# The fourth, from the other encoder. A single frame is required: without
# -frames:v 1 ffmpeg treats the input as a video and writes a stream instead of
# a still image.
convert -size 64x64 xc:red -alpha set -channel A -evaluate set 70% src.png
ffmpeg -i src.png -frames:v 1 -c:v libaom-av1 -still-picture 1 -y ff.avif
```

Two encoders were used deliberately. On these sizes they produced the same set
of boxes, which is worth recording as a limit: the reader's handling of an
unusual box set is tested by construction rather than by a file someone else
wrote. The brands did agree, however, and so did the item structure.

## Checking them with another implementation

```sh
$ identify -format '%wx%h %[channels] depth=%z\n' a_alpha.avif
32x32 srgba depth=12

$ identify -format '%wx%h %[channels]\n' a_64.avif a_128x96.avif ff.avif
64x64 srgb
128x96 srgb
64x64 srgb
```

The tests assert these numbers. `run.sh` re-runs the comparison, generating one
fresh fixture at a size none of the four use so the comparison cannot pass by
coinciding with a value that is already in the repository.

## What the alpha fixture contains

Its box tree, measured:

```
ftyp   @0    size=28
meta   @28   size=408
  hdlr   @40    size=33
  pitm   @73    size=14
  iloc   @87    size=52
  iinf   @139   size=56
    infe   @153   size=21        item 1, type av01
    infe   @174   size=21        item 2, type av01
  iprp   @195   size=215
    ipco   @203   size=177
      colr   @211   size=19      property 1
      av1C   @230   size=12      property 2
      ispe   @242   size=20      property 3
      pixi   @262   size=14      property 4
      auxC   @276   size=56      property 5
      av1C   @332   size=12      property 6
      ispe   @344   size=20      property 7
      pixi   @364   size=16      property 8
    ipma   @380   size=30
  iref   @410   size=26
    auxl   @422   size=14
mdat   @436   size=60
```

and its associations:

```
ipma: item 1 -> colr, av1C[6](essential), ispe[7], pixi[8]
      item 2 -> av1C[2](essential), ispe[3], pixi[4], auxC[5](essential)

iref: auxl, from item 2 -> [1]     item 2, the alpha, is auxiliary to item 1
```

Two items, one with three channels and one with one, sharing an `ipco` list that
holds both sets of properties. That sharing is why `ipma` has to be parsed rather
than the properties read in order.

Wait — the association above shows item 1 taking properties 6, 7 and 8 while item
2 takes 2, 3, 4 and 5, yet `ipco` lists properties 2..5 before 6..8. The order in
`ipco` is unrelated to which item gets which property: the indices in `ipma` are
what decide, and they cross over.

## The payload

```
mdat @436 size=60, so its payload runs 444..496
  item 2 (alpha) extent 444..463, 19 bytes
  item 1 (colour) extent 463..496, 33 bytes
```

The two extents join with no gap and no overlap, and together they are exactly
the mdat payload. `TestExtentsTileThePayload` asserts this, because an off-by-any
in the `iloc` field offsets breaks the join even when both numbers look
individually reasonable.

## Reproducing the tree by hand

```sh
heifbox -file testdata/a_alpha.avif
heifbox -file testdata/a_64.avif -json
heifbox -file testdata/a_alpha.avif -payload 1 -out /tmp/payload.bin
```
