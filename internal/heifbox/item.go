package heifbox

import (
	"encoding/binary"
	"fmt"
)

// ItemType is the four-character type recorded in an infe box, which says what
// kind of thing an item is.
type ItemType string

const (
	// ItemAV1 is a coded image using AV1, the type an AVIF file's picture uses.
	ItemAV1 ItemType = "av01"
	// ItemHEVC is a coded image using HEVC, the type a HEIC file's picture uses.
	ItemHEVC ItemType = "hvc1"
	// ItemGrid is a derived image composed from other images.
	ItemGrid ItemType = "grid"
	// ItemExif carries an Exif payload.
	ItemExif ItemType = "Exif"
	// ItemXMP carries an XMP payload.
	ItemXMP ItemType = "mime"
)

// Item is one entry in the file's item list.
type Item struct {
	// ID is the item identifier, which iloc, ipma and iref all refer to.
	ID uint32

	// Type is what the item is: a coded image, a grid, an Exif payload.
	Type ItemType

	// Name is the optional item name, empty when the file does not give one.
	Name string

	// ContentType and ContentEncoding are set for mime items, which is how XMP
	// and similar payloads are carried.
	ContentType     string
	ContentEncoding string

	// ProtectionIndex is non-zero when the item is protected.
	ProtectionIndex uint16

	// Location says where the item's bytes are, when iloc records it.
	Location *ItemLocation

	// Properties are the properties associated with this item, in the order
	// ipma lists them.
	Properties []*Property

	// References are the relationships this item takes part in.
	References []ItemReference
}

// ItemLocation is an item's byte range inside the file, from iloc.
type ItemLocation struct {
	// ConstructionMethod is 0 for a file offset, 1 for an idat-relative offset
	// and 2 for an offset into another item's data.
	ConstructionMethod uint16

	// DataReferenceIndex names the data entry the offsets are relative to. Zero
	// means the file itself, which is the normal case.
	DataReferenceIndex uint16

	// BaseOffset is added to every extent offset.
	BaseOffset uint64

	// Extents are the byte ranges the item occupies.
	Extents []Extent
}

// Extent is one contiguous byte range.
type Extent struct {
	Offset uint64
	Length uint64

	// Index is present only when iloc's index size is non-zero, and is used by
	// construction method 1 to address into the idat box.
	Index uint64
}

// Property is one entry in the ipco list.
type Property struct {
	// Index is the property's position in ipco, 1-based, which is the number
	// ipma refers to.
	Index int

	// Type is the four-character box type: ispe, pixi, colr, av1C, and so on.
	Type string

	// Box is the raw box, so a caller can read a property this package does not
	// decode.
	Box *Box

	// Essential is ipma's essential flag for this association.
	Essential bool
}

// ItemReference is one relationship from iref.
type ItemReference struct {
	// Type is the relationship: auxl, thmb, dimg, cdsc.
	Type string

	// From is the item the relationship starts at.
	From uint32

	// To are the items it points at.
	To []uint32
}

// Items returns the file's item list with locations, properties and references
// resolved into it.
func (f *File) Items() ([]*Item, error) {
	meta, err := f.Meta()
	if err != nil {
		return nil, err
	}

	items, err := f.readItemInfo(meta)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint32]*Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}

	if err := f.readIloc(meta, byID, items); err != nil {
		return nil, err
	}
	if err := f.readIprp(meta, byID, items); err != nil {
		return nil, err
	}
	if err := f.readIref(meta, byID); err != nil {
		return nil, err
	}
	return items, nil
}

// readItemInfo reads iinf and its infe children.
func (f *File) readItemInfo(meta *Box) ([]*Item, error) {
	iinf := FindChild(meta, "iinf")
	if iinf == nil {
		return nil, fmt.Errorf("%w: meta has no iinf box", ErrMalformed)
	}
	body := iinf.Body()
	version := iinf.Version()

	// The item count is 16 bits before version 1 and 32 bits from version 1 on.
	var count uint32
	var rest []byte
	if version == 0 {
		if len(body) < 2 {
			return nil, fmt.Errorf("%w: iinf is too short for its item count", ErrMalformed)
		}
		count = uint32(binary.BigEndian.Uint16(body[0:2]))
		rest = body[2:]
	} else {
		if len(body) < 4 {
			return nil, fmt.Errorf("%w: iinf is too short for its item count", ErrMalformed)
		}
		count = binary.BigEndian.Uint32(body[0:4])
		rest = body[4:]
	}

	// Bound the declared count against the bytes present before trusting it.
	if int(count) > len(rest)/8+1 {
		return nil, fmt.Errorf("%w: iinf declares %d items but only %d bytes follow",
			ErrMalformed, count, len(rest))
	}

	items := make([]*Item, 0, count)
	for _, child := range iinf.Children {
		if child.Type != "infe" {
			continue
		}
		it, err := parseInfe(child)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}

	// If the count and the number of boxes disagree the file contradicts itself,
	// and which to trust is not obvious. Saying so is better than picking one.
	if uint32(len(items)) != count {
		return nil, fmt.Errorf("%w: iinf declares %d items but carries %d infe boxes",
			ErrMalformed, count, len(items))
	}
	return items, nil
}

