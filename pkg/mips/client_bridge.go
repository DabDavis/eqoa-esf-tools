package mips

// CLIENT module bridge: intercepts VIMemStream read functions
// and routes them through a Go packet buffer, capturing the
// exact read sequence for each opcode handler.
//
// This is the network-protocol equivalent of the ESF bridge.
// Instead of VIObjFile::Read*, we intercept VIMemStream read primitives.

// PacketStream provides packet data to the CLIENT opcode handlers.
type PacketStream struct {
	data []byte
	pos  int
	Reads []PacketRead // captured read trace
}

// PacketRead records one read from the packet stream.
type PacketRead struct {
	Type  string // "zigzag", "raw", "string", "blob", "int32", "float32"
	Pos   int    // byte offset in packet
	Size  int    // bytes consumed
	IVal  int64  // decoded integer value
	FVal  float32
	SVal  string // for string reads
}

// NewPacketStream creates a stream from raw packet bytes.
func NewPacketStream(data []byte) *PacketStream {
	return &PacketStream{data: data}
}

// CLIENT stream read function addresses (from decompilation)
var clientReadFuncs = map[uint32]string{
	0x00CB8610: "Zigzag",    // zigzag varint → int32
	0x00CB8520: "Raw",       // raw bytes (elemSize × count)
	0x00CB8740: "String",    // 4-byte length + UTF-8
	0x00CB87E8: "Blob",      // 4-byte length + UTF-16
}

// handleClientRead intercepts CLIENT stream reads and routes through PacketStream.
func (m *Interp) handleClientRead(target uint32, ps *PacketStream) bool {
	name, ok := clientReadFuncs[target]
	if !ok {
		return false
	}

	dest := uint32(m.rReg(5)) // $a1 = destination pointer

	switch name {
	case "Zigzag":
		// Read varint from stream, zigzag decode to int32
		val, n := readVarint(ps.data[ps.pos:])
		decoded := zigzagDecode(val)
		ps.Reads = append(ps.Reads, PacketRead{
			Type: "zigzag", Pos: ps.pos, Size: n, IVal: int64(decoded),
		})
		ps.pos += n
		if dest != 0 {
			m.store32(dest, uint32(decoded))
		}
		m.wReg32(2, 0) // success

	case "Raw":
		// raw(dest, elemSize, count) — read elemSize*count bytes
		elemSize := int(m.rReg(6)) // $a2
		count := int(m.rReg(7))    // $a3
		total := elemSize * count
		if ps.pos+total > len(ps.data) {
			total = len(ps.data) - ps.pos
		}
		// Capture value for small reads (4 bytes or less)
		var ival int64
		if total == 1 && ps.pos < len(ps.data) {
			ival = int64(ps.data[ps.pos])
		} else if total == 2 && ps.pos+2 <= len(ps.data) {
			ival = int64(int16(ps.data[ps.pos]) | int16(ps.data[ps.pos+1])<<8)
		} else if total == 4 && ps.pos+4 <= len(ps.data) {
			ival = int64(int32(ps.data[ps.pos]) | int32(ps.data[ps.pos+1])<<8 |
				int32(ps.data[ps.pos+2])<<16 | int32(ps.data[ps.pos+3])<<24)
		}
		ps.Reads = append(ps.Reads, PacketRead{
			Type: "raw", Pos: ps.pos, Size: total, IVal: ival,
		})
		// Copy bytes to dest
		for i := 0; i < total && ps.pos+i < len(ps.data); i++ {
			m.store8(dest+uint32(i), ps.data[ps.pos+i])
		}
		ps.pos += total
		m.wReg32(2, 0)

	case "String":
		// 4-byte length prefix + UTF-8 bytes
		if ps.pos+4 > len(ps.data) {
			m.wReg32(2, -1)
			break
		}
		slen := int(ps.data[ps.pos]) | int(ps.data[ps.pos+1])<<8 |
			int(ps.data[ps.pos+2])<<16 | int(ps.data[ps.pos+3])<<24
		ps.pos += 4
		maxLen := int(m.rReg(6)) // $a2 = max length
		if slen > maxLen {
			slen = maxLen
		}
		if ps.pos+slen > len(ps.data) {
			slen = len(ps.data) - ps.pos
		}
		str := string(ps.data[ps.pos : ps.pos+slen])
		ps.Reads = append(ps.Reads, PacketRead{
			Type: "string", Pos: ps.pos - 4, Size: 4 + slen, SVal: str,
		})
		// Copy to dest
		for i := 0; i < slen; i++ {
			m.store8(dest+uint32(i), ps.data[ps.pos+i])
		}
		m.store8(dest+uint32(slen), 0) // null terminate
		ps.pos += slen
		m.wReg32(2, 0)

	case "Blob":
		// 4-byte length prefix + UTF-16 chars
		if ps.pos+4 > len(ps.data) {
			m.wReg32(2, -1)
			break
		}
		charCount := int(ps.data[ps.pos]) | int(ps.data[ps.pos+1])<<8 |
			int(ps.data[ps.pos+2])<<16 | int(ps.data[ps.pos+3])<<24
		ps.pos += 4
		byteCount := charCount * 2 // UTF-16
		if ps.pos+byteCount > len(ps.data) {
			byteCount = len(ps.data) - ps.pos
		}
		ps.Reads = append(ps.Reads, PacketRead{
			Type: "blob", Pos: ps.pos - 4, Size: 4 + byteCount,
		})
		for i := 0; i < byteCount; i++ {
			m.store8(dest+uint32(i), ps.data[ps.pos+i])
		}
		ps.pos += byteCount
		m.wReg32(2, 0)
	}

	m.Intercepted++
	return true
}

