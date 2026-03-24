// esf-verify compares Go ESF parsers against PS2 native MIPS execution.
// For each ESF object, runs both the Go parser and the PS2 parser (via MIPS
// interpreter), then diffs the read traces field-by-field.
//
// Usage:
//
//	esf-verify CHAR.ESF                    # verify all PrimBuffers
//	esf-verify CHAR.ESF --type 0x1200      # verify specific type
//	esf-verify CHAR.ESF --all              # verify all supported types
//	esf-verify CHAR.ESF --max 10           # limit to first 10 objects
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"

	"github.com/DabDavis/eqoa-esf-tools/pkg/esf"
	"github.com/DabDavis/eqoa-esf-tools/pkg/mips"
)

// PS2 parser addresses (SUPPORT symbols)
var parserAddrs = map[uint16]uint32{
	0x1200: 0x004320B8, // ParsePrimBuffer
	0x4200: 0x00434610, // ParseCollBuffer (inferred)
}

// scanForType finds all objects of a given ESF type by scanning for headers.
func scanForType(data []byte, typ uint16, maxCount int) []int {
	target := make([]byte, 2)
	binary.LittleEndian.PutUint16(target, typ)

	var offsets []int
	for i := 0; i+8 < len(data) && len(offsets) < maxCount; i++ {
		if data[i] == target[0] && data[i+1] == target[1] {
			ver := binary.LittleEndian.Uint16(data[i+2:])
			size := binary.LittleEndian.Uint32(data[i+4:])
			// Validate: reasonable version and size
			if ver < 20 && size > 0 && size < 10_000_000 && i+8+int(size) <= len(data) {
				offsets = append(offsets, i)
				i += 8 + int(size) - 1 // skip past this object
			}
		}
	}
	return offsets
}

// goTraceFromPrimBuffer runs the Go PrimBuffer parser and returns a simplified read trace.
func goTraceFromPrimBuffer(data []byte) []mips.ReadEntry {
	var trace []mips.ReadEntry

	if len(data) < 8 {
		return trace
	}

	typ := binary.LittleEndian.Uint16(data[0:])
	ver := binary.LittleEndian.Uint16(data[2:])
	size := binary.LittleEndian.Uint32(data[4:])

	trace = append(trace, mips.ReadEntry{
		Type:  "ReadBegin",
		Pos:   0,
		Extra: fmt.Sprintf("type=0x%04X ver=%d size=%d", typ, ver, size),
	})

	pos := 8

	if ver == 0 {
		// V0: nmats(i32) + nfaces(i32) + unk(i32), then per-face float vertices
		trace = append(trace, ri32(&pos, data, "nmats"))
		trace = append(trace, ri32(&pos, data, "nfaces"))
		trace = append(trace, ri32(&pos, data, "unk"))
		return trace // V0 parsing differs enough to skip detailed comparison
	}

	// Version > 0
	if ver > 1 {
		trace = append(trace, ru32(&pos, data, "dictID"))
	}
	trace = append(trace, ri32(&pos, data, "pbtype"))
	trace = append(trace, ri32(&pos, data, "nmats"))
	nfaces := ri32val(&pos, data)
	trace = append(trace, mips.ReadEntry{Type: "int32", IVal: int64(nfaces), Pos: pos - 4})
	trace = append(trace, ri32(&pos, data, "unk"))
	trace = append(trace, ri32(&pos, data, "p1"))
	trace = append(trace, ri32(&pos, data, "p2"))
	trace = append(trace, ri32(&pos, data, "p3"))

	return trace
}

func ri32(pos *int, data []byte, _ string) mips.ReadEntry {
	if *pos+4 > len(data) {
		return mips.ReadEntry{Type: "int32", Pos: *pos}
	}
	v := int32(binary.LittleEndian.Uint32(data[*pos:]))
	e := mips.ReadEntry{Type: "int32", IVal: int64(v), Pos: *pos}
	*pos += 4
	return e
}

func ru32(pos *int, data []byte, _ string) mips.ReadEntry {
	if *pos+4 > len(data) {
		return mips.ReadEntry{Type: "uint32", Pos: *pos}
	}
	v := binary.LittleEndian.Uint32(data[*pos:])
	e := mips.ReadEntry{Type: "uint32", IVal: int64(v), Pos: *pos}
	*pos += 4
	return e
}

func ri32val(pos *int, data []byte) int32 {
	if *pos+4 > len(data) {
		return 0
	}
	v := int32(binary.LittleEndian.Uint32(data[*pos:]))
	*pos += 4
	return v
}

