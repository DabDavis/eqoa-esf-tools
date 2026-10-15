package esf

import (
	"encoding/binary"
	"image"
	"image/color"
)

// Font represents a PS2 bitmap font from a CSF/ESF file.
// PS2: ParseFontObj (0x0043A830), type 0x7000.
// Children: 0x7010 (header), 0x7020 (display widths), 0x7030 (CLUT + per-char pixel data).
//
// 0x7030 layout (VIESFParse::ParseFontObj, viesfparse.cpp:4173):
//   depth 0 or 1 only: palCount(i32) + palCount RGBA entries
//   per char × numChars: charCode(u16) + n(i32) + charHeight rows of n bytes.
// Depth 0 is 4 bits a pixel, low nibble first (Convert4); depth 1 is a byte.
// ARIAL.CSF is depth 1 with 256 entries, ARIALUI.CSF depth 0 with 16.
type Font struct {
	info       *ObjInfo
	DictID     uint32
	NumChars   int
	CharHeight int
	BitDepth   int
	Widths     []byte // display widths per char (from 0x7020)
	// Palette is the CLUT the glyph pixels index. ARIAL's entry 1 is an
	// opaque grey (128,128,128,255), the outline, and entry 2 white.
	Palette []color.NRGBA
	Glyphs  []FontGlyph
}

// FontGlyph holds one character's bitmap data.
type FontGlyph struct {
	CharCode uint16
	RowBytes int    // pixels per row (a 4-bit glyph is expanded to one byte a pixel)
	Pixels   []byte // RowBytes * charHeight palette indices, row-major
}

func (f *Font) ObjInfo() *ObjInfo { return f.info }

func (f *Font) Load(file *ObjFile) error {
	// Header (0x7010)
	hdr := f.info.Child(0x7010)
	if hdr != nil {
		file.Seek(hdr.Offset)
		f.DictID = file.readUint32()
		f.NumChars = int(file.readInt32())
		f.CharHeight = int(file.readInt32())
		f.BitDepth = int(file.readInt32())
	}

	// Display widths (0x7020) — 1 byte per character
	ct := f.info.Child(0x7020)
	if ct != nil {
		f.Widths = file.RawBytes(int(ct.Offset), int(ct.Size))
	}

	// Pixel data (0x7030): CLUT + per-char glyph data
	px := f.info.Child(0x7030)
	if px == nil {
		return nil
	}
	pxData := file.RawBytes(int(px.Offset), int(px.Size))
	offset := 0
	if f.BitDepth == 0 || f.BitDepth == 1 {
		if len(pxData) < 4 {
			return nil
		}
		n := int(binary.LittleEndian.Uint32(pxData))
		offset = 4 + n*4
		if n < 0 || offset > len(pxData) {
			return nil
		}
		f.Palette = make([]color.NRGBA, n)
		for i := range f.Palette {
			c := pxData[4+i*4:]
			f.Palette[i] = color.NRGBA{c[0], c[1], c[2], c[3]}
		}
	} else {
		return nil // direct colour: no shipped font uses it
	}

	f.Glyphs = make([]FontGlyph, 0, f.NumChars)
	for i := 0; i < f.NumChars && offset+6 <= len(pxData); i++ {
		charCode := binary.LittleEndian.Uint16(pxData[offset:])
		rowBytes := int(binary.LittleEndian.Uint32(pxData[offset+2:]))
		offset += 6

		if rowBytes <= 0 || rowBytes > 256 || offset+rowBytes*f.CharHeight > len(pxData) {
			break
		}

		g := FontGlyph{CharCode: charCode, RowBytes: rowBytes}
		if f.BitDepth == 0 {
			// Convert4 into an I8 store: the char's width in pixels, two
			// a byte, low nibble first.
			w := rowBytes * 2
			if i < len(f.Widths) && int(f.Widths[i]) <= w {
				w = int(f.Widths[i])
			}
			g.RowBytes = w
			g.Pixels = make([]byte, w*f.CharHeight)
			for y := 0; y < f.CharHeight; y++ {
				row := pxData[offset+y*rowBytes:]
				for x := 0; x < w; x++ {
					b := row[x>>1]
					if x&1 == 0 {
						b &= 0x0F
					} else {
						b >>= 4
					}
					g.Pixels[y*w+x] = b
				}
			}
		} else {
			g.Pixels = make([]byte, rowBytes*f.CharHeight)
			copy(g.Pixels, pxData[offset:])
		}
		offset += rowBytes * f.CharHeight

		f.Glyphs = append(f.Glyphs, g)
	}

	return nil
}

// GlyphByCode finds a glyph by character code. Returns nil if not found.
func (f *Font) GlyphByCode(code uint16) *FontGlyph {
	for i := range f.Glyphs {
		if f.Glyphs[i].CharCode == code {
			return &f.Glyphs[i]
		}
	}
	return nil
}

// GlyphImage extracts a single glyph's palette indices as an alpha image.
func (f *Font) GlyphImage(charCode uint16) *image.Alpha {
	g := f.GlyphByCode(charCode)
	if g == nil || g.RowBytes == 0 {
		return nil
	}
	img := image.NewAlpha(image.Rect(0, 0, g.RowBytes, f.CharHeight))
	for y := 0; y < f.CharHeight; y++ {
		for x := 0; x < g.RowBytes; x++ {
			img.SetAlpha(x, y, color.Alpha{A: g.Pixels[y*g.RowBytes+x]})
		}
	}
	return img
}

// GlyphRect describes a glyph's position in a texture atlas.
type GlyphRect struct {
	X, Y, W, H int
}

// Atlas generates a texture atlas of all printable ASCII glyphs (32-127).
func (f *Font) Atlas() (*image.NRGBA, map[uint16]GlyphRect) {
	cols := 16
	maxW := 0
	for _, g := range f.Glyphs {
		if g.CharCode >= 32 && g.CharCode < 128 && g.RowBytes > maxW {
			maxW = g.RowBytes
		}
	}
	if maxW == 0 {
		maxW = 16
	}
	cellW := maxW + 1
	numPrintable := 0
	for _, g := range f.Glyphs {
		if g.CharCode >= 32 && g.CharCode < 128 {
			numPrintable++
		}
	}
	rows := (numPrintable + cols - 1) / cols
	atlasW := cols * cellW
	atlasH := rows * f.CharHeight

	img := image.NewNRGBA(image.Rect(0, 0, atlasW, atlasH))
	// Background stays (0,0,0,0) — Raylib handles alpha blending correctly.
	rects := make(map[uint16]GlyphRect)

	idx := 0
	for _, g := range f.Glyphs {
		if g.CharCode < 32 || g.CharCode >= 128 {
			continue
		}
		col := idx % cols
		row := idx / cols
		x0 := col * cellW
		y0 := row * f.CharHeight

		for y := 0; y < f.CharHeight; y++ {
			for x := 0; x < g.RowBytes; x++ {
				// The palette's own colour: ARIAL's 1 is an opaque grey
				// outline, not a half-transparent white.
				if v := int(g.Pixels[y*g.RowBytes+x]); v < len(f.Palette) {
					img.Set(x0+x, y0+y, f.Palette[v])
				}
			}
		}
		rects[g.CharCode] = GlyphRect{X: x0, Y: y0, W: g.RowBytes, H: f.CharHeight}
		idx++
	}

	return img, rects
}
