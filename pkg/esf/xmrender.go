package esf

import (
	"encoding/binary"
	"math"
)

// XmModule holds parsed XM module data ready for rendering.
type XmModule struct {
	SongLength     int
	RestartPos     int
	NumChannels    int
	NumPatterns    int
	NumInstruments int
	Flags          int
	Tempo          int // ticks per row
	BPM            int
	PatternOrder   [256]byte
	Patterns       []XmPattern
	Samples        [][]int16 // decoded PCM samples per instrument region
}

// XmPattern holds one pattern's note data.
type XmPattern struct {
	Rows [16][16]XmNote // [row][channel], max 16 channels
}

// XmNote is a single note slot.
type XmNote struct {
	Type   byte // 0=empty, 1=noteOff, 2=cmd, 3=note
	Note   byte
	Param1 byte
	Param2 byte
}

// ParseXmModule parses the raw pattern+sample data from an Xm object into a playable module.
func ParseXmModule(xm *Xm) *XmModule {
	if xm == nil || len(xm.PatternData) < 284 {
		return nil
	}

	d := xm.PatternData
	m := &XmModule{}

	// Header (offsets relative to pattern data, after DictID+vol+pan were stripped)
	// But PatternData starts at headerBytes offset, after the 12-byte ESF header
	// Actually Xm.PatternData is read from hdr.Offset+headerBytes, so it starts
	// at the first byte after DictID+vol+pan = the XM module body

	m.SongLength = int(binary.LittleEndian.Uint16(d[4:]))
	m.RestartPos = int(binary.LittleEndian.Uint16(d[6:]))
	m.NumChannels = int(binary.LittleEndian.Uint16(d[8:]))
	m.NumPatterns = int(binary.LittleEndian.Uint16(d[10:]))
	m.NumInstruments = int(binary.LittleEndian.Uint16(d[12:]))
	m.Flags = int(binary.LittleEndian.Uint16(d[14:]))
	m.Tempo = int(binary.LittleEndian.Uint16(d[16:]))
	m.BPM = int(binary.LittleEndian.Uint16(d[18:]))

	if m.Tempo <= 0 {
		m.Tempo = 6
	}
	if m.BPM <= 0 {
		m.BPM = 125
	}
	if m.NumChannels <= 0 || m.NumChannels > 16 {
		return nil
	}

	// Pattern order table at offset 28
	copy(m.PatternOrder[:], d[28:28+256])

	// Parse patterns: each is 16 rows × numChannels × 4-byte slots
	patStart := 284
	m.Patterns = make([]XmPattern, m.NumPatterns)
	for pi := 0; pi < m.NumPatterns; pi++ {
		for row := 0; row < 16; row++ {
			for ch := 0; ch < m.NumChannels; ch++ {
				off := patStart + (pi*16*m.NumChannels+row*m.NumChannels+ch)*4
				if off+4 > len(d) {
					continue
				}
				b0, b1, b2, b3 := d[off], d[off+1], d[off+2], d[off+3]

				var n XmNote
				if b0 == 0x00 && b1 == 0xCD && b2 == 0xCD && b3 == 0xCD {
					n.Type = 0 // empty
				} else if b0 == 0xCD && b1 == 0xCD && b2 == 0xCD && b3 == 0xCD {
					n.Type = 0 // filler
				} else if b0 == 0x00 && b1 == 0x00 && b2 == 0x00 && b3 == 0x00 {
					n.Type = 1 // note off
				} else if b0 == 0x40 && b1 == 0x00 {
					n.Type = 2 // cmd: volume + instrument
					n.Param1 = b2 // volume
					n.Param2 = b3 // instrument
				} else if b2 == 0x00 && b3 == 0x00 {
					n.Type = 3 // note trigger
					n.Note = b0
					n.Param1 = b1 // timing offset
				} else {
					n.Type = 0 // unknown, treat as empty
				}
				m.Patterns[pi].Rows[row][ch] = n
			}
		}
	}

	// Decode VAG samples from the Xm's SampleData
	if len(xm.SampleData) > 0 {
		m.Samples = decodeVAGSamples(xm.SampleData)
	}

	return m
}

// decodeVAGSamples splits VAG ADPCM data at end markers and decodes each to PCM.
func decodeVAGSamples(data []byte) [][]int16 {
	// Find sample boundaries by scanning for end-of-sample flags
	var boundaries []int
	boundaries = append(boundaries, 0)
	for i := 0; i < len(data)-16; i += 16 {
		flags := data[i+1]
		if flags == 1 || flags == 7 {
			boundaries = append(boundaries, i+16)
		}
	}

	var samples [][]int16
	for i := 0; i < len(boundaries)-1; i++ {
		start := boundaries[i]
		end := boundaries[i+1]
		if end-start < 32 { // skip tiny samples
			samples = append(samples, nil)
			continue
		}
		pcm := decodeVAGBlock(data[start:end])
		samples = append(samples, pcm)
	}
	return samples
}

