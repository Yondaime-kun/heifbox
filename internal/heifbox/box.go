// Package heifbox reads the HEIF family of container formats: HEIC, AVIF, and
// anything else built on the ISO base media file format's box structure.
//
// It reads the structural metadata -- the box tree, the item list, the item
// locations, and the properties each item carries -- and returns embedded
// payloads such as EXIF unmodified. It does not decompress image data: an AV1 or
// HEVC decoder is a codec, not a container, and it is out of scope by design.
//
// Every structural fact here was measured against files written by other
// implementations. ImageMagick 1.17.6 produced the fixtures and is the
// independent implementation the readings are checked against; ffmpeg produced a
// fourth file for the same content so two encoders could be tested rather than
// one. RESEARCH.md records the measurements and the wrong turns.
package heifbox

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Errors returned by this package.
var (
	// ErrNotHEIF is returned when the bytes are not a HEIF-family file.
	ErrNotHEIF = errors.New("heifbox: not a HEIF file")
	// ErrMalformed is returned when a box contradicts its own declared size.
	ErrMalformed = errors.New("heifbox: malformed box structure")
	// ErrNoItem is returned when an item id names no item.
	ErrNoItem = errors.New("heifbox: no such item")
	// ErrUnsupported is returned for a box this package deliberately does not
	// decode, with the reason in the message.
	ErrUnsupported = errors.New("heifbox: unsupported")
)

// MaxDepth bounds how deep the box walker descends. A malformed file can nest
// boxes without end, and without a limit a reader would recurse until the stack
// runs out.
const MaxDepth = 32

// Box is one box in the file's tree.
type Box struct {
	// Type is the four-character box type.
	Type string

	// Offset is where the box header begins in the file.
	Offset int64

	// Size is the total box size including its header.
	Size int64

	// HeaderSize is 8, or 16 when the box uses a 64-bit size.
	HeaderSize int64

	// Children are the boxes contained in this one, for container boxes.
	Children []*Box

	// data is the file these boxes came from.
	data []byte

	// bodyStart is where this box's own fields begin, after the header and after
	// the version and flags if it is a full box.
	bodyStart int64

	// isFull records whether a version and flags precede the body.
	isFull bool
}

// Body returns the box's payload, which is everything after its header and, for
// a full box, after the version and flag bytes.
func (b *Box) Body() []byte {
	end := b.Offset + b.Size
	if end > int64(len(b.data)) {
		end = int64(len(b.data))
	}
	if b.bodyStart > end {
		return nil
	}
	return b.data[b.bodyStart:end]
}

// RawBody returns the payload including the version and flag bytes of a full box.
func (b *Box) RawBody() []byte {
	start := b.Offset + b.HeaderSize
	end := b.Offset + b.Size
	if end > int64(len(b.data)) {
		end = int64(len(b.data))
	}
	if start > end {
		return nil
	}
	return b.data[start:end]
}

// Version and Flags are meaningful only for a full box.
func (b *Box) Version() uint8 {
	if !b.isFull || len(b.RawBody()) < 4 {
		return 0
	}
	return b.RawBody()[0]
}

// Flags returns the 24 flag bits of a full box.
func (b *Box) Flags() uint32 {
	if !b.isFull || len(b.RawBody()) < 4 {
		return 0
	}
	r := b.RawBody()
	return uint32(r[1])<<16 | uint32(r[2])<<8 | uint32(r[3])
}

// containerTypes are the boxes whose payload is a list of other boxes. A box
// absent from this set has its payload treated as data, which is the safe
// default: walking a payload that is not boxes would invent structure.
var containerTypes = map[string]bool{
	"meta": true,
	"iprp": true,
	"ipco": true,
	"iinf": true,
	"iref": true,
	"dinf": true,
	"moov": true,
	"trak": true,
	"mdia": true,
	"minf": true,
	"stbl": true,
	"edts": true,
	"udta": true,
}

// fullBoxTypes are the boxes that carry a version byte and three flag bytes
// before their own fields.
var fullBoxTypes = map[string]bool{
	"meta": true,
	"iinf": true,
	"iref": true,
	"iloc": true,
	"ipma": true,
	"pitm": true,
	"hdlr": true,
	"infe": true,
	"ispe": true,
	"pixi": true,
	"auxC": true,
}

// av1C is deliberately absent from fullBoxTypes. It is not a full box: its first
// byte is a marker bit and its own version field, so treating it as a full box
// would skip four bytes of real configuration and report a codec profile of zero.

// File is a parsed HEIF file.
type File struct {
	// MajorBrand is the ftyp box's major brand, such as "avif" or "heic".
	MajorBrand string

	// MinorVersion is the ftyp box's minor version.
	MinorVersion uint32

	// CompatibleBrands are the brands the file claims compatibility with.
	CompatibleBrands []string

	// Boxes is the top-level box tree.
	Boxes []*Box

	data []byte
}

// Parse reads the box structure of a HEIF-family file.
//
// It does not require the file to be a valid image; it requires the container to
// be well formed. A file with a HEIF brand but no meta box parses and reports the
// absence, because a tool that inspects half-broken files is more useful than one
// that refuses them, and the absence is stated rather than filled in.
func Parse(data []byte) (*File, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("%w: %d bytes is too short for a box header", ErrNotHEIF, len(data))
	}
	f := &File{data: data}

	boxes, err := walkBoxes(data, 0, int64(len(data)), f, 0)
	if err != nil {
		return nil, err
	}
	f.Boxes = boxes

	// The ftyp box must come first or the file is not identified.
	if len(boxes) == 0 || boxes[0].Type != "ftyp" {
		return nil, fmt.Errorf("%w: the first box is not ftyp", ErrNotHEIF)
	}
	if err := f.readFtyp(boxes[0]); err != nil {
		return nil, err
	}
	return f, nil
}