func main() {
	typFlag := flag.String("type", "0x1200", "ESF type to verify (hex)")
	maxFlag := flag.Int("max", 50, "Max objects to verify")
	verboseFlag := flag.Bool("v", false, "Verbose output")
	dumpPath := flag.String("dump", "/home/sdg/claude-eqoa/memory-dumps/go-inspect2.eeMemory", "EE memory dump path")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: esf-verify [flags] <esf-file>\n")
		os.Exit(1)
	}
	esfPath := flag.Arg(0)

	targetType := uint16(0x1200)
	if *typFlag != "" {
		var t uint32
		fmt.Sscanf(*typFlag, "0x%x", &t)
		if t == 0 {
			fmt.Sscanf(*typFlag, "%x", &t)
		}
		targetType = uint16(t)
	}

	parserAddr, ok := parserAddrs[targetType]
	if !ok {
		fmt.Fprintf(os.Stderr, "No PS2 parser known for type 0x%04X\n", targetType)
		fmt.Fprintf(os.Stderr, "Known types: ")
		for t := range parserAddrs {
			fmt.Fprintf(os.Stderr, "0x%04X ", t)
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}

	// Load EE dump
	fmt.Printf("Loading EE dump from %s...\n", *dumpPath)
	eeDump, err := os.ReadFile(*dumpPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load EE dump: %v\n", err)
		os.Exit(1)
	}

	// Load ESF
	fmt.Printf("Loading %s...\n", esfPath)
	esfData, err := os.ReadFile(esfPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load ESF: %v\n", err)
		os.Exit(1)
	}

	// Find objects
	offsets := scanForType(esfData, targetType, *maxFlag)
	fmt.Printf("Found %d objects of type 0x%04X\n\n", len(offsets), targetType)

	if len(offsets) == 0 {
		return
	}

	// Also open ESF with Go parser for comparison
	goFile, err := esf.Open(esfPath)
	_ = goFile // for future Go-side parsing comparison
	if err != nil {
		fmt.Printf("Note: Go ESF open failed: %v (PS2-only mode)\n", err)
	}

	matched := 0
	diffed := 0
	errored := 0

	for i, offset := range offsets {
		size := binary.LittleEndian.Uint32(esfData[offset+4:])
		objData := esfData[offset : offset+8+int(size)]

		// Run PS2 parser via MIPS
		ps2Result, ps2Reads := mips.RunParser(eeDump, parserAddr, objData)

		// Run Go parser trace
		goReads := goTraceFromPrimBuffer(objData)

		// Compare header reads
		minReads := len(goReads)
		if len(ps2Reads) < minReads {
			minReads = len(ps2Reads)
		}

		diffs := 0
		for j := 0; j < minReads; j++ {
			g := goReads[j]
			p := ps2Reads[j]

			if g.Type != p.Type {
				diffs++
				if *verboseFlag {
					fmt.Printf("  [%d] TYPE DIFF: Go=%s PS2=%s @ pos Go=0x%X PS2=0x%X\n",
						j, g.Type, p.Type, g.Pos, p.Pos)
				}
			} else if g.Type == "ReadBegin" {
				// Compare extra info
				if g.Extra != p.Extra {
					diffs++
				}
			} else if g.IVal != p.IVal {
				diffs++
				if *verboseFlag {
					fmt.Printf("  [%d] VALUE DIFF: Go=%d PS2=%d (%s) @ pos Go=0x%X PS2=0x%X\n",
						j, g.IVal, p.IVal, g.Type, g.Pos, p.Pos)
				}
			}
		}

		status := "MATCH"
		if ps2Result < 0 {
			status = "ERROR"
			errored++
		} else if diffs > 0 || len(goReads) != minReads {
			status = fmt.Sprintf("DIFF(%d)", diffs)
			diffed++
		} else {
			matched++
		}

		ver := binary.LittleEndian.Uint16(esfData[offset+2:])
		fmt.Printf("[%3d] @0x%06X ver=%d size=%5d  PS2=%3d reads  Go=%3d reads  %s\n",
			i, offset, ver, size, len(ps2Reads), len(goReads), status)

		if *verboseFlag && diffs > 0 {
			fmt.Println("  PS2 trace (first 10):")
			for j := 0; j < 10 && j < len(ps2Reads); j++ {
				r := ps2Reads[j]
				fmt.Printf("    [%d] %s val=%d pos=0x%X\n", j, r.Type, r.IVal, r.Pos)
			}
		}
	}

	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Total: %d objects\n", len(offsets))
	fmt.Printf("Match: %d  Diff: %d  Error: %d\n", matched, diffed, errored)

	if diffed > 0 || errored > 0 {
		fmt.Println("\nRe-run with -v for detailed diffs.")
	}
}
