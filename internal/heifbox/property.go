package heifbox

import (
	"encoding/binary"
	"fmt"
)

// SpatialExtents is the image size an ispe property declares.
type SpatialExtents struct {
	Width  uint32
	Height uint32
}

// PixelInfo is a pixi property: how many channels an item has and how many bits
// each carries.
type PixelInfo struct {
	// NumChannels counts the channels present. A three-channel pixi on an RGBA
	// image means the alpha is carried by a separate item.
	NumChannels int

	// BitsPerChannel is one entry per channel.
	BitsPerChannel []uint8
}

// ColourInfo is a colr property.
type ColourInfo struct {
	// ColourType is "nclx", "rICC" or "prof".
	ColourType string

	// For nclx: the CICP code points.
	ColourPrimaries    uint16
	TransferFunction   uint16
	MatrixCoefficients uint16
	FullRange          bool

	// For rICC and prof: the raw profile bytes.
	Profile []byte
}

// AV1Config is an av1C property, the AV1 codec configuration.
type AV1Config struct {
	Marker                          bool
	Version                         uint8
	SeqProfile                      uint8
	SeqLevelIdx                     uint8
	HighBitdepth                    bool
	TwelveBit                       bool
	Monochrome                      bool
	ChromaSubsamplingX              bool
	ChromaSubsamplingY              bool
	ChromaSamplePosition            uint8
	InitialPresentationDelayPresent bool
}

// AuxiliaryType is an auxC property, which names what an auxiliary image is for.
type AuxiliaryType struct {
	// Type is the auxiliary type string, for example the CICP alpha
	// declaration "urn:mpeg:mpegB:cicp:systems:auxiliary:alpha".
	Type string

	// Subtypes are the optional trailing bytes.
	Subtypes []byte
}

// HEVCConfig is an hvcC property, the HEVC codec configuration.
//
// It is decoded because a HEIC file carries it where an AVIF file carries av1C,
// and refusing to read it would leave HEIC files opaque. **No HEIC fixture exists
// on the machine this was developed on**, so this reading is implemented from the
// specification without a file written by another implementation behind it. The
// README says so, and the test suite has no fixture for it.
type HEVCConfig struct {
	ConfigurationVersion uint8
	GeneralProfileSpace  uint8
	GeneralTierFlag      bool
	GeneralProfileIDC    uint8
	GeneralLevelIDC      uint8
	ChromaFormatIDC      uint8
	BitDepthLumaMinus8   uint8
	BitDepthChromaMinus8 uint8
	AverageFrameRate     uint16
}

// PropertyValues holds the decoded form of every property type this package
// understands. Exactly one field is set per property, according to Type.
type PropertyValues struct {
	SpatialExtents *SpatialExtents
	PixelInfo      *PixelInfo
	ColourInfo     *ColourInfo
	AV1Config      *AV1Config
	HEVCConfig     *HEVCConfig
	AuxiliaryType  *AuxiliaryType

	// Rotation and Mirror come from irot and imir, which carry a single
	// meaningful byte.
	Rotation *uint8
	Mirror   *uint8
}

// Decode reads the property's fields. A property type this package does not
// understand yields an empty PropertyValues and no error, so a caller learns the
// type from Property.Type rather than from an error that reads like a failure of
// the file.
func (p *Property) Decode() (PropertyValues, error) {
	switch p.Type {
	case "ispe":
		v, err := p.spatialExtents()
		return PropertyValues{SpatialExtents: v}, err
	case "pixi":
		v, err := p.pixelInfo()
		return PropertyValues{PixelInfo: v}, err
	case "colr":
		v, err := p.colourInfo()
		return PropertyValues{ColourInfo: v}, err
	case "av1C":
		v, err := p.av1Config()
		return PropertyValues{AV1Config: v}, err
	case "hvcC":
		v, err := p.hevcConfig()
		return PropertyValues{HEVCConfig: v}, err
	case "auxC":
		v, err := p.auxiliaryType()
		return PropertyValues{AuxiliaryType: v}, err
	case "irot":
		v, err := p.singleByte("rotation angle")
		return PropertyValues{Rotation: v}, err
	case "imir":
		v, err := p.singleByte("mirror axis")
		return PropertyValues{Mirror: v}, err
	}
	return PropertyValues{}, nil
}

