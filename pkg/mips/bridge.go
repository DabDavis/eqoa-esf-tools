package mips

import "math"

// ESF bridge: intercepts JAL calls to known VIObjFile Read* functions
// and routes them through the ESFStream instead of executing MIPS code.

// nativeSubParsers lists PS2 parser functions that should execute natively
// (not be auto-stubbed). These are ESF child parsers that call ReadBegin/
// ReadEnd and Read* to parse sub-objects. Adding a function here lets
// the MIPS interpreter execute it fully, capturing all its reads.
var nativeSubParsers = []uint32{
	// CSprite sub-parsers that should execute natively.
	// Critical: sub-parsers that call ReadBegin/ReadEnd MUST run natively
	// so the tree stream stays in sync. A stubbed sub-parser that skips
	// ReadEnd leaves the tree stream pointing at the wrong node.

	// Simple data readers (count + fields)
	0x00437B80, // ParseCSpritePlayList
	0x00437E30, // ParseCSpriteNodeIDList
	0x00437F18, // ParseCSpriteASlotList
	0x00437FF8, // ParseCSpriteTSlotList
	0x00438100, // ParseCSpriteContSound
	0x00437A78, // ParseCSpriteSkinList
	0x00436248, // ParseHSpriteTriggers
	0x004372D8, // ParseRefMap

	// ParseMaterialPal and its sub-parsers are NOT whitelisted because they
	// need VIRaster (GPU) functions to create surfaces/materials. Instead,
	// ParseMaterialPal is handled by a special stub that reads past its
	// ReadBegin/ReadEnd pair to keep the tree stream in sync.
}

// Known Read* function addresses (from SUPPORT symbol map)
var readFuncs = map[uint32]string{
	0x003EB0A8: "ReadBegin",
	0x003EB0C8: "ReadBegin2",
	0x003EB3C8: "ReadEnd",
	0x003EA350: "ObjectVersion",
	0x003EB6E8: "Read_Ri",
	0x003EB780: "Read_RUi",
	0x003EB948: "Read_Rf",
	0x003EB5B8: "Read_Rs",
	0x003EB550: "Read_RUc",
	0x003EB4E8: "Read_RSc",
	0x003EB480: "Read_Rc",
	0x003EB650: "Read_RUs",
	0x003EB818: "Read_Rl",
	0x003EBA78: "Read_PUci",
}

// Well-known math functions to intercept
var mathFuncs = map[uint32]string{
	// powf is critical for packing scale computation
}

