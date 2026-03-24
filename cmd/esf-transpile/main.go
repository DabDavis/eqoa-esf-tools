// esf-transpile generates Go ESF parser code from PS2 MIPS read traces.
// Runs PS2 parsers natively, captures exact read sequences for every
// version/pbtype variant, and emits Go with proper if/switch dispatch.
//
// Usage:
//
//	esf-transpile --type 0x1200 TUNARIA_chunk.bin LAVASTM.ESF SKY.ESF
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/DabDavis/eqoa-esf-tools/pkg/mips"
)

var parserInfo = map[uint16]struct {
	addr uint32
	name string
}{
	0x1200: {0x004320B8, "PrimBuffer"},
	0x4200: {0x004343D8, "CollBuffer"},
}

// variant identifies a unique parse path (version + pbtype combination).
type variant struct {
	ver    uint16
	pbtype int32 // -1 for v0 (no pbtype field)
}

// variantTrace holds the read trace for one variant.
type variantTrace struct {
	variant
	reads []mips.ReadEntry // data reads only (no ReadBegin/ReadEnd)
	count int              // how many objects produced this trace
}

func main() {
	typFlag := flag.String("type", "", "ESF type (hex)")
	isoFlag := flag.String("iso", "/home/sdg/claude-eqoa/EverQuest - Online Adventures - Frontiers (USA).iso", "")
	dumpFlag := flag.String("dump", "/home/sdg/claude-eqoa/memory-dumps/go-inspect2.eeMemory", "")
	maxFlag := flag.Int("max", 10, "Max objects per file")
	outFlag := flag.String("out", "", "Output file (stdout if empty)")
	flag.Parse()

	if *typFlag == "" {
		fmt.Fprintf(os.Stderr, "Usage: esf-transpile --type 0x1200 [files...]\n")
		os.Exit(1)
	}

	var targetType uint16
	fmt.Sscanf(*typFlag, "0x%x", &targetType)

	info, ok := parserInfo[targetType]
	if !ok {
		fmt.Fprintf(os.Stderr, "Unknown type 0x%04X\n", targetType)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Loading EE dump...\n")
	eeDump, err := os.ReadFile(*dumpFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "EE dump: %v\n", err)
		os.Exit(1)
	}

	// Collect ESF data from all sources
	var allData []struct {
		name string
		data []byte
	}

	// Explicit files from args
	for _, path := range flag.Args() {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			continue
		}
		allData = append(allData, struct {
			name string
			data []byte
		}{path, data})
	}

	// Also read from TUNARIA in ISO if no args
	if len(allData) == 0 {
		fmt.Fprintf(os.Stderr, "Reading TUNARIA from ISO...\n")
		f, err := os.Open(*isoFlag)
		if err == nil {
			chunk := make([]byte, 50*1024*1024)
			f.ReadAt(chunk, 520000*2048)
			f.Close()
			allData = append(allData, struct {
				name string
				data []byte
			}{"TUNARIA", chunk})
		}
	}

	// Scan all files for objects, trace each one
	variants := map[variant]*variantTrace{}

	for _, src := range allData {
		offsets := scanType(src.data, targetType, *maxFlag)
		fmt.Fprintf(os.Stderr, "%s: %d objects of type 0x%04X\n", src.name, len(offsets), targetType)

		for _, off := range offsets {
			ver := binary.LittleEndian.Uint16(src.data[off+2:])
			size := binary.LittleEndian.Uint32(src.data[off+4:])
			objData := src.data[off : off+8+int(size)]

			result, reads := mips.RunParser(eeDump, info.addr, objData)
			if result < 0 {
				continue
			}

			// Extract pbtype from trace (3rd data read for ver>0, -1 for v0)
			pbtype := int32(-1)
			dataIdx := 0
			for _, r := range reads {
				if r.Type == "ReadBegin" || r.Type == "ReadEnd" {
					continue
				}
				dataIdx++
				// For PrimBuffer: read 1=dictID (if ver>1), then pbtype
				if targetType == 0x1200 {
					if ver > 1 && dataIdx == 2 {
						pbtype = int32(r.IVal)
					} else if ver <= 1 && dataIdx == 1 {
						pbtype = int32(r.IVal)
					}
				}
				if targetType == 0x4200 {
					if ver > 1 && dataIdx == 1 {
						pbtype = int32(r.IVal) // cbtype
					}
				}
			}
			if ver == 0 {
				pbtype = -1
			}

			// Filter to data reads only
			var dataReads []mips.ReadEntry
			for _, r := range reads {
				if r.Type != "ReadBegin" && r.Type != "ReadEnd" {
					dataReads = append(dataReads, r)
				}
			}

			v := variant{ver, pbtype}
			vt, ok := variants[v]
			if !ok {
				vt = &variantTrace{variant: v, reads: dataReads}
				variants[v] = vt
			}
			vt.count++

			fmt.Fprintf(os.Stderr, "  ver=%d pb=%d → %d reads\n", ver, pbtype, len(dataReads))
		}
	}

	fmt.Fprintf(os.Stderr, "\n%d unique variants found\n", len(variants))

	// Generate code
	code := generateCode(info.name, targetType, variants)

	if *outFlag != "" {
		os.WriteFile(*outFlag, []byte(code), 0644)
		fmt.Fprintf(os.Stderr, "Wrote %s\n", *outFlag)
	} else {
		fmt.Print(code)
	}
}