// singleByte reads the one meaningful byte of irot and imir.
func (p *Property) singleByte(what string) (*uint8, error) {
	body := p.Box.Body()
	if len(body) < 1 {
		return nil, fmt.Errorf("%w: %s property %q has an empty body", ErrMalformed, p.Type, what)
	}
	v := body[0]
	return &v, nil
}

func (p *Property) spatialExtents() (*SpatialExtents, error) {
	body := p.Box.Body()
	if len(body) < 8 {
		return nil, fmt.Errorf("%w: ispe is %d bytes, need 8 for width and height", ErrMalformed, len(body))
	}
	return &SpatialExtents{
		Width:  binary.BigEndian.Uint32(body[0:4]),
		Height: binary.BigEndian.Uint32(body[4:8]),
	}, nil
}

func (p *Property) pixelInfo() (*PixelInfo, error) {
	body := p.Box.Body()
	if len(body) < 1 {
		return nil, fmt.Errorf("%w: pixi has no channel count", ErrMalformed)
	}
	n := int(body[0])
	if n == 0 {
		return nil, fmt.Errorf("%w: pixi declares zero channels", ErrMalformed)
	}
	if len(body) < 1+n {
		return nil, fmt.Errorf("%w: pixi declares %d channels but carries %d bit-depth bytes",
			ErrMalformed, n, len(body)-1)
	}
	bits := make([]uint8, n)
	copy(bits, body[1:1+n])
	return &PixelInfo{NumChannels: n, BitsPerChannel: bits}, nil
}

func (p *Property) colourInfo() (*ColourInfo, error) {
	body := p.Box.Body()
	if len(body) < 4 {
		return nil, fmt.Errorf("%w: colr is %d bytes, need 4 for its type", ErrMalformed, len(body))
	}
	c := &ColourInfo{ColourType: string(body[0:4])}
	rest := body[4:]
	switch c.ColourType {
	case "nclx":
		if len(rest) < 7 {
			return nil, fmt.Errorf("%w: nclx colr is %d bytes, need 7", ErrMalformed, len(rest))
		}
		c.ColourPrimaries = binary.BigEndian.Uint16(rest[0:2])
		c.TransferFunction = binary.BigEndian.Uint16(rest[2:4])
		c.MatrixCoefficients = binary.BigEndian.Uint16(rest[4:6])
		// The full-range flag is the high bit of the last byte.
		c.FullRange = rest[6]&0x80 != 0
	case "rICC", "prof":
		c.Profile = append([]byte(nil), rest...)
	default:
		// An unrecognised colour type is reported by name with its bytes left
		// alone rather than guessed at.
		c.Profile = append([]byte(nil), rest...)
	}
	return c, nil
}

func (p *Property) av1Config() (*AV1Config, error) {
	body := p.Box.Body()
	if len(body) < 4 {
		return nil, fmt.Errorf("%w: av1C is %d bytes, need at least 4", ErrMalformed, len(body))
	}
	// av1C is not a full box: its first byte is a marker bit and its own version
	// field. Treating it as a full box would skip four bytes of configuration.
	c := &AV1Config{
		Marker:      body[0]&0x80 != 0,
		Version:     body[0] & 0x7F,
		SeqProfile:  body[1] >> 5,
		SeqLevelIdx: body[1] & 0x1F,
	}
	// The third byte packs the bit-depth and monochrome flags into its high bits.
	c.HighBitdepth = body[2]&0x40 != 0
	c.TwelveBit = body[2]&0x20 != 0
	c.Monochrome = body[2]&0x10 != 0
	c.ChromaSubsamplingX = body[2]&0x08 != 0
	c.ChromaSubsamplingY = body[2]&0x04 != 0
	c.ChromaSamplePosition = body[2] & 0x03
	// The initial presentation delay flag is the low bit of the fourth byte.
	c.InitialPresentationDelayPresent = body[3]&0x01 != 0
	return c, nil
}