// handleJAL intercepts JAL calls. Returns true if the call was handled
// (caller should skip to return address), false to execute normally.
func (m *Interp) handleJAL(target uint32) bool {
	// Call trace (PCSX2 DebugInterface style)
	if m.callTraceEnabled {
		name := ""
		if n, ok := readFuncs[target]; ok {
			name = n
		}
		m.callTrace = append(m.callTrace, CallEntry{
			Target: target, Caller: m.pc, Name: name,
		})
	}

	// Runtime object functions (VIDictionary, VIScene, VIRaster)
	if m.handleRuntime(target) {
		return true
	}

	if name, ok := readFuncs[target]; ok {
		m.handleRead(name)
		m.Intercepted++
		return true
	}

	// powf interception — PS2 uses this for packing scale
	// powf is called via jalr, not jal, but also check here
	// Symbol: powf at various addresses

	// powf — critical for packing scale: pow(2.0, exponent)
	if target == 0x00127328 {
		base := m.fregs[12] // $f12 = first float arg
		exp := m.fregs[13]  // $f13 = second float arg (from $f14 on PS2, but decompiler maps to 13)
		result := float32(math.Pow(float64(base), float64(exp)))
		m.fregs[0] = result // $f0 = return value
		m.Intercepted++
		return true
	}

	// ParseMaterialPal — skip by calling ReadBegin + ReadEnd to advance
	// the tree stream past the MaterialPalette child. Can't run natively
	// because it needs VIRaster to create surfaces/materials.
	if target == 0x00435240 {
		if m.Reader != nil {
			m.Reader.ReadBegin() // opens 0x1110 MaterialPalette
			m.Reader.ReadEnd()   // closes it without reading contents
		}
		m.wReg32(2, 0) // return 0 (success)
		m.Intercepted++
		return true
	}

	// Skip-stubs: sub-parsers that call ReadBegin internally.
	// When stubbed, they must still call ReadBegin + ReadEnd to keep
	// the tree stream's child index in sync.
	for _, skipAddr := range []uint32{
		0x00435240, // ParseMaterialPal
		0x0043A790, // ParseSoundArray
		0x004379D8, // ParseCSpriteSpriteArray
		0x004364C0, // ParseHSpriteAnimArray
		0x00435FA0, // ParseHSpriteHierarchy
	} {
		if target == skipAddr {
			if m.Reader != nil {
				m.Reader.ReadBegin() // open the child
				m.Reader.ReadEnd()   // close it (skip contents)
			}
			m.wReg32(2, 0)
			m.Intercepted++
			return true
		}
	}

	// VIScene runtime object creation — return fake pointers.
	// These are called by whitelisted sub-parsers (ParseRefMap, etc.)
	// and need non-null pointers for Init/Insert to write to.
	if target == 0x00463CE8 { // CreateRefMap
		m.wReg32(2, 0) // return index 0
		m.Intercepted++
		return true
	}
	if target == 0x00463D78 { // RefMap — return fake pointer
		m.wReg32(2, int64(0x01F50000))
		m.Intercepted++
		return true
	}

	// VIScene::Animation — MUST return fake pointer (auto-stub can't detect
	// the daddu $s4,$v0,$zero past a beq branch in the delay slot chain)
	if target == 0x00463C00 {
		m.wReg32(2, int64(0x01F70000))
		m.Intercepted++
		return true
	}

	// VIRaster::CreatePrimBuffer — return a valid fake index (0, success)
	if target == 0x00403220 {
		m.wReg32(2, 0) // return index 0
		m.Intercepted++
		return true
	}
	// VIRaster::PrimBuffer — return a fake PrimBuffer pointer (non-null)
	// The parser stores this in $s1 and uses it as base for Init/Lock/SetPacking/Vertex calls.
	if target == 0x004032A0 {
		m.wReg32(2, int64(0x01F80000)) // fake PrimBuffer at 0x01F80000
		m.Intercepted++
		return true
	}

	// VIDictionary::Find — return 0 (found) and write a plausible resource type.
	// PS2 parsers call: Find(dict, dictID, &resourceType, &index)
	// After Find, they check if the resource type matches the expected value.
	// $a2 = &resourceType (output), $a3 = &index (output).
	// We write type from the next instruction's comparison constant (9=CSprite,
	// 11=PrimBuffer, etc.) — but since we can't peek ahead, we write 0 which
	// works when dictID is 0 (parser skips Find entirely for dictID==0).
	// For non-zero dictIDs, the parser uses the Find result to decide whether
	// to create a new object or reuse an existing one.
	if target == 0x003E4318 {
		// VIDictionary::Find — always return 1 (not found).
		// During parsing, objects are being created for the first time.
		// The "not found" path leads to object creation which is what we want.
		//
		// After Find returns non-zero, parsers check the resource type:
		//   li $v0, N; if (type != N) → error
		// We write the expected type N to the output pointer.
		// Peek at return+8 (past the beq check delay slot) for "li $v0, N":
		a2 := uint32(m.rReg(6)) // &resourceType output
		ra := uint32(m.rReg(31))
		if a2 != 0 && ra > 0 && ra+8 < uint32(len(m.code)) {
			// The pattern after Find is:
			//   beq/bnez $v0 → branch (delay: lw $v1, ...)
			//   li $v0, N    ← the expected type
			liInsn := m.load32(ra + 4) // instruction after delay slot
			liOp := (liInsn >> 26) & 0x3F
			liRt := (liInsn >> 16) & 0x1F
			liImm := liInsn & 0xFFFF
			if liOp == 9 && liRt == 2 { // ADDIU $v0, $zero, N (li $v0, N)
				m.store16(a2, uint16(liImm)) // write expected type
			} else if liOp == 8 && liRt == 2 { // ADDI variant
				m.store16(a2, uint16(liImm))
			}
		}
		m.wReg32(2, 1) // return 1 = not found
		m.Intercepted++
		return true
	}

	// Whitelist: sub-parsers that should execute natively (NOT be stubbed).
	// These are ESF child parsers called by tree-navigating parsers like
	// ParseCSpriteObj. They call ReadBegin/ReadEnd and Read* functions
	// which are routed through our ESFTreeStream.
	for _, addr := range nativeSubParsers {
		if target == addr {
			return false // execute natively, don't stub
		}
	}

	// Auto-stub: ANY function in known code ranges.
	// Smart return: peek at the instruction after the return (the caller's
	// check) to decide what to return. Common patterns:
	//   bltz $v0 → caller checks $v0 < 0 for error → return 0 (success)
	//   beq $v0, $zero → caller checks $v0 == NULL → return fake pointer
	// Default: return 0 (success for most functions).
	//
	// For functions that return pointers used as base addresses (PrimBuffer,
	// Animation, etc.), specific intercepts above return fake pointers.
	// Everything else gets 0 which passes bltz/error checks.
	if (target >= 0x003E3D00 && target < 0x00559F14) ||
		(target >= 0x00100000 && target < 0x00170000) ||
		(target >= 0x00CC7DA8 && target < 0x00D546A4) {
		// Check if the caller's next instruction after return treats $v0 as a pointer.
		// If the return address loads from $v0 (lw $rX, offset($v0)), we need non-null.
		// Check the first 4 instructions after return to detect pointer usage.
		// PCSX2 doesn't need this — it executes everything natively. We need it
		// because stubbed functions must return plausible values.
		ra := uint32(m.rReg(31))
		if ra > 0 && ra+16 < uint32(len(m.code)) {
			for scan := uint32(0); scan < 16; scan += 4 {
				insn := m.load32(ra + scan)
				op := (insn >> 26) & 0x3F
				rs := (insn >> 21) & 0x1F
				funct := insn & 0x3F

				// lw/sw with $v0 as base → direct pointer dereference
				if (op == 35 || op == 43) && rs == 2 {
					m.wReg32(2, int64(0x01F60000))
					m.Intercepted++
					return true
				}
				// daddu/addu $rX, $v0, $zero → saving pointer to register
				if op == 0 && (funct == 45 || funct == 33) {
					srcRs := (insn >> 21) & 0x1F
					srcRt := (insn >> 16) & 0x1F
					if (srcRs == 2 && srcRt == 0) || (srcRs == 0 && srcRt == 2) {
						m.wReg32(2, int64(0x01F60000))
						m.Intercepted++
						return true
					}
				}
				// Stop scanning at unconditional jumps (but continue past conditional branches
				// since the pointer save might be in a delay slot)
				if op == 2 || op == 3 { // J, JAL only
					break
				}
			}
		}
		m.wReg32(2, 0) // default: return 0 (success)
		m.Intercepted++
		return true
	}

	return false
}