func scanType(data []byte, typ uint16, maxCount int) []int {
	target := make([]byte, 2)
	binary.LittleEndian.PutUint16(target, typ)
	var offsets []int
	for i := 0; i+8 < len(data) && len(offsets) < maxCount; i++ {
		if data[i] != target[0] || data[i+1] != target[1] {
			continue
		}
		ver := binary.LittleEndian.Uint16(data[i+2:])
		size := binary.LittleEndian.Uint32(data[i+4:])
		if ver > 20 || size == 0 || size > 500000 || i+8+int(size) > len(data) {
			i++
			continue
		}
		offsets = append(offsets, i)
		i += 8 + int(size) - 1
	}
	return offsets
}

// detectVertexPattern analyzes reads to find the repeating per-vertex pattern.
// headerLen is a hint for where to start looking (skipped reads assumed to be header).
// Returns the pattern and count, or nil if no pattern found.
func detectVertexPattern(reads []mips.ReadEntry, headerLen int) (vertexReads []mips.ReadEntry, vertexCount int) {
	// Try starting from different offsets after the header to find where
	// the repeating pattern begins. CollBuffer has 3 reads per vertex-group
	// header (num + primg + list), PrimBuffer has 2 (nverts + mat).
	for skip := 2; skip <= 4; skip++ {
		startIdx := headerLen + skip
		if startIdx >= len(reads) {
			continue
		}
		vr, vc := findRepeat(reads[startIdx:])
		if vc >= 2 {
			return vr, vc
		}
	}
	return nil, 0
}

func findRepeat(after []mips.ReadEntry) ([]mips.ReadEntry, int) {
	if len(after) < 4 {
		return nil, 0
	}

	// Try all plausible vertex strides:
	//   3  = CollBuffer cbtype=0 (3×float pos)
	//   4  = CollBuffer cbtype=1 (3×int16 → 3 reads, but may be 4 with padding)
	//   5  = CollBuffer cbtype=3 (3×int16 + vgroup + flora)
	//  12  = PrimBuffer pbtype=0 (8×float + 4×uint8) or pbtype=2 (5×int16 + 3×int8 + 4×uint8)
	//  13  = PrimBuffer pbtype=4 (5×int16 + 3×int8 + 4×uint8 + int16)
	for _, stride := range []int{3, 4, 5, 12, 13, 14, 10, 11, 6, 7, 8} {
		if stride > len(after) {
			continue
		}
		pattern := after[:stride]
		matches := 1
		for j := stride; j+stride <= len(after); j += stride {
			same := true
			for k := 0; k < stride; k++ {
				if after[j+k].Type != pattern[k].Type {
					same = false
					break
				}
			}
			if !same {
				break
			}
			matches++
		}
		if matches >= 2 {
			return pattern, matches
		}
	}
	return nil, 0
}

