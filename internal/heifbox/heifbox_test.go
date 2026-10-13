package heifbox

import (
	"os"
	"path/filepath"
	"testing"
)

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func parse(t *testing.T, name string) *File {
	t.Helper()
	f, err := Parse(load(t, name))
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return f
}

// TestBrands checks the brand list of every fixture.
//
// All four fixtures were written by two different encoders and agree, which is
// recorded here as the expected values: identity against an independent writer
// is not available for the brand list because no fixture on this machine
// disagrees.
func TestBrands(t *testing.T) {
	for _, name := range []string{"a_64.avif", "a_128x96.avif", "a_alpha.avif", "ff.avif"} {
		t.Run(name, func(t *testing.T) {
			f := parse(t, name)
			if f.MajorBrand != "avif" {
				t.Errorf("MajorBrand = %q, want avif", f.MajorBrand)
			}
			if !f.IsHEIF() {
				t.Error("IsHEIF() = false for an avif file")
			}
			want := []string{"avif", "mif1", "miaf"}
			if len(f.CompatibleBrands) != len(want) {
				t.Fatalf("CompatibleBrands = %v, want %v", f.CompatibleBrands, want)
			}
			for i := range want {
				if f.CompatibleBrands[i] != want[i] {
					t.Errorf("CompatibleBrands[%d] = %q, want %q", i, f.CompatibleBrands[i], want[i])
				}
			}
		})
	}
}

// TestPrimaryImageDimensions checks the dimensions the reader reports for each
// fixture against what ImageMagick reports for the same file.
//
// The expected values below come from `identify -format '%wx%h'` on each file,
// not from the input sizes given to the encoder:
//
//	a_64.avif      64x64
//	a_128x96.avif  128x96
//	a_alpha.avif   32x32
//	ff.avif        64x64
//
// This is the cross-check that matters most: ispe is the field a caller trusts
// for the picture size, and a parser that reads it from the wrong offset would
// report a plausible but wrong number.
func TestPrimaryImageDimensions(t *testing.T) {
	cases := []struct {
		file string
		w, h uint32
	}{
		{"a_64.avif", 64, 64},
		{"a_128x96.avif", 128, 96},
		{"a_alpha.avif", 32, 32},
		{"ff.avif", 64, 64},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			f := parse(t, tc.file)
			img, err := f.PrimaryImage()
			if err != nil {
				t.Fatalf("PrimaryImage: %v", err)
			}
			if img.Width != tc.w || img.Height != tc.h {
				t.Errorf("primary image = %dx%d, want %dx%d",
					img.Width, img.Height, tc.w, tc.h)
			}
			if len(img.Payload) == 0 {
				t.Error("payload is empty")
			}
		})
	}
}

// TestItemsAndProperties checks the item list and the property associations of
// the alpha fixture, which is the file with two items.
//
// The associations are the point: ipco holds two ispe and two pixi boxes, and
// which item gets which is decided only by ipma. The expected readings are the
// measured ones:
//
//	item 1 -> colr, av1C[6] essential, ispe[7], pixi[8]   (3 channels)
//	item 2 -> av1C[2] essential, ispe[3], pixi[4], auxC[5] essential  (1 channel)
func TestItemsAndProperties(t *testing.T) {
	f := parse(t, "a_alpha.avif")
	items, err := f.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	byID := map[uint32]*Item{}
	for _, it := range items {
		byID[it.ID] = it
	}

	colourItem, ok := byID[1]
	if !ok {
		t.Fatal("no item 1")
	}
	alphaItem, ok := byID[2]
	if !ok {
		t.Fatal("no item 2")
	}

	if colourItem.Type != ItemAV1 {
		t.Errorf("item 1 type = %q, want %q", colourItem.Type, ItemAV1)
	}

	// The colour item must have three channels and the alpha item one. Getting
	// these the wrong way round is exactly what reading the first pixi in ipco
	// produces, so the assertion is on the channel count rather than on presence.
	cp := colourItem.FindProperty("pixi")
	if cp == nil {
		t.Fatal("the colour item has no pixi property")
	}
	cv, err := cp.Decode()
	if err != nil {
		t.Fatalf("colour pixi: %v", err)
	}
	if cv.PixelInfo.NumChannels != 3 {
		t.Errorf("colour item has %d channels, want 3", cv.PixelInfo.NumChannels)
	}

	ap := alphaItem.FindProperty("pixi")
	if ap == nil {
		t.Fatal("the alpha item has no pixi property")
	}
	av, err := ap.Decode()
	if err != nil {
		t.Fatalf("alpha pixi: %v", err)
	}
	if av.PixelInfo.NumChannels != 1 {
		t.Errorf("alpha item has %d channels, want 1", av.PixelInfo.NumChannels)
	}

	// The feature that says the second item is an alpha channel.
	aux := alphaItem.FindProperty("auxC")
	if aux == nil {
		t.Fatal("the alpha item has no auxC property")
	}
	auxv, err := aux.Decode()
	if err != nil {
		t.Fatalf("auxC: %v", err)
	}
	const wantAux = "urn:mpeg:mpegB:cicp:systems:auxiliary:alpha"
	if auxv.AuxiliaryType.Type != wantAux {
		t.Errorf("auxC type = %q, want %q", auxv.AuxiliaryType.Type, wantAux)
	}
}