func (m *Interp) handleRead(name string) {
	r := m.Reader
	if r == nil {
		m.wReg32(2, -1)
		return
	}

	switch name {
	case "ReadBegin":
		typ, ver, _ := r.ReadBegin()
		a1 := uint32(m.rReg(5))
		a2 := uint32(m.rReg(6))
		m.store16(a1, typ)
		m.store16(a2, ver)
		if typ > 0 {
			m.wReg32(2, 0)
		} else {
			m.wReg32(2, -1)
		}

	case "ReadBegin2":
		typ, ver, size := r.ReadBegin()
		a1 := uint32(m.rReg(5))
		a2 := uint32(m.rReg(6))
		a3 := uint32(m.rReg(7))
		t0 := uint32(m.rReg(8))
		m.store16(a1, typ)
		m.store16(a2, ver)
		if a3 != 0 {
			m.store32(a3, size)
		}
		if t0 != 0 {
			m.store32(t0, 0)
		}
		if typ > 0 {
			m.wReg32(2, 0)
		} else {
			m.wReg32(2, -1)
		}

	case "ReadEnd":
		r.ReadEnd()
		m.wReg32(2, 0)

	case "ObjectVersion":
		m.wReg32(2, int64(r.ObjectVersion()))

	case "Read_Ri":
		a1 := uint32(m.rReg(5))
		v := r.ReadInt32()
		m.store32(a1, uint32(v))
		m.wReg32(2, 0)

	case "Read_RUi":
		a1 := uint32(m.rReg(5))
		v := r.ReadUint32()
		m.store32(a1, v)
		m.wReg32(2, 0)

	case "Read_Rf":
		a1 := uint32(m.rReg(5))
		v := r.ReadFloat32()
		m.storeFloat(a1, v)
		m.wReg32(2, 0)

	case "Read_Rs":
		a1 := uint32(m.rReg(5))
		v := r.ReadInt16()
		m.store16(a1, uint16(v))
		m.wReg32(2, 0)

	case "Read_RUc":
		a1 := uint32(m.rReg(5))
		v := r.ReadUint8()
		m.store8(a1, v)
		m.wReg32(2, 0)

	case "Read_RSc":
		a1 := uint32(m.rReg(5))
		v := r.ReadInt8()
		m.store8(a1, byte(v))
		m.wReg32(2, 0)

	case "Read_Rc":
		a1 := uint32(m.rReg(5))
		v := r.ReadUint8()
		m.store8(a1, v)
		m.wReg32(2, 0)

	case "Read_RUs":
		a1 := uint32(m.rReg(5))
		v := r.ReadInt16()
		m.store16(a1, uint16(v))
		m.wReg32(2, 0)

	case "Read_Rl":
		a1 := uint32(m.rReg(5))
		lo := r.ReadInt32()
		hi := r.ReadInt32()
		m.store32(a1, uint32(lo))
		m.store32(a1+4, uint32(hi))
		m.wReg32(2, 0)

	case "Read_PUci":
		// Bulk read: $a1=dest, $a2=count
		a1 := uint32(m.rReg(5))
		count := int(m.rReg(6) & 0xFFFFFFFF)
		data := r.ReadBytes(count)
		for i, b := range data {
			m.store8(a1+uint32(i), b)
		}
		m.wReg32(2, 0)

	default:
		m.wReg32(2, 0)
	}
}