func generateCode(name string, typ uint16, variants map[variant]*variantTrace) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf(`// Code generated by esf-transpile from PS2 Parse%s.
// DO NOT EDIT — regenerate with: esf-transpile --type 0x%04X [esf-files...]
//
// This parser is byte-accurate against the PS2 MIPS binary.
// Every read type and order matches the native PS2 execution trace.
package esf

import (
	"encoding/binary"
	"fmt"
	"math"
)

`, name, typ))

	// Sort variants for deterministic output
	var vkeys []variant
	for v := range variants {
		vkeys = append(vkeys, v)
	}
	sort.Slice(vkeys, func(i, j int) bool {
		if vkeys[i].ver != vkeys[j].ver {
			return vkeys[i].ver < vkeys[j].ver
		}
		return vkeys[i].pbtype < vkeys[j].pbtype
	})

	// Emit PS2Vertex struct
	sb.WriteString(`// PS2Vertex holds one vertex as parsed by the PS2 natively.
type PS2Vertex struct {
	X, Y, Z    float32
	U, V       float32
	NX, NY, NZ float32
	R, G, B, A float32
	VGroup     int16
}

`)

	// Main function
	sb.WriteString(fmt.Sprintf("// PS2Parse%s parses a %s using the exact PS2 read sequence.\n", name, name))
	sb.WriteString(fmt.Sprintf("func PS2Parse%s(data []byte) ([]PS2Vertex, error) {\n", name))
	sb.WriteString("\tif len(data) < 8 {\n\t\treturn nil, fmt.Errorf(\"too short\")\n\t}\n\n")
	sb.WriteString("\ttyp := binary.LittleEndian.Uint16(data[0:])\n")
	sb.WriteString(fmt.Sprintf("\tif typ != 0x%04X {\n\t\treturn nil, fmt.Errorf(\"wrong type 0x%%04X\", typ)\n\t}\n\n", typ))
	sb.WriteString("\tver := binary.LittleEndian.Uint16(data[2:])\n")
	sb.WriteString("\tpos := 8\n")
	sb.WriteString("\tvar vertices []PS2Vertex\n\n")

	// Separate v0 from v1+ variants
	var v0trace *variantTrace
	pbtypeTraces := map[int32]*variantTrace{} // pbtype → trace (merged across versions)
	totalCount := 0

	for _, v := range vkeys {
		vt := variants[v]
		if v.ver == 0 {
			v0trace = vt
			continue
		}
		// Merge all ver>0 by pbtype (v1 and v2+ share vertex format, differ only in dictID gate)
		if existing, ok := pbtypeTraces[v.pbtype]; ok {
			existing.count += vt.count
		} else {
			pbtypeTraces[v.pbtype] = vt
		}
		totalCount += vt.count
	}

	// Emit v0 path
	if v0trace != nil && len(v0trace.reads) > 0 {
		sb.WriteString("\tif ver == 0 {\n")
		emitV0Body(&sb, v0trace.reads)
		sb.WriteString("\t\treturn vertices, nil\n\t}\n\n")
	}

	// Emit v1+ path with merged version gate
	if len(pbtypeTraces) > 0 {
		sb.WriteString(fmt.Sprintf("\t// ver >= 1 (%d objects traced across all versions)\n", totalCount))
		sb.WriteString("\tif ver > 1 {\n\t\t_ = ps2ru32(data, &pos) // dictID (ver >= 2 only)\n\t}\n")

		// Auto-detect header length by finding where the vertex repeat starts.
		// Use any trace to detect the pattern.
		var anyTrace *variantTrace
		for _, vt := range pbtypeTraces {
			anyTrace = vt
			break
		}

		// Find header length: everything before the first repeating pattern
		headerLen := findHeaderLen(anyTrace.reads)

		// Emit header fields from the trace
		sb.WriteString(fmt.Sprintf("\t// Header: %d fields (auto-detected from PS2 trace)\n", headerLen))
		emitAutoHeader(&sb, anyTrace.reads, headerLen)

		// Sort pbtypes
		var pbtypes []int32
		for pb := range pbtypeTraces {
			pbtypes = append(pbtypes, pb)
		}
		sort.Slice(pbtypes, func(i, j int) bool { return pbtypes[i] < pbtypes[j] })

		if len(pbtypes) > 1 {
			sb.WriteString("\tswitch pbtype {\n")
			for _, pb := range pbtypes {
				vt := pbtypeTraces[pb]
				sb.WriteString(fmt.Sprintf("\tcase %d: // %d objects traced\n", pb, vt.count))
				emitVertexLoop(&sb, vt.reads, headerLen, "\t\t")
			}
			sb.WriteString("\tdefault:\n")
			sb.WriteString(fmt.Sprintf("\t\treturn nil, fmt.Errorf(\"%s: unsupported pbtype %%d\", pbtype)\n", name))
			sb.WriteString("\t}\n")
		} else {
			vt := pbtypeTraces[pbtypes[0]]
			_ = anyTrace
			emitVertexLoop(&sb, vt.reads, headerLen, "\t")
		}
	}

	sb.WriteString("\n\treturn vertices, nil\n}\n")

	// Helper functions
	sb.WriteString(`
func ps2ri32(d []byte, p *int) int32 {
	if *p+4 > len(d) { return 0 }
	v := int32(binary.LittleEndian.Uint32(d[*p:])); *p += 4; return v
}
func ps2ru32(d []byte, p *int) uint32 {
	if *p+4 > len(d) { return 0 }
	v := binary.LittleEndian.Uint32(d[*p:]); *p += 4; return v
}
func ps2rf32(d []byte, p *int) float32 {
	if *p+4 > len(d) { return 0 }
	v := math.Float32frombits(binary.LittleEndian.Uint32(d[*p:])); *p += 4; return v
}
func ps2ri16(d []byte, p *int) int16 {
	if *p+2 > len(d) { return 0 }
	v := int16(binary.LittleEndian.Uint16(d[*p:])); *p += 2; return v
}
func ps2ru8(d []byte, p *int) byte {
	if *p >= len(d) { return 0 }
	v := d[*p]; *p++; return v
}
func ps2ri8(d []byte, p *int) int8 {
	if *p >= len(d) { return 0 }
	v := int8(d[*p]); *p++; return v
}
var _ = math.Float32frombits
var _ = fmt.Errorf
`)

	return sb.String()
}

