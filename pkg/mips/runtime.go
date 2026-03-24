package mips

// PS2 runtime object implementations for the MIPS interpreter.
// These replace the per-function stubs with actual data structures
// that PS2 code can operate on natively.

// FakeDictionary implements VIDictionary Find/Add operations.
// PS2 parsers use this to register and look up parsed objects by DictID.
type FakeDictionary struct {
	entries map[uint32]dictEntry
}

type dictEntry struct {
	resourceType uint16
	index        int32
}

// Find looks up a DictID. Returns (found bool, type, index).
func (d *FakeDictionary) Find(dictID uint32) (bool, uint16, int32) {
	if d.entries == nil {
		return false, 0, -1
	}
	if e, ok := d.entries[dictID]; ok {
		return true, e.resourceType, e.index
	}
	return false, 0, -1
}

// Add registers a DictID with a type and index.
func (d *FakeDictionary) Add(dictID uint32, resourceType uint16, index int32) {
	if d.entries == nil {
		d.entries = make(map[uint32]dictEntry)
	}
	d.entries[dictID] = dictEntry{resourceType, index}
}

// FakeScene implements VIScene Create*/Get* operations.
// Allocates incrementing indices for animations, refmaps, etc.
type FakeScene struct {
	nextAnim   int32
	nextRefMap int32
	nextSprite int32
}

func (s *FakeScene) CreateAnimation() int32 { s.nextAnim++; return s.nextAnim - 1 }
func (s *FakeScene) CreateRefMap() int32    { s.nextRefMap++; return s.nextRefMap - 1 }
func (s *FakeScene) CreateSprite() int32    { s.nextSprite++; return s.nextSprite - 1 }

// FakeRaster implements VIRaster Create*/Get* operations.
type FakeRaster struct {
	nextPrimBuf  int32
	nextSurface  int32
	nextMatPal   int32
	nextColorBuf int32
}

func (r *FakeRaster) CreatePrimBuffer() int32 { r.nextPrimBuf++; return r.nextPrimBuf - 1 }
func (r *FakeRaster) CreateSurface() int32    { r.nextSurface++; return r.nextSurface - 1 }
func (r *FakeRaster) CreateMatPal() int32     { r.nextMatPal++; return r.nextMatPal - 1 }
func (r *FakeRaster) CreateColorBuf() int32   { r.nextColorBuf++; return r.nextColorBuf - 1 }

// runtimeState holds all fake PS2 runtime objects.
type runtimeState struct {
	dict   FakeDictionary
	scene  FakeScene
	raster FakeRaster
}

// Known PS2 runtime function addresses and their Go handlers.
// These functions are intercepted by handleJAL and routed to Go.
var runtimeFuncs = map[uint32]string{
	// VIDictionary
	0x003E4318: "Dictionary_Find",
	0x003E42D8: "Dictionary_Add",

	// VIScene object creation (return indices)
	0x00463B90: "Scene_CreateAnimation",
	0x00463CE8: "Scene_CreateRefMap",

	// VIScene object access (return fake pointers from heap)
	0x00463C00: "Scene_Animation",
	0x00463D78: "Scene_RefMap",
	0x00463C78: "Scene_ShareAnimation",

	// VIRaster object creation
	0x00403220: "Raster_CreatePrimBuffer",
	0x004032A0: "Raster_PrimBuffer",
}

// handleRuntime processes a PS2 runtime function call.
// Returns true if the function was handled, false if not recognized.
func (m *Interp) handleRuntime(target uint32) bool {
	name, ok := runtimeFuncs[target]
	if !ok {
		return false
	}

	switch name {
	case "Dictionary_Find":
		// Find(dict, dictID, &resourceType, &index)
		// During parsing, objects haven't been registered yet → return "not found"
		dictID := uint32(m.rReg(5))
		_ = dictID

		// During ESF parsing, Find should ALWAYS return 0 (found = "proceed
		// with parsing"). The "not found" path (return non-zero) is for
		// duplicate detection — "this DictID already exists, skip creation."
		// Since we're parsing fresh, nothing exists yet → always "found"
		// means "first time seeing this, create it."
		//
		// PS2 behavior: Find returns 0 → beq taken → goto create path.
		m.wReg32(2, 0) // return 0 = proceed with creation

	case "Dictionary_Add":
		// Add(dict, resource, dictID, resourceType, index)
		dictID := uint32(m.rReg(6))
		resType := uint16(m.rReg(7))
		idx := int32(m.rReg(8))
		m.runtime.dict.Add(dictID, resType, idx)
		m.wReg32(2, 0)

	case "Scene_CreateAnimation":
		idx := m.runtime.scene.CreateAnimation()
		m.wReg32(2, int64(idx))

	case "Scene_CreateRefMap":
		idx := m.runtime.scene.CreateRefMap()
		m.wReg32(2, int64(idx))

	case "Scene_Animation":
		// Return a heap-allocated fake object pointer
		addr := m.heapAlloc(256) // enough for VIHSpriteAnim struct
		m.wReg32(2, int64(addr))

	case "Scene_RefMap":
		addr := m.heapAlloc(256)
		m.wReg32(2, int64(addr))

	case "Scene_ShareAnimation":
		m.wReg32(2, 0) // no-op (reference counting)

	case "Raster_CreatePrimBuffer":
		idx := m.runtime.raster.CreatePrimBuffer()
		m.wReg32(2, int64(idx))

	case "Raster_PrimBuffer":
		addr := m.heapAlloc(512) // enough for VIPrimBuffer struct
		m.wReg32(2, int64(addr))

	default:
		return false
	}

	m.Intercepted++
	return true
}

// peekExpectedType reads the type constant from the caller's comparison
// instruction after Find returns. Pattern: beq/bne → li $v0, N → compare.
func (m *Interp) peekExpectedType() uint16 {
	ra := uint32(m.rReg(31))
	if ra == 0 || ra+12 >= uint32(len(m.code)) {
		return 0
	}
	// Scan ra+4 through ra+12 for "li $v0, N" (ADDIU $v0, $zero, N)
	// Pattern after Find: beq(delay) → lw(delay slot) → li $v0, N
	for off := uint32(4); off <= 12; off += 4 {
		insn := m.load32(ra + off)
		op := (insn >> 26) & 0x3F
		rs := (insn >> 21) & 0x1F
		rt := (insn >> 16) & 0x1F
		imm := insn & 0xFFFF
		// ADDIU $v0, $zero, N or LI $v0, N
		if op == 9 && rs == 0 && rt == 2 {
			return uint16(imm)
		}
		// ADDI variant
		if op == 8 && rs == 0 && rt == 2 {
			return uint16(imm)
		}
	}
	return 0
}