// zeroFakePointers zeros memory at all fake pointer addresses so that
// reads from fake objects return 0 instead of stale EE dump data.
// Critical: the EE dump may have non-zero data at these addresses from
// the game's runtime state, causing incorrect branch decisions.
// ZeroFakePointersExported is the exported version of zeroFakePointers.
func ZeroFakePointersExported(interp *Interp) { zeroFakePointers(interp) }

func zeroFakePointers(interp *Interp) {
	// Each fake pointer region: 256 bytes should cover any struct fields
	for _, base := range []uint32{0x01F40000, 0x01F50000, 0x01F60000, 0x01F70000,
		0x01F80000, 0x01F90000, 0x01FA0000, 0x01FB0000, 0x01FC0000, 0x01FD0000} {
		for i := uint32(0); i < 256; i++ {
			interp.Store8At(base+i, 0)
		}
	}
}

// RunParserV0 runs a v0 sub-parser (ParsePrimBufferObjV0, etc.) on object data.
// The stream is pre-positioned past the 8-byte ESF header, and ReadBegin state
// is pre-populated, matching how the parent parser calls the v0 function.
func RunParserV0(eeDump []byte, parserAddr uint32, objData []byte) (int32, []ReadEntry) {
	interp := New(eeDump)
	esf := NewESFStream(objData)
	esf.ReadBegin()
	zeroFakePointers(interp)
	thisAddr := uint32(0x01FE0000)
	for i := uint32(0); i < 512; i++ {
		interp.Store8At(thisAddr+i, 0)
	}
	interp.Store32At(thisAddr+0x0C, 0x01FB0000)
	interp.Store32At(thisAddr+0x18, 0x01FA0000)
	interp.Store32At(thisAddr+0x20, 0x01F90000)
	interp.Store32At(thisAddr+0x24, 0x01FD0000)

	interp.Reader = esf // set Reader for handleRead routing
	result := interp.Run(parserAddr, nil, thisAddr) // nil ESF, Reader already set
	return result, esf.Reads
}

// RunParser sets up a VIESFParse context and runs a parser function.
// Returns the read trace from the ESF stream.
func RunParser(eeDump []byte, parserAddr uint32, objData []byte) (int32, []ReadEntry) {
	interp := New(eeDump)
	esf := NewESFStream(objData)
	zeroFakePointers(interp)
	thisAddr := uint32(0x01FE0000)
	for i := uint32(0); i < 512; i++ {
		interp.Store8At(thisAddr+i, 0)
	}
	// Note: thisAddr+0x04 is a callback pointer, NOT VIScene. Must be NULL
	// to skip the jalr dispatch in BeginMaterial/EndMaterial material change callbacks.
	interp.Store32At(thisAddr+0x0C, 0x01FB0000) // VIRaster* (non-null — CreatePrimBuffer needs this)
	interp.Store32At(thisAddr+0x18, 0x01FA0000) // VIParticleSystem* (non-null stub)
	interp.Store32At(thisAddr+0x20, 0x01F90000) // VIDictionary* (non-null stub)
	interp.Store32At(thisAddr+0x24, 0x01FD0000) // VIObjFile* (non-null, Read* intercepted)

	result := interp.Run(parserAddr, esf, thisAddr)
	return result, esf.Reads
}