func (p *Property) hevcConfig() (*HEVCConfig, error) {
	body := p.Box.Body()
	if len(body) < 23 {
		return nil, fmt.Errorf("%w: hvcC is %d bytes, need at least 23", ErrMalformed, len(body))
	}
	c := &HEVCConfig{ConfigurationVersion: body[0]}
	// Byte 1: profile space (2 bits), tier flag (1), profile idc (5).
	c.GeneralProfileSpace = body[1] >> 6
	c.GeneralTierFlag = body[1]&0x20 != 0
	c.GeneralProfileIDC = body[1] & 0x1F
	c.GeneralLevelIDC = body[12]
	// Bytes 13 and 14 hold the minimum chroma format and bit depths, each in the
	// low three bits.
	if len(body) >= 15 {
		c.ChromaFormatIDC = body[13] & 0x03
		c.BitDepthLumaMinus8 = body[14] & 0x07
		c.BitDepthChromaMinus8 = body[15] & 0x07
	}
	if len(body) >= 19 {
		c.AverageFrameRate = binary.BigEndian.Uint16(body[17:19])
	}
	return c, nil
}

func (p *Property) auxiliaryType() (*AuxiliaryType, error) {
	body := p.Box.Body()
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: auxC has an empty body", ErrMalformed)
	}
	a := &AuxiliaryType{Type: readCString(body)}
	if n := len(a.Type) + 1; n < len(body) {
		a.Subtypes = append([]byte(nil), body[n:]...)
	}
	return a, nil
}

// FindProperty returns the item's property of a given type, or nil. When several
// of a type are present the first is returned, which matches the order ipma gave.
func (it *Item) FindProperty(typ string) *Property {
	for _, p := range it.Properties {
		if p.Type == typ {
			return p
		}
	}
	return nil
}

// Image is the picture an item describes, assembled from its properties.
type Image struct {
	// Item is the item the picture comes from.
	Item *Item

	// Width and Height come from the ispe property.
	Width, Height uint32

	// PixelInfo comes from pixi, when the item has one.
	PixelInfo *PixelInfo

	// Colour comes from colr, when the item has one.
	Colour *ColourInfo

	// AV1 comes from av1C, when the item has one.
	AV1 *AV1Config

	// HEVC comes from hvcC, when the item has one.
	HEVC *HEVCConfig

	// Rotation and Mirror come from irot and imir.
	Rotation *uint8
	Mirror   *uint8

	// Payload is the coded image data, as stored. It is not decompressed.
	Payload []byte
}

// PrimaryImage returns the picture the file designates as its main one.
func (f *File) PrimaryImage() (*Image, error) {
	meta, err := f.Meta()
	if err != nil {
		return nil, err
	}
	pitm := FindChild(meta, "pitm")
	if pitm == nil {
		return nil, fmt.Errorf("%w: meta has no pitm box", ErrMalformed)
	}
	body := pitm.Body()
	var id uint32
	if pitm.Version() == 0 {
		if len(body) < 2 {
			return nil, fmt.Errorf("%w: pitm is too short for its item id", ErrMalformed)
		}
		id = uint32(binary.BigEndian.Uint16(body[0:2]))
	} else {
		if len(body) < 4 {
			return nil, fmt.Errorf("%w: pitm is too short for its item id", ErrMalformed)
		}
		id = binary.BigEndian.Uint32(body[0:4])
	}

	items, err := f.Items()
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.ID == id {
			return f.buildImage(it)
		}
	}
	// A pitm naming no item is a contradiction, not an empty result.
	return nil, fmt.Errorf("%w: pitm names item %d, which is not in the list", ErrNoItem, id)
}