// TestEssentialFlag plants two mistakes: dropping the essential flag, and
// applying one item's flag to a property another item shares.
//
// The measured fixture marks av1C and auxC essential and leaves colr, ispe and
// pixi unmarked. Because ipco is shared, a reader that stores the flag on the
// property box rather than on the association would mark every av1C essential --
// which on this file happens to be right for both items, so the test also
// asserts a property that must NOT be essential.
func TestEssentialFlag(t *testing.T) {
	f := parse(t, "a_alpha.avif")
	items, err := f.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("got %d items", len(items))
	}
	for _, it := range items {
		av1c := it.FindProperty("av1C")
		if av1c == nil {
			t.Fatalf("item %d has no av1C", it.ID)
		}
		if !av1c.Essential {
			t.Errorf("item %d: av1C is not marked essential; the fixture marks it so", it.ID)
		}
		// colr is present on item 1 and not essential there.
		if colr := it.FindProperty("colr"); colr != nil && colr.Essential {
			t.Errorf("item %d: colr is marked essential, the fixture does not mark it", it.ID)
		}
		// ispe is never essential on this fixture.
		if ispe := it.FindProperty("ispe"); ispe != nil && ispe.Essential {
			t.Errorf("item %d: ispe is marked essential, the fixture does not mark it", it.ID)
		}
	}
}

// TestItemReference plants the two mistakes made in iref: treating a child as a
// full box, and omitting the reference_count field.
//
// The fixture's auxl payload is 00 02 00 01 00 01, which is from=2, count=1,
// to=[1]. Reading it without the count produces a second bogus reference, and
// treating the child as a full box consumes the from-item id and yields
// from=1 -> [1, 1].
func TestItemReference(t *testing.T) {
	f := parse(t, "a_alpha.avif")
	items, err := f.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}

	var alphaItem *Item
	for _, it := range items {
		if it.ID == 2 {
			alphaItem = it
		}
	}
	if alphaItem == nil {
		t.Fatal("no item 2")
	}
	if len(alphaItem.References) != 1 {
		t.Fatalf("item 2 has %d references, want 1: %+v",
			len(alphaItem.References), alphaItem.References)
	}
	ref := alphaItem.References[0]
	if ref.Type != "auxl" {
		t.Errorf("reference type = %q, want auxl", ref.Type)
	}
	if ref.From != 2 {
		t.Errorf("reference from = %d, want 2", ref.From)
	}
	// Exactly one target. Reading the payload without the count field yields two
	// ids and this length becomes 2.
	if len(ref.To) != 1 {
		t.Fatalf("reference targets = %v, want exactly [1]; a count field was probably skipped",
			ref.To)
	}
	if ref.To[0] != 1 {
		t.Errorf("reference target = %d, want 1", ref.To[0])
	}
}

// TestExtentsTileThePayload plants an offset-base mistake in iloc.
//
// The alpha fixture's two items occupy the mdat payload end to end with no gap
// and no overlap: item 2 is 444..463 and item 1 is 463..496, and mdat's payload
// runs 444..496. If iloc's field widths were read from the wrong position the
// extents would not join like this.
func TestExtentsTileThePayload(t *testing.T) {
	f := parse(t, "a_alpha.avif")
	items, err := f.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}

	mdat := f.Find("mdat")
	if mdat == nil {
		t.Fatal("no mdat")
	}
	mdatStart := uint64(mdat.Offset + mdat.HeaderSize)
	mdatEnd := uint64(mdat.Offset + mdat.Size)

	type span struct{ start, end uint64 }
	var spans []span
	for _, it := range items {
		if it.Location == nil {
			t.Fatalf("item %d has no location", it.ID)
		}
		for _, e := range it.Location.Extents {
			s := it.Location.BaseOffset + e.Offset
			spans = append(spans, span{s, s + e.Length})
		}
	}
	if len(spans) != 2 {
		t.Fatalf("got %d extents, want 2", len(spans))
	}

	// Sort by start so the join can be checked without assuming item order.
	if spans[0].start > spans[1].start {
		spans[0], spans[1] = spans[1], spans[0]
	}
	if spans[0].end != spans[1].start {
		t.Errorf("extents %d..%d and %d..%d do not join",
			spans[0].start, spans[0].end, spans[1].start, spans[1].end)
	}
	if spans[0].start != mdatStart {
		t.Errorf("first extent starts at %d, mdat payload starts at %d", spans[0].start, mdatStart)
	}
	if spans[1].end != mdatEnd {
		t.Errorf("last extent ends at %d, mdat ends at %d", spans[1].end, mdatEnd)
	}
}

