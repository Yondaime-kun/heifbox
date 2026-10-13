// Command heifbox prints the structure and metadata of a HEIF-family file: its
// brands, its box tree, its items, and what each item declares about itself.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/yondaime-kun/heifbox/internal/heifbox"
)

type options struct {
	path      string
	showTree  bool
	showItems bool
	asJSON    bool
	payload   uint
	out       string
	depth     int
}

func main() {
	var o options
	flag.StringVar(&o.path, "file", "", "path to a HEIC, HEIF or AVIF file")
	flag.BoolVar(&o.showTree, "tree", false, "print the box tree")
	flag.BoolVar(&o.showItems, "items", false, "print items with their properties")
	flag.BoolVar(&o.asJSON, "json", false, "print as JSON")
	flag.UintVar(&o.payload, "payload", 0, "write the coded payload of this item id to -out")
	flag.StringVar(&o.out, "out", "", "where to write a payload, with -payload")
	flag.IntVar(&o.depth, "depth", 8, "how deep the tree print descends")
	flag.Parse()

	if o.path == "" && flag.NArg() > 0 {
		o.path = flag.Arg(0)
	}
	if o.path == "" {
		fmt.Fprintln(os.Stderr, "heifbox: give a file with -file, or as an argument")
		flag.Usage()
		os.Exit(2)
	}

	data, err := os.ReadFile(o.path)
	if err != nil {
		fail(err)
	}
	f, err := heifbox.Parse(data)
	if err != nil {
		fail(err)
	}

	if o.payload != 0 {
		if o.out == "" {
			fmt.Fprintln(os.Stderr, "heifbox: -payload needs -out")
			os.Exit(2)
		}
		img, err := f.ImageByID(uint32(o.payload))
		if err != nil {
			fail(err)
		}
		if err := os.WriteFile(o.out, img.Payload, 0o644); err != nil {
			fail(err)
		}
		fmt.Printf("wrote %d bytes of item %d to %s\n", len(img.Payload), o.payload, o.out)
		return
	}

	// Default to showing everything when no view was chosen, so the common case
	// needs no flags.
	if !o.showTree && !o.showItems && !o.asJSON {
		o.showTree = true
		o.showItems = true
	}

	if o.asJSON {
		if err := printJSON(o.path, f); err != nil {
			fail(err)
		}
		return
	}

	printHeader(o.path, f)
	if o.showTree {
		printTree(f, o.depth)
	}
	if o.showItems {
		printItems(f)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "heifbox: %v\n", err)
	os.Exit(1)
}

func printHeader(path string, f *heifbox.File) {
	fmt.Printf("file       : %s\n", path)
	fmt.Printf("major brand: %s\n", f.MajorBrand)
	fmt.Printf("compatible : %s\n", strings.Join(f.CompatibleBrands, " "))
	if f.IsHEIF() {
		fmt.Println("family     : recognised HEIF brand")
	} else {
		fmt.Println("family     : brand not recognised; structure still read")
	}
}

func printTree(f *heifbox.File, maxDepth int) {
	fmt.Println("tree       :")
	var walk func(bs []*heifbox.Box, indent string, depth int)
	walk = func(bs []*heifbox.Box, indent string, depth int) {
		for _, b := range bs {
			extra := ""
			if b.HeaderSize == 16 {
				extra = " (64-bit size)"
			}
			fmt.Printf("%s%s size=%d%s\n", indent, b.Type, b.Size, extra)
			if len(b.Children) == 0 {
				continue
			}
			if depth+1 < maxDepth {
				walk(b.Children, indent+"  ", depth+1)
			} else {
				fmt.Printf("%s  ... %d more boxes\n", indent, len(b.Children))
			}
		}
	}
	walk(f.Boxes, "  ", 0)
}