// decodeVAGBlock decodes a single VAG ADPCM sample to signed 16-bit PCM.
func decodeVAGBlock(data []byte) []int16 {
	filters := [5][2]float64{
		{0, 0},
		{60.0 / 64.0, 0},
		{115.0 / 64.0, -52.0 / 64.0},
		{98.0 / 64.0, -55.0 / 64.0},
		{122.0 / 64.0, -60.0 / 64.0},
	}

	var samples []int16
	var s1, s2 float64

	for i := 0; i < len(data); i += 16 {
		if i+16 > len(data) {
			break
		}
		shift := int(data[i] & 0x0F)
		predict := int((data[i] >> 4) & 0x0F)
		flags := data[i+1]

		if predict > 4 {
			predict = 0
		}
		if shift > 12 {
			shift = 12
		}
		f0, f1 := filters[predict][0], filters[predict][1]

		for j := 2; j < 16; j++ {
			for _, nibble := range []int{int(data[i+j] & 0x0F), int((data[i+j] >> 4) & 0x0F)} {
				if nibble > 7 {
					nibble -= 16
				}
				sample := float64(nibble) * float64(int(1)<<(12-shift))
				sample += s1*f0 + s2*f1
				s2 = s1
				s1 = sample
				clamped := int16(math.Max(-32768, math.Min(32767, sample)))
				samples = append(samples, clamped)
			}
		}

		if flags == 1 || flags == 7 {
			break
		}
	}
	return samples
}

// RenderXmToWAV renders an XM module to stereo 16-bit PCM at the given sample rate.
// Returns interleaved stereo samples (L, R, L, R, ...).
func RenderXmToWAV(m *XmModule, sampleRate int) []int16 {
	if m == nil || len(m.Patterns) == 0 || len(m.Samples) == 0 {
		return nil
	}

	// Timing: tickDuration = 2500ms / BPM (at standard speed)
	// Each row = Tempo ticks
	tickDur := 2500.0 / float64(m.BPM)                         // ms per tick
	rowDur := tickDur * float64(m.Tempo)                        // ms per row
	samplesPerRow := int(rowDur * float64(sampleRate) / 1000.0) // PCM samples per row

	// Total song length
	totalRows := m.SongLength * 16 // songLength patterns × 16 rows each
	totalSamples := totalRows * samplesPerRow

	// Output buffer (stereo)
	out := make([]int16, totalSamples*2)

	// Channel state
	type chanState struct {
		sampleIdx int     // which sample is playing (-1 = none)
		pos       int     // position within sample
		volume    float64 // 0-1
		playing   bool
	}
	channels := make([]chanState, m.NumChannels)
	for i := range channels {
		channels[i].sampleIdx = -1
	}

	// Process each row
	outPos := 0
	for orderIdx := 0; orderIdx < m.SongLength; orderIdx++ {
		patIdx := int(m.PatternOrder[orderIdx])
		if patIdx >= len(m.Patterns) {
			patIdx = 0
		}
		pat := &m.Patterns[patIdx]

		for row := 0; row < 16; row++ {
			// Process note events for this row
			for ch := 0; ch < m.NumChannels; ch++ {
				n := pat.Rows[row][ch]
				switch n.Type {
				case 1: // note off
					channels[ch].playing = false
				case 2: // cmd: set volume + instrument
					channels[ch].volume = float64(n.Param1) / 64.0
					channels[ch].sampleIdx = int(n.Param2)
					channels[ch].pos = 0
					channels[ch].playing = true
				case 3: // note trigger
					// Note triggers a sample with pitch shift
					// For now, just restart the current sample
					if channels[ch].sampleIdx >= 0 {
						channels[ch].pos = 0
						channels[ch].playing = true
					}
				}
			}

			// Mix samples for this row
			for s := 0; s < samplesPerRow; s++ {
				var mixL, mixR float64
				for ch := 0; ch < m.NumChannels; ch++ {
					cs := &channels[ch]
					if !cs.playing || cs.sampleIdx < 0 || cs.sampleIdx >= len(m.Samples) {
						continue
					}
					samp := m.Samples[cs.sampleIdx]
					if samp == nil || cs.pos >= len(samp) {
						cs.playing = false
						continue
					}
					val := float64(samp[cs.pos]) * cs.volume
					// Simple stereo: alternate channels L/R
					if ch%2 == 0 {
						mixL += val
					} else {
						mixR += val
					}
					cs.pos++
				}

				idx := (outPos + s) * 2
				if idx+1 < len(out) {
					l := int16(math.Max(-32768, math.Min(32767, mixL)))
					r := int16(math.Max(-32768, math.Min(32767, mixR)))
					out[idx] = l
					out[idx+1] = r
				}
			}
			outPos += samplesPerRow
		}
	}

	return out
}