// TestColourIsNotAFullBox plants a mistake that was made here: listing colr
// among the full boxes.
//
// colr has no version or flags. Its colour_type is the first four bytes of its
// payload, which on the fixture is "nclx". Skipping four bytes reads the CICP
// values as the type and reports a colour type of "\x00\x01\x00\r".
func TestColourIsNotAFullBox(t *testing.T) {
	f := parse(t, "a_64.avif")
	img, err := f.PrimaryImage()
	if err != nil {
		t.Fatalf("PrimaryImage: %v", err)
	}
	if img.Colour == nil {
		t.Fatal("no colour property")
	}
	if img.Colour.ColourType != "nclx" {
		t.Errorf("colour type = %q, want nclx; colr is not a full box", img.Colour.ColourType)
	}
	// The measured CICP values: primaries 1 (BT.709), transfer 13 (sRGB),
	// matrix 6 (BT.601).
	if img.Colour.ColourPrimaries != 1 {
		t.Errorf("colour primaries = %d, want 1", img.Colour.ColourPrimaries)
	}
	if !img.Colour.FullRange {
		t.Error("full range flag is clear; the fixture declares full range")
	}
}

// TestIlocSizeFieldsAreRead plants the mistake of reading iloc's field widths at
// the wrong offset.
//
// iloc stores offset_size, length_size, base_offset_size and index_size in two
// bytes at the front of its payload, after the version and flags. Reading them at
// offset four takes them from the item id and every extent lands elsewhere. The
// fixture declares 4, 4, 4 and 0.
func TestIlocSizeFieldsAreRead(t *testing.T) {
	f := parse(t, "a_64.avif")
	items, err := f.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items", len(items))
	}
	loc := items[0].Location
	if loc == nil {
		t.Fatal("item has no location; iloc was not applied")
	}
	if len(loc.Extents) != 1 {
		t.Fatalf("got %d extents, want 1", len(loc.Extents))
	}
	// The measured values: base offset 270, one extent of 58 bytes.
	if loc.BaseOffset != 270 {
		t.Errorf("base offset = %d, want 270", loc.BaseOffset)
	}
	if loc.Extents[0].Length != 58 {
		t.Errorf("extent length = %d, want 58", loc.Extents[0].Length)
	}
}

// TestTruncatedFileIsRejected plants a file cut short: a box claiming more bytes
// than remain must be an error, not a slice out of range.
func TestTruncatedFileIsRejected(t *testing.T) {
	full := load(t, "a_64.avif")
	for _, cut := range []int{4, 12, 40, 100, len(full) - 1} {
		if cut >= len(full) {
			continue
		}
		_, err := Parse(full[:cut])
		if err == nil {
			t.Errorf("Parse of the first %d bytes: got nil error, want a rejection", cut)
		}
	}
}

// TestBoxSizeLargerThanFileIsRejected plants an inflated box size.
func TestBoxSizeLargerThanFileIsRejected(t *testing.T) {
	b := append([]byte(nil), load(t, "a_64.avif")...)
	// Make the ftyp box claim far more bytes than the file holds.
	b[0], b[1], b[2], b[3] = 0x7F, 0xFF, 0xFF, 0xFF
	if _, err := Parse(b); err == nil {
		t.Error("Parse of a file with an oversized first box: got nil error")
	}
}

// TestNonHEIFIsRejected checks a file that is not a HEIF container at all.
func TestNonHEIFIsRejected(t *testing.T) {
	if _, err := Parse([]byte("this is not a HEIF file, not even close")); err == nil {
		t.Error("Parse of plain text: got nil error")
	}
	if _, err := Parse([]byte{0, 0, 0}); err == nil {
		t.Error("Parse of 3 bytes: got nil error")
	}
	// A well-formed box that is not ftyp first.
	notFtyp := []byte{
		0, 0, 0, 8, 'f', 'r', 'e', 'e',
	}
	if _, err := Parse(notFtyp); err == nil {
		t.Error("Parse of a file whose first box is not ftyp: got nil error")
	}
}