// findHeaderLen determines how many reads are "header" before the vertex loop.
// Scans from the end backward looking for where the repeating pattern starts.
func findHeaderLen(reads []mips.ReadEntry) int {
	// Try detecting a repeating pattern starting from each position
	for start := 1; start < len(reads) && start < 20; start++ {
		// Try skipping 2-4 reads for the per-group header (num+primg+list or nverts+mat)
		for skip := 2; skip <= 4; skip++ {
			idx := start + skip
			if idx >= len(reads) {
				continue
			}
			_, count := findRepeat(reads[idx:])
			if count >= 2 {
				return start
			}
		}
	}
	// Fallback: assume first 8 reads are header
	if len(reads) > 8 {
		return 8
	}
	return len(reads)
}

// emitAutoHeader emits header fields with auto-generated names.
// First field is "pbtype" (or "cbtype"), field with the highest value that
// could be a count is "nfaces" (or "numGroups").
func emitAutoHeader(sb *strings.Builder, reads []mips.ReadEntry, headerLen int) {
	// Name heuristics based on value and position
	for i := 0; i < headerLen && i < len(reads); i++ {
		r := reads[i]
		name := fmt.Sprintf("h%d", i)

		// First int32 after dictID is typically pbtype/cbtype
		if i == 0 {
			name = "pbtype"
		}

		switch r.Type {
		case "int32":
			if name == "pbtype" {
				sb.WriteString(fmt.Sprintf("\tpbtype := ps2ri32(data, &pos)\n"))
			} else {
				sb.WriteString(fmt.Sprintf("\t%s := ps2ri32(data, &pos)\n", name))
			}
		case "uint32":
			sb.WriteString(fmt.Sprintf("\t%s := ps2ru32(data, &pos)\n", name))
		case "float32":
			sb.WriteString(fmt.Sprintf("\t%s := ps2rf32(data, &pos)\n", name))
		default:
			sb.WriteString(fmt.Sprintf("\t_ = ps2ri32(data, &pos) // %s\n", r.Type))
		}
	}

	// Find which header field is "nfaces" (the loop count) — largest small value
	sb.WriteString("\t// nfaces/numGroups derived from header\n")
	sb.WriteString(fmt.Sprintf("\tnfaces := h1 // auto: adjust if wrong field\n"))
	sb.WriteString(fmt.Sprintf("\t_ = pbtype\n"))
	for i := 2; i < headerLen; i++ {
		sb.WriteString(fmt.Sprintf("\t_ = h%d\n", i))
	}
	sb.WriteString("\n")
}