func printItems(f *heifbox.File) {
	items, err := f.Items()
	if err != nil {
		fmt.Printf("items      : %v\n", err)
		return
	}
	fmt.Printf("items      : %d\n", len(items))
	for _, it := range items {
		fmt.Printf("  item %d  type=%s", it.ID, it.Type)
		if it.Name != "" {
			fmt.Printf("  name=%q", it.Name)
		}
		if it.ProtectionIndex != 0 {
			fmt.Printf("  protectionIndex=%d", it.ProtectionIndex)
		}
		fmt.Println()
		if it.Location != nil {
			for _, e := range it.Location.Extents {
				fmt.Printf("      bytes %d..%d (%d bytes)\n",
					it.Location.BaseOffset+e.Offset,
					it.Location.BaseOffset+e.Offset+e.Length, e.Length)
			}
		}
		for _, p := range it.Properties {
			mark := ""
			if p.Essential {
				mark = "  [essential]"
			}
			fmt.Printf("      [%d] %s%s%s\n", p.Index, p.Type, describeProperty(p), mark)
		}
		for _, r := range it.References {
			fmt.Printf("      ref %s -> %v\n", r.Type, r.To)
		}
	}

	img, err := f.PrimaryImage()
	if err != nil {
		fmt.Printf("primary    : %v\n", err)
		return
	}
	fmt.Printf("primary    : item %d  %dx%d  payload=%d bytes\n",
		img.Item.ID, img.Width, img.Height, len(img.Payload))
	if img.Colour != nil {
		fmt.Printf("             colour   %s\n", describeColour(img.Colour))
	}
	if img.PixelInfo != nil {
		fmt.Printf("             channels %d at %v bits\n",
			img.PixelInfo.NumChannels, img.PixelInfo.BitsPerChannel)
	}
	if img.AV1 != nil {
		fmt.Printf("             av1      profile=%d level=%d highBitdepth=%v\n",
			img.AV1.SeqProfile, img.AV1.SeqLevelIdx, img.AV1.HighBitdepth)
	}
}

func describeProperty(p *heifbox.Property) string {
	v, err := p.Decode()
	if err != nil {
		return fmt.Sprintf("  (%v)", err)
	}
	switch {
	case v.SpatialExtents != nil:
		return fmt.Sprintf("  %dx%d", v.SpatialExtents.Width, v.SpatialExtents.Height)
	case v.PixelInfo != nil:
		return fmt.Sprintf("  channels=%d bits=%v", v.PixelInfo.NumChannels, v.PixelInfo.BitsPerChannel)
	case v.ColourInfo != nil:
		return "  " + describeColour(v.ColourInfo)
	case v.AV1Config != nil:
		return fmt.Sprintf("  profile=%d level=%d highBitdepth=%v monochrome=%v",
			v.AV1Config.SeqProfile, v.AV1Config.SeqLevelIdx, v.AV1Config.HighBitdepth, v.AV1Config.Monochrome)
	case v.HEVCConfig != nil:
		return fmt.Sprintf("  profileIDC=%d levelIDC=%d",
			v.HEVCConfig.GeneralProfileIDC, v.HEVCConfig.GeneralLevelIDC)
	case v.AuxiliaryType != nil:
		return fmt.Sprintf("  %s", v.AuxiliaryType.Type)
	case v.Rotation != nil:
		return fmt.Sprintf("  angle=%d", *v.Rotation)
	case v.Mirror != nil:
		return fmt.Sprintf("  axis=%d", *v.Mirror)
	}
	return ""
}

func describeColour(c *heifbox.ColourInfo) string {
	switch c.ColourType {
	case "nclx":
		rng := "limited"
		if c.FullRange {
			rng = "full"
		}
		return fmt.Sprintf("nclx primaries=%d transfer=%d matrix=%d range=%s",
			c.ColourPrimaries, c.TransferFunction, c.MatrixCoefficients, rng)
	case "rICC", "prof":
		return fmt.Sprintf("%s profile, %d bytes", c.ColourType, len(c.Profile))
	}
	return fmt.Sprintf("%s, %d bytes", c.ColourType, len(c.Profile))
}