// Test64BitBoxSize plants the mistake of reading a 64-bit size as 32 bits.
//
// A box with size 1 carries its real size in the following eight bytes. Reading
// only four takes the high half as the next box's type.
func Test64BitBoxSize(t *testing.T) {
	// ftyp followed by a free box using a 64-bit size, then the body we expect to
	// find as a child is not needed: the point is that the free box is skipped by
	// its true length rather than by four bytes.
	var b []byte
	// ftyp: size 16, "ftyp", "avif", minor 0
	b = append(b, 0, 0, 0, 16, 'f', 't', 'y', 'p', 'a', 'v', 'i', 'f', 0, 0, 0, 0)
	// free box: size 1 marker, "free", then the 64-bit size = 16, then 0 bytes
	b = append(b, 0, 0, 0, 1, 'f', 'r', 'e', 'e')
	b = append(b, 0, 0, 0, 0, 0, 0, 0, 16)

	f, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Boxes) != 2 {
		t.Fatalf("got %d top-level boxes, want 2: %+v", len(f.Boxes), f.Boxes)
	}
	if f.Boxes[1].Type != "free" {
		t.Errorf("second box type = %q, want free", f.Boxes[1].Type)
	}
	if f.Boxes[1].HeaderSize != 16 {
		t.Errorf("header size = %d, want 16 for a 64-bit box", f.Boxes[1].HeaderSize)
	}
}

// TestExifPayload plants a mistake in the Exif offset handling.
//
// The fixture carries no Exif item, so this checks the specific refusal rather
// than a successful read. A file with no Exif must say so, and the four-byte
// header offset must be validated when one is present.
func TestExifPayload(t *testing.T) {
	f := parse(t, "a_64.avif")
	_, err := f.ExifPayload()
	if err == nil {
		t.Error("ExifPayload on a file with no Exif item: got nil error")
	}
}

// TestItemsWithNoMeta checks a container with a HEIF brand but no meta box is
// reported as such rather than producing empty items.
func TestItemsWithNoMeta(t *testing.T) {
	var b []byte
	b = append(b, 0, 0, 0, 16, 'f', 't', 'y', 'p', 'a', 'v', 'i', 'f', 0, 0, 0, 0)
	f, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := f.Meta(); err == nil {
		t.Error("Meta on a file without one: got nil error")
	}
	if _, err := f.Items(); err == nil {
		t.Error("Items on a file without a meta box: got nil error")
	}
	if _, err := f.PrimaryImage(); err == nil {
		t.Error("PrimaryImage on a file without a meta box: got nil error")
	}
}

// TestItemDataRejectsOtherConstructionMethods plants a silent wrong-answer path:
// construction method 1 addresses the idat box and method 2 another item, so
// returning file offsets for either would hand back the wrong bytes.
func TestItemDataRejectsOtherConstructionMethods(t *testing.T) {
	f := parse(t, "a_64.avif")
	it := &Item{
		ID: 99,
		Location: &ItemLocation{
			ConstructionMethod: 1,
			Extents:            []Extent{{Offset: 0, Length: 4}},
		},
	}
	if _, err := f.ItemData(it); err == nil {
		t.Error("ItemData with construction method 1: got nil error, want ErrUnsupported")
	}
}

// TestDepthLimit plants unbounded recursion: boxes that contain themselves would
// run until the stack ends without a limit.
func TestDepthLimit(t *testing.T) {
	// Build nested meta boxes deeper than the limit. Each level is a meta box
	// containing the next.
	depth := MaxDepth + 5
	var build func(level int) []byte
	build = func(level int) []byte {
		if level == 0 {
			return nil
		}
		inner := build(level - 1)
		body := append([]byte{0, 0, 0, 0}, inner...) // version and flags
		size := 8 + len(body)
		out := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size), 'm', 'e', 't', 'a'}
		return append(out, body...)
	}
	deep := build(depth)
	// Prepend a valid ftyp so Parse gets past its first check.
	ftyp := []byte{0, 0, 0, 16, 'f', 't', 'y', 'p', 'a', 'v', 'i', 'f', 0, 0, 0, 0}
	if _, err := Parse(append(ftyp, deep...)); err == nil {
		t.Error("Parse of boxes nested past the limit: got nil error")
	}
}