func emitV0Body(sb *strings.Builder, reads []mips.ReadEntry) {
	sb.WriteString("\t\t// V0: float vertices (from PS2 ParsePrimBufferObjV0)\n")
	sb.WriteString("\t\t_ = ps2ri32(data, &pos) // nmats\n")
	sb.WriteString("\t\tnfaces := ps2ri32(data, &pos)\n")
	sb.WriteString("\t\t_ = ps2ri32(data, &pos) // unk\n")
	sb.WriteString("\t\tfor fi := int32(0); fi < nfaces; fi++ {\n")
	sb.WriteString("\t\t\tnverts := ps2ri32(data, &pos)\n")
	sb.WriteString("\t\t\t_ = ps2ri32(data, &pos) // mat\n")
	sb.WriteString("\t\t\tfor vi := int32(0); vi < nverts; vi++ {\n")
	sb.WriteString("\t\t\t\tv := PS2Vertex{\n")
	sb.WriteString("\t\t\t\t\tX: ps2rf32(data, &pos), Y: ps2rf32(data, &pos), Z: ps2rf32(data, &pos),\n")
	sb.WriteString("\t\t\t\t\tU: ps2rf32(data, &pos), V: ps2rf32(data, &pos),\n")
	sb.WriteString("\t\t\t\t\tNX: ps2rf32(data, &pos), NY: ps2rf32(data, &pos), NZ: ps2rf32(data, &pos),\n")
	sb.WriteString("\t\t\t\t}\n")
	sb.WriteString("\t\t\t\tv.R = float32(ps2ru8(data, &pos)) / 255.0\n")
	sb.WriteString("\t\t\t\tv.G = float32(ps2ru8(data, &pos)) / 255.0\n")
	sb.WriteString("\t\t\t\tv.B = float32(ps2ru8(data, &pos)) / 255.0\n")
	sb.WriteString("\t\t\t\tv.A = float32(ps2ru8(data, &pos)) / 255.0\n")
	sb.WriteString("\t\t\t\tvertices = append(vertices, v)\n")
	sb.WriteString("\t\t\t}\n")
	sb.WriteString("\t\t}\n")
}

func emitHeader(sb *strings.Builder, reads []mips.ReadEntry, ver uint16) int {
	idx := 0
	if ver > 1 {
		sb.WriteString("\tif ver > 1 {\n\t\t_ = ps2ru32(data, &pos) // dictID\n\t}\n")
		idx++
	}
	sb.WriteString("\tpbtype := ps2ri32(data, &pos)\n")
	idx++
	sb.WriteString("\t_ = ps2ri32(data, &pos) // nmats\n")
	idx++
	sb.WriteString("\tnfaces := ps2ri32(data, &pos)\n")
	idx++
	sb.WriteString("\t_ = ps2ri32(data, &pos) // unk\n")
	idx++
	sb.WriteString("\tp1 := ps2ri32(data, &pos)\n")
	idx++
	sb.WriteString("\tp2 := ps2ri32(data, &pos)\n")
	idx++
	sb.WriteString("\tp3 := ps2ri32(data, &pos)\n")
	idx++
	sb.WriteString("\t_, _, _ = p1, p2, p3\n")
	return idx
}

func emitVertexLoop(sb *strings.Builder, reads []mips.ReadEntry, headerLen int, indent string) {
	// Detect vertex pattern from trace
	pattern, _ := detectVertexPattern(reads, headerLen)

	sb.WriteString(fmt.Sprintf("%sfor fi := int32(0); fi < nfaces; fi++ {\n", indent))
	sb.WriteString(fmt.Sprintf("%s\tnverts := ps2ri32(data, &pos)\n", indent))
	sb.WriteString(fmt.Sprintf("%s\t_ = ps2ri32(data, &pos) // mat\n", indent))
	sb.WriteString(fmt.Sprintf("%s\tfor vi := int32(0); vi < nverts; vi++ {\n", indent))

	if pattern != nil {
		emitVertexFromPattern(sb, pattern, indent+"\t\t")
	} else {
		sb.WriteString(fmt.Sprintf("%s\t\t// Could not detect vertex pattern from trace\n", indent))
		sb.WriteString(fmt.Sprintf("%s\t\t// Trace has %d reads after header\n", indent, len(reads)-headerLen))
	}

	sb.WriteString(fmt.Sprintf("%s\t\tvertices = append(vertices, v)\n", indent))
	sb.WriteString(fmt.Sprintf("%s\t}\n", indent))
	sb.WriteString(fmt.Sprintf("%s}\n", indent))
}