// ImageByID returns the picture for a specific item.
func (f *File) ImageByID(id uint32) (*Image, error) {
	items, err := f.Items()
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.ID == id {
			return f.buildImage(it)
		}
	}
	return nil, fmt.Errorf("%w: item %d", ErrNoItem, id)
}

// buildImage assembles an Image from an item's properties and payload.
func (f *File) buildImage(it *Item) (*Image, error) {
	img := &Image{Item: it}
	for _, p := range it.Properties {
		v, err := p.Decode()
		if err != nil {
			return nil, fmt.Errorf("item %d property %q: %w", it.ID, p.Type, err)
		}
		switch {
		case v.SpatialExtents != nil:
			img.Width = v.SpatialExtents.Width
			img.Height = v.SpatialExtents.Height
		case v.PixelInfo != nil:
			img.PixelInfo = v.PixelInfo
		case v.ColourInfo != nil:
			img.Colour = v.ColourInfo
		case v.AV1Config != nil:
			img.AV1 = v.AV1Config
		case v.HEVCConfig != nil:
			img.HEVC = v.HEVCConfig
		case v.Rotation != nil:
			img.Rotation = v.Rotation
		case v.Mirror != nil:
			img.Mirror = v.Mirror
		}
	}

	payload, err := f.ItemData(it)
	if err != nil {
		return nil, err
	}
	img.Payload = payload
	return img, nil
}

// ItemData returns an item's bytes, following its location.
//
// Only construction method 0 is resolved, which addresses the file directly and
// is what every file measured here uses. Method 1 addresses the idat box and
// method 2 addresses another item's data; both return ErrUnsupported rather than
// a guess at the wrong bytes.
func (f *File) ItemData(it *Item) ([]byte, error) {
	if it.Location == nil {
		return nil, fmt.Errorf("%w: item %d has no location", ErrNoItem, it.ID)
	}
	loc := it.Location
	if loc.ConstructionMethod != 0 {
		return nil, fmt.Errorf("%w: item %d uses construction method %d",
			ErrUnsupported, it.ID, loc.ConstructionMethod)
	}

	var out []byte
	for _, e := range loc.Extents {
		start := loc.BaseOffset + e.Offset
		if uint64(len(f.data)) < start+e.Length {
			return nil, fmt.Errorf("%w: item %d extent %d..%d is past the %d-byte file",
				ErrMalformed, it.ID, start, start+e.Length, len(f.data))
		}
		out = append(out, f.data[start:start+e.Length]...)
	}
	return out, nil
}

// ExifPayload returns the Exif item's bytes with the four-byte offset prefix the
// format places in front of them removed.
//
// The prefix is the distance from the start of the Exif payload to the TIFF
// header, and an Exif parser wants the header itself. Its value is
// attacker-controlled, so it is validated rather than applied blindly.
func (f *File) ExifPayload() ([]byte, error) {
	items, err := f.Items()
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.Type != ItemExif {
			continue
		}
		raw, err := f.ItemData(it)
		if err != nil {
			return nil, err
		}
		if len(raw) < 4 {
			return nil, fmt.Errorf("%w: Exif item %d is only %d bytes", ErrMalformed, it.ID, len(raw))
		}
		skip := int(binary.BigEndian.Uint32(raw[0:4]))
		// The offset must land inside the payload; a value past the end would
		// otherwise be used to slice out of range.
		if skip < 0 || skip+4 > len(raw) {
			return nil, fmt.Errorf("%w: Exif item %d declares a %d-byte header offset in %d bytes",
				ErrMalformed, it.ID, skip, len(raw))
		}
		return raw[4+skip:], nil
	}
	return nil, fmt.Errorf("%w: no Exif item", ErrNoItem)
}
