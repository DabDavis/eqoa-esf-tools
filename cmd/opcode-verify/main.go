// opcode-verify runs PS2 CLIENT opcode handlers on real packet data
// and dumps the complete read trace + entity struct writes.
//
// Usage:
//   opcode-verify --handler 0x00BD1F10 --hex "82 65 cc 27 ..."
//   opcode-verify --handler 0x00BD1F10 --file packet.bin
//   opcode-verify --pcap capture.pcap --opcode 00B1
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/DabDavis/eqoa-esf-tools/pkg/mips"
)

func main() {
	handlerFlag := flag.String("handler", "", "Handler address (hex, e.g. 0x00BD1F10)")
	hexFlag := flag.String("hex", "", "Packet data as hex string")
	fileFlag := flag.String("file", "", "Packet data from binary file")
	dumpFlag := flag.String("dump", "/home/sdg/claude-eqoa/memory-dumps/go-inspect2.eeMemory", "EE memory dump")
	flag.Parse()

	if *handlerFlag == "" {
		fmt.Fprintf(os.Stderr, "Usage: opcode-verify --handler 0xADDR --hex \"bytes...\"\n")
		fmt.Fprintf(os.Stderr, "\nKnown handlers:\n")
		for _, h := range knownHandlers {
			fmt.Fprintf(os.Stderr, "  0x%08X  %s\n", h.addr, h.name)
		}
		os.Exit(1)
	}

	var handlerAddr uint32
	fmt.Sscanf(*handlerFlag, "0x%x", &handlerAddr)
	if handlerAddr == 0 {
		fmt.Sscanf(*handlerFlag, "%x", &handlerAddr)
	}

	var packetData []byte
	if *hexFlag != "" {
		clean := strings.ReplaceAll(*hexFlag, " ", "")
		var err error
		packetData, err = hex.DecodeString(clean)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Bad hex: %v\n", err)
			os.Exit(1)
		}
	} else if *fileFlag != "" {
		var err error
		packetData, err = os.ReadFile(*fileFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Read file: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Provide --hex or --file\n")
		os.Exit(1)
	}

	eeDump, err := os.ReadFile(*dumpFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "EE dump: %v\n", err)
		os.Exit(1)
	}

	// Find handler name
	handlerName := fmt.Sprintf("0x%08X", handlerAddr)
	for _, h := range knownHandlers {
		if h.addr == handlerAddr {
			handlerName = h.name
			break
		}
	}

	fmt.Printf("=== %s (0x%08X) ===\n", handlerName, handlerAddr)
	fmt.Printf("Packet: %d bytes\n", len(packetData))
	fmt.Printf("Hex: %s\n\n", hex.EncodeToString(packetData))

	result := mips.RunOpcodeHandlerFull(eeDump, handlerAddr, packetData)

	fmt.Printf("Result: %d (0x%08X)\n", result.Result, uint32(result.Result))
	fmt.Printf("Steps: %d\n", result.Steps)

	// Reads
	fmt.Printf("\n--- Reads (%d) ---\n", len(result.Reads))
	for i, r := range result.Reads {
		switch r.Type {
		case "zigzag":
			fmt.Printf("  [%d] zigzag  pos=%d size=%d → %d\n", i, r.Pos, r.Size, r.IVal)
		case "string":
			fmt.Printf("  [%d] string  pos=%d size=%d → %q\n", i, r.Pos, r.Size, r.SVal)
		case "raw":
			if r.Size <= 4 {
				fmt.Printf("  [%d] raw%d    pos=%d → %d (0x%X)\n", i, r.Size, r.Pos, r.IVal, uint32(r.IVal))
			} else {
				fmt.Printf("  [%d] raw     pos=%d size=%d\n", i, r.Pos, r.Size)
			}
		default:
			fmt.Printf("  [%d] %s  pos=%d size=%d ival=%d\n", i, r.Type, r.Pos, r.Size, r.IVal)
		}
	}

	// Writes
	if len(result.Writes) > 0 {
		fmt.Printf("\n--- Entity Writes (%d bytes) ---\n", len(result.Writes))
		// Group by 4-byte aligned offset
		type writeGroup struct {
			offset uint32
			bytes  map[uint32]byte
		}
		groups := map[uint32]*writeGroup{}
		for _, w := range result.Writes {
			aligned := w.Offset & ^uint32(3)
			g, ok := groups[aligned]
			if !ok {
				g = &writeGroup{offset: aligned, bytes: make(map[uint32]byte)}
				groups[aligned] = g
			}
			g.bytes[w.Offset] = byte(w.Value)
		}
		// Sort by offset
		var offsets []uint32
		for off := range groups {
			offsets = append(offsets, off)
		}
		sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })

		for _, off := range offsets {
			g := groups[off]
			var val uint32
			for byteOff, b := range g.bytes {
				shift := (byteOff - off) * 8
				val |= uint32(b) << shift
			}
			fmt.Printf("  entity+0x%04X = 0x%08X (%d)\n", off, val, int32(val))
		}
	}
}

type handlerInfo struct {
	addr uint32
	name string
}

var knownHandlers = []handlerInfo{
	{0x00BD1F10, "CastingCombat (0x00B1)"},
	{0x00BD1578, "MemoryDump (0x000D)"},
	{0x00BD271C, "UpdateTrainingPts (0x001D)"},
	{0x00BD263C, "PlayerTunar (0x0052)"},
	{0x00BD2690, "ConfirmBankTunar (0x1253)"},
	{0x00BD1B5C, "WorldEntry (0x002E)"},
	{0x00BD1E50, "ClientCloseLoot (0x0016)"},
	{0x00BD1590, "ColoredChat (0x0A7B)"},
	{0x00BD2A14, "AdjustItemHP (0x0060)"},
	{0x00BD2118, "EntityDataUpdate (0x00FD)"},
}