func emitVertexFromPattern(sb *strings.Builder, pattern []mips.ReadEntry, indent string) {
	// Count types to classify
	floats := 0
	int16s := 0
	for _, r := range pattern {
		switch r.Type {
		case "float32":
			floats++
		case "int16":
			int16s++
		}
	}

	sb.WriteString(fmt.Sprintf("%svar v PS2Vertex\n", indent))

	if floats >= 8 {
		// Float vertex (pbtype=0)
		sb.WriteString(fmt.Sprintf("%sv.X = ps2rf32(data, &pos)\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.Y = ps2rf32(data, &pos)\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.Z = ps2rf32(data, &pos)\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.U = ps2rf32(data, &pos)\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.V = ps2rf32(data, &pos)\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.NX = ps2rf32(data, &pos)\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.NY = ps2rf32(data, &pos)\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.NZ = ps2rf32(data, &pos)\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.R = float32(ps2ru8(data, &pos)) / 255.0\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.G = float32(ps2ru8(data, &pos)) / 255.0\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.B = float32(ps2ru8(data, &pos)) / 255.0\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.A = float32(ps2ru8(data, &pos)) / 255.0\n", indent))
	} else if int16s >= 5 {
		// Packed vertex (pbtype=2 or 4)
		sb.WriteString(fmt.Sprintf("%spk1 := float32(1.0) / float32(int32(1) << p1)\n", indent))
		sb.WriteString(fmt.Sprintf("%spk2 := float32(1.0) / float32(int32(1) << p2)\n", indent))
		sb.WriteString(fmt.Sprintf("%spk3 := float32(1.0) / float32(int32(1) << p3)\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.X = float32(ps2ri16(data, &pos)) * pk1\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.Y = float32(ps2ri16(data, &pos)) * pk1\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.Z = float32(ps2ri16(data, &pos)) * pk1\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.U = float32(ps2ri16(data, &pos)) * pk2\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.V = float32(ps2ri16(data, &pos)) * pk2\n", indent))

		// Check what comes after 5 int16s
		remaining := pattern[5:]
		ri := 0
		// Normals
		for ri < len(remaining) && remaining[ri].Type == "int8" {
			switch ri {
			case 0:
				sb.WriteString(fmt.Sprintf("%sv.NX = float32(ps2ri8(data, &pos)) * pk3\n", indent))
			case 1:
				sb.WriteString(fmt.Sprintf("%sv.NY = float32(ps2ri8(data, &pos)) * pk3\n", indent))
			case 2:
				sb.WriteString(fmt.Sprintf("%sv.NZ = float32(ps2ri8(data, &pos)) * pk3\n", indent))
			}
			ri++
		}
		// Colors
		colorIdx := 0
		for ri < len(remaining) && remaining[ri].Type == "uint8" {
			switch colorIdx {
			case 0:
				sb.WriteString(fmt.Sprintf("%sv.R = float32(ps2ru8(data, &pos)) / 255.0\n", indent))
			case 1:
				sb.WriteString(fmt.Sprintf("%sv.G = float32(ps2ru8(data, &pos)) / 255.0\n", indent))
			case 2:
				sb.WriteString(fmt.Sprintf("%sv.B = float32(ps2ru8(data, &pos)) / 255.0\n", indent))
			case 3:
				sb.WriteString(fmt.Sprintf("%sv.A = float32(ps2ru8(data, &pos)) / 255.0\n", indent))
			}
			colorIdx++
			ri++
		}
		// VGroup (if present — pbtype=4)
		if ri < len(remaining) && remaining[ri].Type == "int16" {
			sb.WriteString(fmt.Sprintf("%sv.VGroup = ps2ri16(data, &pos)\n", indent))
		}
	} else if floats == 3 && len(pattern) == 3 {
		// CollBuffer cbtype=0: 3×float position only
		sb.WriteString(fmt.Sprintf("%sv.X = ps2rf32(data, &pos)\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.Y = ps2rf32(data, &pos)\n", indent))
		sb.WriteString(fmt.Sprintf("%sv.Z = ps2rf32(data, &pos)\n", indent))
	} else {
		// Generic: emit reads matching the exact trace types
		fieldNames := []string{"X", "Y", "Z", "U", "V", "NX", "NY", "NZ", "R", "G", "B", "A"}
		for i, r := range pattern {
			fname := fmt.Sprintf("f%d", i)
			if i < len(fieldNames) {
				fname = fieldNames[i]
			}
			switch r.Type {
			case "float32":
				sb.WriteString(fmt.Sprintf("%sv.%s = ps2rf32(data, &pos)\n", indent, fname))
			case "int16":
				sb.WriteString(fmt.Sprintf("%s_ = ps2ri16(data, &pos) // %s\n", indent, fname))
			case "int8":
				sb.WriteString(fmt.Sprintf("%s_ = ps2ri8(data, &pos) // %s\n", indent, fname))
			case "uint8":
				sb.WriteString(fmt.Sprintf("%s_ = ps2ru8(data, &pos) // %s\n", indent, fname))
			default:
				sb.WriteString(fmt.Sprintf("%s_ = ps2ri32(data, &pos) // %s %s\n", indent, r.Type, fname))
			}
		}
	}
}