// readFtyp reads the major brand, minor version and compatible brands.
func (f *File) readFtyp(b *Box) error {
	body := b.Body()
	if len(body) < 8 {
		return fmt.Errorf("%w: ftyp is %d bytes, too short for a brand and version", ErrMalformed, len(body))
	}
	f.MajorBrand = string(body[0:4])
	f.MinorVersion = binary.BigEndian.Uint32(body[4:8])

	// The compatible brands fill the rest, four bytes each. A trailing partial
	// brand is refused rather than padded.
	rest := body[8:]
	if len(rest)%4 != 0 {
		return fmt.Errorf("%w: ftyp has %d trailing bytes, not a whole number of brands", ErrMalformed, len(rest))
	}
	for i := 0; i+4 <= len(rest); i += 4 {
		f.CompatibleBrands = append(f.CompatibleBrands, string(rest[i:i+4]))
	}
	return nil
}

// walkBoxes reads a run of boxes from start to end, descending into container
// boxes up to MaxDepth.
func walkBoxes(data []byte, start, end int64, f *File, depth int) ([]*Box, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("%w: boxes nest deeper than %d", ErrMalformed, MaxDepth)
	}
	if end > int64(len(data)) {
		end = int64(len(data))
	}

	var out []*Box
	off := start
	for off+8 <= end {
		size := int64(binary.BigEndian.Uint32(data[off : off+4]))
		typ := string(data[off+4 : off+8])
		header := int64(8)

		switch size {
		case 0:
			// A zero size means the box runs to the end of its container.
			size = end - off
		case 1:
			// A 64-bit size follows the type. Reading it as a 32-bit size would
			// take the high half of this value as the type of the next box.
			if off+16 > end {
				return out, fmt.Errorf("%w: box %q at %d claims a 64-bit size but the header does not fit",
					ErrMalformed, typ, off)
			}
			size = int64(binary.BigEndian.Uint64(data[off+8 : off+16]))
			header = 16
		}

		if size < header {
			return out, fmt.Errorf("%w: box %q at %d declares size %d, smaller than its own header",
				ErrMalformed, typ, off, size)
		}
		if off+size > end {
			return out, fmt.Errorf("%w: box %q at %d declares %d bytes but only %d remain",
				ErrMalformed, typ, off, size, end-off)
		}

		b := &Box{
			Type:       typ,
			Offset:     off,
			Size:       size,
			HeaderSize: header,
			data:       data,
		}
		b.isFull = fullBoxTypes[typ]
		b.bodyStart = off + header
		if b.isFull {
			if b.bodyStart+4 > off+size {
				return out, fmt.Errorf("%w: full box %q at %d has no room for its version and flags",
					ErrMalformed, typ, off)
			}
			b.bodyStart += 4
		}

		if containerTypes[typ] {
			// iinf holds an item count between its full-box header and its child
			// boxes, so its children start two bytes further in.
			childStart := b.bodyStart
			if typ == "iinf" {
				if childStart+2 > off+size {
					return out, fmt.Errorf("%w: iinf at %d has no room for its item count", ErrMalformed, off)
				}
				childStart += 2
			}
			if typ == "iref" {
				// iref's children are themselves boxes, so no extra skip.
				_ = childStart
			}
			kids, err := walkBoxes(data, childStart, off+size, f, depth+1)
			if err != nil {
				return out, err
			}
			b.Children = kids
		}

		out = append(out, b)
		off += size
	}

	// A trailing fragment too small to be a box header means the container was
	// truncated mid-box, which is worth reporting rather than ignoring.
	if off != end {
		return out, fmt.Errorf("%w: %d trailing bytes at %d cannot be a box header",
			ErrMalformed, end-off, off)
	}
	return out, nil
}

// Find returns the first box of the given type anywhere in the tree, or nil.
func (f *File) Find(typ string) *Box {
	var found *Box
	var search func([]*Box)
	search = func(bs []*Box) {
		for _, b := range bs {
			if found != nil {
				return
			}
			if b.Type == typ {
				found = b
				return
			}
			search(b.Children)
		}
	}
	search(f.Boxes)
	return found
}

// FindAll returns every box of the given type, in tree order.
func (f *File) FindAll(typ string) []*Box {
	var out []*Box
	var search func([]*Box)
	search = func(bs []*Box) {
		for _, b := range bs {
			if b.Type == typ {
				out = append(out, b)
			}
			search(b.Children)
		}
	}
	search(f.Boxes)
	return out
}

// FindChild returns the first direct child of parent with the given type.
func FindChild(parent *Box, typ string) *Box {
	if parent == nil {
		return nil
	}
	for _, c := range parent.Children {
		if c.Type == typ {
			return c
		}
	}
	return nil
}

// Meta returns the meta box, which is where a HEIF file keeps its item
// structure. A file without one is a container the format does not describe.
func (f *File) Meta() (*Box, error) {
	m := f.Find("meta")
	if m == nil {
		return nil, fmt.Errorf("%w: no meta box", ErrNotHEIF)
	}
	return m, nil
}

// IsHEIF reports whether the major brand is one this package recognises as a
// HEIF-family brand.
//
// An unrecognised brand is not an error: the reader still walks the structure,
// because a brand it has not seen before is more likely a new one than a broken
// file. This reports the classification rather than enforcing it.
func (f *File) IsHEIF() bool {
	switch f.MajorBrand {
	case "heic", "heix", "hevc", "hevx", "heim", "heis", "hevm", "hevs",
		"mif1", "msf1", "avif", "avis":
		return true
	}
	return false
}
