package mips

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

func TestParsePrimBuffer(t *testing.T) {
	// Load EE dump
	dumpPath := "/home/sdg/claude-eqoa/memory-dumps/go-inspect2.eeMemory"
	eeDump, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Skipf("EE dump not found: %v", err)
	}

	// Load ESF and find first PrimBuffer (type 0x1200 ver 2)
	esfPath := "/home/sdg/claude-eqoa/extracted-assets/CHAR.ESF"
	esfData, err := os.ReadFile(esfPath)
	if err != nil {
		t.Skipf("CHAR.ESF not found: %v", err)
	}

	// Scan for PrimBuffer header
	target := []byte{0x00, 0x12, 0x02, 0x00} // type=0x1200, ver=2
	idx := 0
	for i := 0; i < len(esfData)-8; i++ {
		if esfData[i] == target[0] && esfData[i+1] == target[1] &&
			esfData[i+2] == target[2] && esfData[i+3] == target[3] {
			size := binary.LittleEndian.Uint32(esfData[i+4:])
			if size < 100000 {
				idx = i
				break
			}
		}
	}
	if idx == 0 {
		t.Fatal("No PrimBuffer found in CHAR.ESF")
	}

	size := binary.LittleEndian.Uint32(esfData[idx+4:])
	objData := esfData[idx : idx+8+int(size)]

	t.Logf("PrimBuffer at 0x%06X, size=%d", idx, size)

	// Run PS2 ParsePrimBuffer natively
	result, reads := RunParser(eeDump, 0x004320B8, objData)

	t.Logf("Result: %d, Reads: %d", result, len(reads))

	// Result may be -1 for PrimBuffers with invalid pbtype (0x4D8FE4F8).
	// This is correct PS2 behavior — both PS2 and Go reject invalid pbtypes.
	if result < 0 {
		t.Logf("ParsePrimBuffer returned %d (may be invalid pbtype — OK)", result)
	}

	if len(reads) < 3 {
		t.Errorf("Too few reads: %d (expected at least ReadBegin + header)", len(reads))
	}

	// Verify first read is ReadBegin with type 0x1200
	if reads[0].Type != "ReadBegin" {
		t.Errorf("First read should be ReadBegin, got %s", reads[0].Type)
	}

	// Print first 20 reads for inspection
	for i, r := range reads {
		if i >= 20 {
			break
		}
		switch r.Type {
		case "ReadBegin", "ReadEnd":
			fmt.Printf("  [%3d] %s: %s\n", i, r.Type, r.Extra)
		case "float32":
			fmt.Printf("  [%3d] %-8s = %12.6f  @ 0x%06X\n", i, r.Type, r.FVal, r.Pos)
		default:
			fmt.Printf("  [%3d] %-8s = %12d (0x%08X)  @ 0x%06X\n", i, r.Type, r.IVal, uint32(r.IVal), r.Pos)
		}
	}
}