// readVarint reads a 7-bit encoded unsigned integer.
func readVarint(data []byte) (uint32, int) {
	var val uint32
	var shift uint
	for i := 0; i < len(data) && i < 5; i++ {
		b := data[i]
		val |= uint32(b&0x7F) << shift
		shift += 7
		if b&0x80 == 0 {
			return val, i + 1
		}
	}
	return val, len(data)
}

// zigzagDecode converts a zigzag-encoded uint32 to int32.
func zigzagDecode(n uint32) int32 {
	return int32((n >> 1) ^ -(n & 1))
}

// RunOpcodeHandler runs a CLIENT opcode handler on packet data.
// Returns the handler's result and the captured read trace.
func RunOpcodeHandler(eeDump []byte, handlerAddr uint32, packetData []byte) (int32, []PacketRead) {
	interp := New(eeDump)
	ps := NewPacketStream(packetData)

	// Set up the VIClient/entity context
	// The mega-function handlers receive:
	//   $a0 = VIClient* (this)
	//   $a1 = stream* (VIMemStream)
	// The stream object has a pointer to the data buffer
	thisAddr := uint32(0x01FE0000)
	streamAddr := interp.heapAlloc(256)

	// Store packet data pointer in the stream struct
	// VIMemStream layout (from decompilation):
	//   +0x00: data pointer
	//   +0x04: current position
	//   +0x08: total size
	//   +0x0C: error flag
	packetBuf := interp.heapAlloc(uint32(len(packetData)) + 16)
	for i, b := range packetData {
		interp.store8(packetBuf+uint32(i), b)
	}
	interp.store32(streamAddr+0x00, packetBuf)
	interp.store32(streamAddr+0x04, 0)                       // position = 0
	interp.store32(streamAddr+0x08, uint32(len(packetData))) // size
	interp.store32(streamAddr+0x0C, 0)                       // no error

	// Zero the entity/client struct
	for i := uint32(0); i < 4096; i++ {
		interp.store8(thisAddr+i, 0)
	}

	// Store stream pointer where handlers expect it
	// (varies by handler — some use $a1, some use this+offset)

	// Enable client read interception
	interp.packetStream = ps

	// Run the handler
	result := interp.Run(handlerAddr, nil, thisAddr, streamAddr)

	return result, ps.Reads
}