// parseInfe reads one infe box. Its layout depends on its version: only version
// 2 and later carry the item type, which is the field that says what the item is.
func parseInfe(b *Box) (*Item, error) {
	body := b.Body()
	version := b.Version()
	it := &Item{}

	switch {
	case version >= 2:
		// The minimum is item id, protection index, item type and a NUL
		// terminator for an empty name: 2+2+4+1 for version 2, 4+2+4+1 for
		// version 3. A file written by ImageMagick has exactly nine bytes here.
		// Requiring twelve, as an earlier version did, rejects every real file.
		if version == 2 {
			if len(body) < 9 {
				return nil, fmt.Errorf("%w: infe version 2 is only %d bytes, need at least 9",
					ErrMalformed, len(body))
			}
			it.ID = uint32(binary.BigEndian.Uint16(body[0:2]))
			it.ProtectionIndex = binary.BigEndian.Uint16(body[2:4])
			it.Type = ItemType(string(body[4:8]))
			it.Name = readCString(body[8:])
		} else {
			if len(body) < 11 {
				return nil, fmt.Errorf("%w: infe version %d is only %d bytes, need at least 11",
					ErrMalformed, version, len(body))
			}
			it.ID = binary.BigEndian.Uint32(body[0:4])
			it.ProtectionIndex = binary.BigEndian.Uint16(body[4:6])
			it.Type = ItemType(string(body[6:10]))
			it.Name = readCString(body[10:])
		}
	case version == 1:
		return nil, fmt.Errorf("%w: infe version 1 predates item types and is not decoded",
			ErrUnsupported)
	default:
		if len(body) < 4 {
			return nil, fmt.Errorf("%w: infe version 0 is only %d bytes", ErrMalformed, len(body))
		}
		it.ID = uint32(binary.BigEndian.Uint16(body[0:2]))
		it.ProtectionIndex = binary.BigEndian.Uint16(body[2:4])
		it.Name = readCString(body[4:])
	}

	// A mime item carries its content type after the name, which is how an XMP
	// payload declares itself.
	if it.Type == ItemXMP {
		rest := body[itemNameEnd(body, it.Name):]
		it.ContentType = readCString(rest)
		if n := len(it.ContentType) + 1; n < len(rest) {
			it.ContentEncoding = readCString(rest[n:])
		}
	}
	return it, nil
}

// itemNameEnd returns the offset just past where the name field ended.
func itemNameEnd(body []byte, name string) int {
	start := 0
	// The name begins after the fixed fields, whose size depends on the version,
	// and it was already parsed from that position.
	switch {
	case len(body) >= 10:
		start = 10
	default:
		start = 8
	}
	end := start + len(name) + 1
	if end > len(body) {
		end = len(body)
	}
	return end
}

