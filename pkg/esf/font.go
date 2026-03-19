package esf

import (
	"image"
	"image/color"
)

// Font represents a PS2 bitmap font from a CSF/ESF file.
// PS2: ParseFontObj (0x0043A830), type 0x7000.
// Children: 0x7010 (header), 0x7020 (char widths), 0x7030 (pixel data).
type Font struct {
	info       *ObjInfo
	DictID     uint32
	NumChars   int
	CharHeight int
	BitDepth   int // VIBitDepth enum (1=4bpp from PS2, but data appears 8bpp)
	Widths     []byte   // per-char pixel width (1 byte each)
	PixelData  []byte   // raw glyph bitmaps (sequential, 8-bit alpha per pixel)
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

	// Char widths (0x7020) — 1 byte per character
	ct := f.info.Child(0x7020)
	if ct != nil {
		f.Widths = file.RawBytes(int(ct.Offset), int(ct.Size))
	}

	// Pixel data (0x7030) — 8-bit alpha per pixel, row-major
	px := f.info.Child(0x7030)
	if px != nil {
		f.PixelData = file.RawBytes(int(px.Offset), int(px.Size))
	}

	return nil
}

// GlyphImage extracts a single glyph as an alpha image.
// Returns nil if the character is out of range or has zero width.
func (f *Font) GlyphImage(charCode int) *image.Alpha {
	if charCode < 0 || charCode >= f.NumChars || charCode >= len(f.Widths) {
		return nil
	}
	w := int(f.Widths[charCode])
	if w == 0 {
		return nil
	}

	// Compute pixel offset by summing widths of all preceding chars
	offset := 0
	for i := 0; i < charCode; i++ {
		offset += int(f.Widths[i]) * f.CharHeight
	}

	img := image.NewAlpha(image.Rect(0, 0, w, f.CharHeight))
	for y := 0; y < f.CharHeight; y++ {
		for x := 0; x < w; x++ {
			pos := offset + y*w + x
			if pos < len(f.PixelData) {
				img.SetAlpha(x, y, color.Alpha{A: f.PixelData[pos]})
			}
		}
	}
	return img
}

// Atlas generates a texture atlas of all printable ASCII glyphs (32-127).
// Returns the atlas image and per-char UV rects for rendering.
type GlyphRect struct {
	X, Y, W, H int
}

func (f *Font) Atlas() (*image.NRGBA, map[int]GlyphRect) {
	cols := 16
	maxW := 0
	for i := 32; i < 128 && i < len(f.Widths); i++ {
		w := int(f.Widths[i])
		if w > maxW {
			maxW = w
		}
	}
	startChar := 32
	endChar := 128
	if endChar > f.NumChars {
		endChar = f.NumChars
	}
	numToRender := endChar - startChar
	rows := (numToRender + cols - 1) / cols
	cellW := maxW + 1
	atlasW := cols * cellW
	atlasH := rows * f.CharHeight

	img := image.NewNRGBA(image.Rect(0, 0, atlasW, atlasH))
	rects := make(map[int]GlyphRect)

	offset := 0
	for ch := 0; ch < f.NumChars && ch < len(f.Widths); ch++ {
		w := int(f.Widths[ch])
		if w == 0 {
			continue
		}
		if ch >= startChar && ch < endChar {
			idx := ch - startChar
			col := idx % cols
			row := idx / cols
			x0 := col * cellW
			y0 := row * f.CharHeight

			for y := 0; y < f.CharHeight; y++ {
				for x := 0; x < w; x++ {
					pos := offset + y*w + x
					if pos < len(f.PixelData) {
						a := f.PixelData[pos]
						if a > 0 {
							img.Set(x0+x, y0+y, color.NRGBA{255, 255, 255, a})
						}
					}
				}
			}
			rects[ch] = GlyphRect{X: x0, Y: y0, W: w, H: f.CharHeight}
		}
		offset += w * f.CharHeight
	}

	return img, rects
}
