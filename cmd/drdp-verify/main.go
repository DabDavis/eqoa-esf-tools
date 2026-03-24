// drdp-verify — verify Go DRDP implementation against PS2 native code.
//
// Runs the PS2 DRDP CRC function on packet data and compares with Go.
// Can also verify segment parsing and message decoding.
//
// Usage:
//   drdp-verify --crc --hex "0a 7e fe ff c8 e0 ..."
//   drdp-verify --crc --pcap capture.pcap
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"hash/crc32"
	"os"
	"strings"

	"github.com/DabDavis/eqoa-esf-tools/pkg/mips"
)

const DRDP_CRC_XOR = 0x11f19ed3

func main() {
	crcMode := flag.Bool("crc", false, "Verify CRC computation")
	hexFlag := flag.String("hex", "", "Packet data as hex")
	dumpFlag := flag.String("dump", "/home/sdg/claude-eqoa/memory-dumps/go-inspect2.eeMemory", "EE dump path")
	flag.Parse()

	if !*crcMode {
		fmt.Println("Usage: drdp-verify --crc --hex \"bytes...\"")
		fmt.Println("\nVerifies Go DRDP CRC against PS2 native drdp_crc function.")
		os.Exit(0)
	}

	eeDump, err := os.ReadFile(*dumpFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "EE dump: %v\n", err)
		os.Exit(1)
	}

	var packetData []byte
	if *hexFlag != "" {
		clean := strings.ReplaceAll(*hexFlag, " ", "")
		packetData, err = hex.DecodeString(clean)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Bad hex: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintln(os.Stderr, "Provide --hex")
		os.Exit(1)
	}

	fmt.Printf("Packet: %d bytes\n\n", len(packetData))

	// DRDP packet format: [body][CRC:4]
	// CRC covers all bytes except the last 4
	if len(packetData) < 8 {
		fmt.Println("Packet too short for CRC verification")
		os.Exit(1)
	}

	body := packetData[:len(packetData)-4]
	storedCRC := uint32(packetData[len(packetData)-4]) |
		uint32(packetData[len(packetData)-3])<<8 |
		uint32(packetData[len(packetData)-2])<<16 |
		uint32(packetData[len(packetData)-1])<<24

	// PS2 native CRC — full pipeline: drdp_crc(0, data, len) XOR drdp_crc_table_get(2)
	interp := mips.New(eeDump)
	bodyAddr := interp.HeapAllocExported(uint32(len(body)) + 16)
	for i, b := range body {
		interp.Store8At(bodyAddr+uint32(i), b)
	}
	ps2Raw := uint32(interp.RunCall(0x004B8ED8, 0, bodyAddr, uint32(len(body))))
	ps2Mask := uint32(interp.RunCall(0x004B9068, 2)) // protocol version 2
	ps2CRC := ps2Raw ^ ps2Mask

	// Go CRC32 — PS2 drdp_crc(0) == ChecksumIEEE.
	// PS2 pipeline: drdp_crc(0, data, len) XOR drdp_crc_table_get(2)
	//             = ChecksumIEEE XOR 0xEE0E612C
	// Note: DRDP_CRC_XOR (0x11F19ED3) = ~0xEE0E612C, from earlier RE.
	// The actual PS2 mask is 0xEE0E612C, not 0x11F19ED3.
	const PS2_CRC_MASK = 0xEE0E612C
	goCRC := crc32.ChecksumIEEE(body) ^ PS2_CRC_MASK

	fmt.Printf("=== CRC Verification ===\n")
	fmt.Printf("Body length:    %d bytes\n", len(body))
	fmt.Printf("Stored CRC:     0x%08X\n", storedCRC)
	fmt.Printf("PS2 CRC:        0x%08X (native drdp_crc)\n", ps2CRC)
	fmt.Printf("Go CRC:         0x%08X (crc32.IEEE ^ 0x%08X)\n", goCRC, DRDP_CRC_XOR)
	fmt.Printf("\n")
	fmt.Printf("PS2 == Stored:  %v\n", ps2CRC == storedCRC)
	fmt.Printf("Go  == Stored:  %v\n", goCRC == storedCRC)
	fmt.Printf("PS2 == Go:      %v\n", ps2CRC == goCRC)

	if ps2CRC == goCRC && goCRC == storedCRC {
		fmt.Println("\n✓ All CRCs match — Go implementation is PS2-accurate")
	} else if ps2CRC != goCRC {
		fmt.Println("\n✗ PS2 and Go CRCs differ — Go implementation has a bug")
	}

	// Also decode segment header fields
	fmt.Printf("\n=== Segment Header ===\n")
	if len(body) >= 4 {
		localEndpoint := uint16(body[0]) | uint16(body[1])<<8
		remoteEndpoint := uint16(body[2]) | uint16(body[3])<<8
		fmt.Printf("Local endpoint:  0x%04X\n", localEndpoint)
		fmt.Printf("Remote endpoint: 0x%04X\n", remoteEndpoint)
	}
}