// readCString reads a NUL-terminated string from the front of b.
func readCString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// readIloc reads the item location box and attaches a location to each item.
func (f *File) readIloc(meta *Box, byID map[uint32]*Item, items []*Item) error {
	iloc := FindChild(meta, "iloc")
	if iloc == nil {
		// iloc is not strictly required: an item could be located elsewhere. Its
		// absence is recorded by every item having no location.
		return nil
	}
	body := iloc.Body()
	version := iloc.Version()
	if len(body) < 4 {
		return fmt.Errorf("%w: iloc is only %d bytes", ErrMalformed, len(body))
	}

	// The four field widths are stored in the box rather than fixed, and reading
	// them as constants mislocates every extent of a file that packs them
	// differently.
	// Body() has already skipped the version and flag bytes, so the two size
	// bytes are at the front of the payload. Reading them at offset four, as an
	// earlier version did, takes them from the item id instead and every extent
	// lands in the wrong place.
	offsetSize := int(body[0] >> 4)
	lengthSize := int(body[0] & 0x0F)
	baseOffsetSize := int(body[1] >> 4)
	indexSize := int(body[1] & 0x0F)
	pos := 2

	var count uint32
	if version < 2 {
		if pos+2 > len(body) {
			return fmt.Errorf("%w: iloc is too short for its item count", ErrMalformed)
		}
		count = uint32(binary.BigEndian.Uint16(body[pos : pos+2]))
		pos += 2
	} else {
		if pos+4 > len(body) {
			return fmt.Errorf("%w: iloc is too short for its item count", ErrMalformed)
		}
		count = binary.BigEndian.Uint32(body[pos : pos+4])
		pos += 4
	}
	if int(count) > len(body) {
		return fmt.Errorf("%w: iloc declares %d items in a %d-byte box", ErrMalformed, count, len(body))
	}

	rd := func(n int) (uint64, error) {
		if n == 0 {
			return 0, nil
		}
		if pos+n > len(body) {
			return 0, fmt.Errorf("%w: iloc needs %d more bytes", ErrMalformed, n)
		}
		var v uint64
		for i := 0; i < n; i++ {
			v = v<<8 | uint64(body[pos+i])
		}
		pos += n
		return v, nil
	}

	for i := 0; i < int(count); i++ {
		var id uint32
		if version < 2 {
			v, err := rd(2)
			if err != nil {
				return err
			}
			id = uint32(v)
		} else {
			v, err := rd(4)
			if err != nil {
				return err
			}
			id = uint32(v)
		}

		loc := &ItemLocation{}
		if version == 1 || version == 2 {
			v, err := rd(2)
			if err != nil {
				return err
			}
			loc.ConstructionMethod = uint16(v)
		}
		dri, err := rd(2)
		if err != nil {
			return err
		}
		loc.DataReferenceIndex = uint16(dri)
		base, err := rd(baseOffsetSize)
		if err != nil {
			return err
		}
		loc.BaseOffset = base

		ec, err := rd(2)
		if err != nil {
			return err
		}
		extentCount := uint16(ec)
		// Each extent is at most offset+length+index bytes; bound the count
		// before looping so a hostile value cannot spin.
		maxExtent := offsetSize + lengthSize + indexSize
		if maxExtent > 0 && int(extentCount) > (len(body)-pos)/maxExtent+1 {
			return fmt.Errorf("%w: iloc item %d declares %d extents in %d remaining bytes",
				ErrMalformed, id, extentCount, len(body)-pos)
		}

		for j := 0; j < int(extentCount); j++ {
			var ext Extent
			if (version == 1 || version == 2) && indexSize > 0 {
				v, err := rd(indexSize)
				if err != nil {
					return err
				}
				ext.Index = v
			}
			o, err := rd(offsetSize)
			if err != nil {
				return err
			}
			l, err := rd(lengthSize)
			if err != nil {
				return err
			}
			ext.Offset = o
			ext.Length = l
			loc.Extents = append(loc.Extents, ext)
		}

		if it, ok := byID[id]; ok {
			it.Location = loc
		}
	}
	return nil
}

