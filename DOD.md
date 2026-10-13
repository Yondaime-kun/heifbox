# Definition of Done — heifbox

The six required items, each with the evidence that it is met rather than
asserted.

## 1. PLAN.md, with failure conditions written before the code

`PLAN.md` was written first and names five failure conditions up front. Each is a
way the project could have failed while looking finished, and each is resolved:

| # | failure condition | outcome |
|---|---|---|
| 1 | A field implemented from a specification description and never checked against a file another implementation wrote | Partly triggered, and handled: `hvcC` is implemented because a HEIC file needs it, but no HEIC fixture exists on this machine, so it is reported as unverified rather than listed as supported. Every other box has a fixture. |
| 2 | This reader's dimensions or bit depth disagree with another implementation | Resolved: `run.sh` compares every fixture and a freshly generated one against ImageMagick, and the tests assert ImageMagick's numbers. |
| 3 | A malformed file causes a panic, an unbounded loop, or an allocation driven by a number taken from the file | Resolved: every size and count is bounded before use; `TestTruncatedFileIsRejected`, `TestBoxSizeLargerThanFileIsRejected` and `TestDepthLimit` exercise it. |
| 4 | The reader reports success on a file that is not a HEIF file | Resolved: a missing `ftyp`, a truncated file and a missing `meta` are all errors; `TestNonHEIFIsRejected` and `TestItemsWithNoMeta` cover it. |
| 5 | A box claimed as supported has no fixture exercising it | Resolved by removal: the `grid` item is reported but not decoded, and `hvcC` is reported as unverified, both because no fixture exists. |

## 2. RESEARCH.md, with measurements rather than impressions

`RESEARCH.md` records the gap as a measurement (a GitHub search returning nothing
for a HEIF container reader in Go, and the two cgo bindings that exist), then the
byte layout of every box it covers, then the wrong turns. Four of those produced a
reading that looked successful and was not:

- reading `infe` version 2's minimum body as 12 bytes when it is 9, which rejects
  every real file;
- applying the 16-bit index mask to an 8-bit `ipma` association, turning index 3
  into 131;
- treating `colr` as a full box, which reads the CICP values as the colour type;
- reading `iloc`'s field widths at offset four, which takes them from the item id.

Plus the two `iref` mistakes, which the bytes alone could not settle and which
were confirmed against **ISO/IEC 14496-12:2015 section 8.11.12.2** afterwards. The
document says which of the two settled it and how.

## 3. Working code

```
$ go build ./...
build ok

$ ls internal/heifbox/*.go cmd/heifbox/*.go | grep -v _test | xargs wc -l | tail -1
 1766 total
```

By file:

```
internal/heifbox/box.go        the container, the box tree, the bounds-checked reader
internal/heifbox/item.go       iinf, infe, iloc, iprp, ipco, ipma, iref
internal/heifbox/property.go   the property types, image assembly, payload access
cmd/heifbox/main.go            the command-line tool
```

Dependencies outside the standard library: **none**.

It reads real files written by two other implementations:

```
$ go run ./cmd/heifbox testdata/a_alpha.avif
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
```

## 4. Tests actually executed

```
$ go test ./... >/dev/null 2>&1; echo $?
0

$ go test -race ./... >/dev/null 2>&1; echo $?
0

$ go vet ./... >/dev/null 2>&1; echo $?
0

$ gofmt -l .
(no output)
```

**16 tests at the top level, 24 including subtests.** Verified by exit code, not
by reading the output for the word "ok".

### The cross-check against another implementation

The dimension assertions use ImageMagick's numbers, and `run.sh` re-runs the
comparison including one freshly generated fixture at a size no committed fixture
uses:

```
$ ./run.sh
heifbox reports : 200x150
ImageMagick says: 200x150
the two agree

checking every committed fixture against ImageMagick
  testdata/a_128x96.avif   heifbox=128x96     identify=128x96     agree
  testdata/a_64.avif       heifbox=64x64      identify=64x64      agree
  testdata/a_alpha.avif    heifbox=32x32      identify=32x32      agree
  testdata/ff.avif         heifbox=64x64      identify=64x64      agree
```

A fresh size is used deliberately: a comparison against a value that already
appears in the repository could pass by coincidence.

### The extent join

The strongest structural check available without decoding: the two items'
extents must fill the `mdat` payload exactly.

```
mdat @436 size=60, so its payload runs 444..496
  item 2 (alpha) extent 444..463, 19 bytes
  item 1 (colour) extent 463..496, 33 bytes
                                        19 + 33 = 52 = the mdat payload size
```

No gap, no overlap. Any error in the `iloc` size fields breaks this even when
each individual number still looks reasonable, which is why it is a test rather
than an observation.

### Mutation testing

Each bug this package had was reintroduced and the suite re-run. Five of five were
caught.

| # | mutation | result | test that caught it |
|---|---|---|---|
| 1 | `infe` v2 minimum back to 12 bytes | **caught** | `TestItemsAndProperties` |
| 2 | 8-bit `ipma` association masked with `0x7FFF` | **caught** | `TestItemsAndProperties` |
| 3 | `iloc` field widths read at offset four | **caught** | `TestIlocSizeFieldsAreRead`, `TestExtentsTileThePayload` |
| 4 | `colr` listed among the full boxes | **caught** | `TestColourIsNotAFullBox` |
| 5 | `iref` reference count ignored | **caught** | `TestItemReference` |

All five were real bugs found by running the reader against files written by
other implementations, not hypothetical ones planted for the exercise. That is
the useful kind of mutation: each was a mistake this code actually made and the
test now prevents.

## 5. Honest README

`README.md` states what works and how each claim was checked, then what does not
work with the reason for each:

- no AV1 or HEVC decoding;
- no writing;
- `hvcC` decoded with no fixture, therefore unverified and said to be;
- `grid` items reported but not decoded, because no fixture contains one;
- construction methods 1 and 2 in `iloc` refused rather than guessed at;
- the `name` table not read.

Three detail sections name the specific traps that cost a wrong parse, because a
reader of this code will meet the same three.

Nothing in the README claims a capability that is not demonstrated by a command
in it or a test in the suite.

## 6. Commit

One commit, on the default branch, with the repository pushed.

## The bar, compared to the previous project

`colorfont` verified against fontTools and against one real font from Google.
`heifbox` verifies against two independent encoders (ImageMagick and ffmpeg) and
against a second implementation's readings of four files, plus a structural
invariant — the extent join — that no field-by-field comparison would catch. Two
of the bugs it fixed could not be settled by the bytes alone and were confirmed
against the published standard, which is recorded with the section number.