// jsonFile is the shape -json prints. It is a separate type so the output stays
// stable even if the package's own types change.
type jsonFile struct {
	File             string            `json:"file"`
	MajorBrand       string            `json:"majorBrand"`
	CompatibleBrands []string          `json:"compatibleBrands"`
	Recognised       bool              `json:"recognisedBrand"`
	Boxes            []jsonBox         `json:"boxes"`
	Items            []jsonItem        `json:"items,omitempty"`
	Primary          *jsonImage        `json:"primaryImage,omitempty"`
	Errors           map[string]string `json:"errors,omitempty"`
}

type jsonBox struct {
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	Children []jsonBox `json:"children,omitempty"`
}

type jsonItem struct {
	ID         uint32          `json:"id"`
	Type       string          `json:"type"`
	Name       string          `json:"name,omitempty"`
	Protection uint16          `json:"protectionIndex,omitempty"`
	Extents    []jsonExtent    `json:"extents,omitempty"`
	Properties []jsonProperty  `json:"properties,omitempty"`
	References []jsonReference `json:"references,omitempty"`
}

type jsonExtent struct {
	Offset uint64 `json:"offset"`
	Length uint64 `json:"length"`
}

type jsonProperty struct {
	Index     int    `json:"index"`
	Type      string `json:"type"`
	Essential bool   `json:"essential"`
	Summary   string `json:"summary"`
}

type jsonReference struct {
	Type string   `json:"type"`
	To   []uint32 `json:"to"`
}

type jsonImage struct {
	ItemID         uint32  `json:"itemId"`
	Width          uint32  `json:"width"`
	Height         uint32  `json:"height"`
	PayloadBytes   int     `json:"payloadBytes"`
	Channels       int     `json:"channels,omitempty"`
	BitsPerChannel []uint8 `json:"bitsPerChannel,omitempty"`
	Colour         string  `json:"colour,omitempty"`
}

func printJSON(path string, f *heifbox.File) error {
	out := jsonFile{
		File:             path,
		MajorBrand:       f.MajorBrand,
		CompatibleBrands: f.CompatibleBrands,
		Recognised:       f.IsHEIF(),
		Boxes:            toJSONBoxes(f.Boxes),
	}

	if items, err := f.Items(); err == nil {
		for _, it := range items {
			ji := jsonItem{ID: it.ID, Type: string(it.Type), Name: it.Name, Protection: it.ProtectionIndex}
			if it.Location != nil {
				for _, e := range it.Location.Extents {
					ji.Extents = append(ji.Extents, jsonExtent{
						Offset: it.Location.BaseOffset + e.Offset,
						Length: e.Length,
					})
				}
			}
			for _, p := range it.Properties {
				ji.Properties = append(ji.Properties, jsonProperty{
					Index: p.Index, Type: p.Type, Essential: p.Essential,
					Summary: strings.TrimSpace(describeProperty(p)),
				})
			}
			for _, r := range it.References {
				ji.References = append(ji.References, jsonReference{Type: r.Type, To: r.To})
			}
			out.Items = append(out.Items, ji)
		}
	} else {
		out.setError("items", err.Error())
	}

	if img, err := f.PrimaryImage(); err == nil {
		ji := &jsonImage{
			ItemID: img.Item.ID, Width: img.Width, Height: img.Height,
			PayloadBytes: len(img.Payload),
		}
		if img.PixelInfo != nil {
			ji.Channels = img.PixelInfo.NumChannels
			ji.BitsPerChannel = img.PixelInfo.BitsPerChannel
		}
		if img.Colour != nil {
			ji.Colour = describeColour(img.Colour)
		}
		out.Primary = ji
	} else {
		out.setError("primaryImage", err.Error())
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func toJSONBoxes(bs []*heifbox.Box) []jsonBox {
	out := make([]jsonBox, 0, len(bs))
	for _, b := range bs {
		out = append(out, jsonBox{Type: b.Type, Size: b.Size, Children: toJSONBoxes(b.Children)})
	}
	return out
}

func (j *jsonFile) setError(key, msg string) {
	if j.Errors == nil {
		j.Errors = map[string]string{}
	}
	j.Errors[key] = msg
}