// readIprp reads iprp, building the property list from ipco and attaching
// properties to items from ipma.
//
// This is the part that decides what a caller can learn about an image. ipco is
// an ordered list and ipma holds indices into it, so the same property box is
// shared between items and the order is what the indices mean.
func (f *File) readIprp(meta *Box, byID map[uint32]*Item, items []*Item) error {
	iprp := FindChild(meta, "iprp")
	if iprp == nil {
		return nil
	}
	ipco := FindChild(iprp, "ipco")
	if ipco == nil {
		return fmt.Errorf("%w: iprp has no ipco box", ErrMalformed)
	}

	// The property index is the position in ipco, counting from one.
	props := make([]*Property, 0, len(ipco.Children))
	for i, c := range ipco.Children {
		props = append(props, &Property{Index: i + 1, Type: c.Type, Box: c})
	}

	ipma := FindChild(iprp, "ipma")
	if ipma == nil {
		// Without ipma no item has properties, which is legal though unusual.
		return nil
	}
	body := ipma.Body()
	version := ipma.Version()
	flags := ipma.Flags()
	if len(body) < 4 {
		return fmt.Errorf("%w: ipma is only %d bytes", ErrMalformed, len(body))
	}

	count := binary.BigEndian.Uint32(body[0:4])
	pos := 4
	// Flag bit 0 selects 16-bit association entries rather than 8-bit ones.
	large := flags&1 != 0

	if int(count) > len(body) {
		return fmt.Errorf("%w: ipma declares %d entries in a %d-byte box", ErrMalformed, count, len(body))
	}

	for i := 0; i < int(count); i++ {
		if pos+2 > len(body) {
			return fmt.Errorf("%w: ipma ran out of bytes at entry %d", ErrMalformed, i)
		}
		id := uint32(binary.BigEndian.Uint16(body[pos : pos+2]))
		pos += 2
		if pos+1 > len(body) {
			return fmt.Errorf("%w: ipma ran out of bytes at entry %d", ErrMalformed, i)
		}
		assocCount := int(body[pos])
		pos++

		it, ok := byID[id]
		for j := 0; j < assocCount; j++ {
			var raw uint16
			if large {
				if pos+2 > len(body) {
					return fmt.Errorf("%w: ipma ran out of bytes at item %d association %d",
						ErrMalformed, id, j)
				}
				raw = binary.BigEndian.Uint16(body[pos : pos+2])
				pos += 2
			} else {
				if pos+1 > len(body) {
					return fmt.Errorf("%w: ipma ran out of bytes at item %d association %d",
						ErrMalformed, id, j)
				}
				raw = uint16(body[pos])
				pos++
			}
			// The high bit of the association is the essential flag and the rest
			// is the 1-based index into ipco. The mask depends on the form: an
			// 8-bit association has a 7-bit index, so masking with 0x7FFF there
			// keeps the essential bit and turns index 3 into 131.
			var essential bool
			var idx int
			if large {
				essential = raw&0x8000 != 0
				idx = int(raw & 0x7FFF)
			} else {
				essential = raw&0x80 != 0
				idx = int(raw & 0x7F)
			}
			if idx == 0 || idx > len(props) {
				return fmt.Errorf("%w: ipma item %d refers to property %d of %d",
					ErrMalformed, id, idx, len(props))
			}
			if !ok {
				continue
			}
			// Copy the property so the essential flag, which is per association,
			// does not leak between items that share one property box.
			cp := *props[idx-1]
			cp.Essential = essential
			it.Properties = append(it.Properties, &cp)
		}
	}
	_ = version
	return nil
}

// readIref reads item references and attaches them to the items they start from.
//
// Two things about this box cost a wrong parse and both were settled by measuring
// the bytes and then confirming against ISO/IEC 14496-12:2015 section 8.11.12.2:
//
//   - iref itself is a full box, but its children are not. A child begins with
//     the from-item id directly, with no version or flags of its own.
//   - each child carries a reference_count between the from-item id and the
//     target ids: from_item_ID, reference_count, to_item_ID[reference_count].
//
// The fixture's payload is 00 02 00 01 00 01, which is from=2, count=1, to=[1]:
// item 2, the alpha auxiliary, refers to item 1, the colour image. Reading it
// without the count field gives a bogus second reference to the same item.
func (f *File) readIref(meta *Box, byID map[uint32]*Item) error {
	iref := FindChild(meta, "iref")
	if iref == nil {
		return nil
	}
	version := iref.Version()
	idSize := 2
	if version != 0 {
		idSize = 4
	}

	for _, child := range iref.Children {
		// RawBody is the child's payload after its own box header, with no
		// version or flags removed, because a child here has none.
		inner := child.RawBody()
		if len(inner) < idSize+2 {
			return fmt.Errorf("%w: iref child %q is %d bytes, too short for an item id and a count",
				ErrMalformed, child.Type, len(inner))
		}

		pos := 0
		var from uint32
		if idSize == 2 {
			from = uint32(binary.BigEndian.Uint16(inner[0:2]))
		} else {
			from = binary.BigEndian.Uint32(inner[0:4])
		}
		pos += idSize

		count := int(binary.BigEndian.Uint16(inner[pos : pos+2]))
		pos += 2

		// Bound the declared count against the bytes actually present.
		if count > (len(inner)-pos)/idSize {
			return fmt.Errorf("%w: iref child %q declares %d references in %d remaining bytes",
				ErrMalformed, child.Type, count, len(inner)-pos)
		}

		ref := ItemReference{Type: child.Type, From: from}
		for i := 0; i < count; i++ {
			if idSize == 2 {
				ref.To = append(ref.To, uint32(binary.BigEndian.Uint16(inner[pos:pos+2])))
			} else {
				ref.To = append(ref.To, binary.BigEndian.Uint32(inner[pos:pos+4]))
			}
			pos += idSize
		}

		if it, ok := byID[from]; ok {
			it.References = append(it.References, ref)
		}
	}
	return nil
}
