package esf

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
)

// ObjInfo represents a node in the ESF object tree.
type ObjInfo struct {
	Type          uint16
	Version       int16
	Size          int32
	NumSubObjects int32
	Offset        int // byte offset of body start in file
	DictID        int32
	Children      []*ObjInfo
	Parent        *ObjInfo
}

func (o *ObjInfo) Child(typ uint16) *ObjInfo {
	for _, c := range o.Children {
		if c.Type == typ {
			return c
		}
	}
	return nil
}

func (o *ObjInfo) ChildrenOfType(typ uint16) []*ObjInfo {
	var out []*ObjInfo
	for _, c := range o.Children {
		if typ == 0 || c.Type == typ {
			out = append(out, c)
		}
	}
	return out
}

func (o *ObjInfo) ParentOfType(typ uint16) *ObjInfo {
	p := o.Parent
	for p != nil {
		if typ == 0 || p.Type == typ {
			return p
		}
		p = p.Parent
	}
	return nil
}

func (o *ObjInfo) NextSibling() *ObjInfo {
	if o.Parent == nil {
		return nil
	}
	for i, c := range o.Parent.Children {
		if c == o && i+1 < len(o.Parent.Children) {
			return o.Parent.Children[i+1]
		}
	}
	return nil
}

// Available returns unread bytes remaining in this object's body.
func (o *ObjInfo) Available(filePos int) int {
	return int(o.Size) - (filePos - o.Offset)
}

func (o *ObjInfo) String() string {
	return fmt.Sprintf("%s(0x%04x) offset=0x%x size=0x%x subs=%d dict=0x%08x",
		TypeName(o.Type), o.Type, o.Offset, o.Size, o.NumSubObjects, o.DictID)
}

// ObjFile is the main ESF file parser.
type ObjFile struct {
	data     []byte
	pos      int
	root     *ObjInfo
	objects  []*ObjInfo
	dict     map[int32]*ObjInfo
	objCache map[int]Object

	Debug bool
}

// Object is implemented by all parsed ESF objects.
type Object interface {
	Load(file *ObjFile) error
	ObjInfo() *ObjInfo
}

// Open parses an ESF file from disk.
func Open(path string) (*ObjFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return OpenBytes(data)
}

// OpenBytes parses an ESF file from a byte slice.
func OpenBytes(data []byte) (*ObjFile, error) {
	f := &ObjFile{
		data:     data,
		objCache: make(map[int]Object),
	}
	if err := f.readFileHeader(); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *ObjFile) readFileHeader() error {
	if len(f.data) < 32 {
		return fmt.Errorf("file too small for header")
	}
	// Magic is stored reversed
	magic := string([]byte{f.data[3], f.data[2], f.data[1], f.data[0]})
	if magic != "OBJF" {
		return fmt.Errorf("missing OBJF magic, got %q", magic)
	}
	f.pos = 4
	_ = f.readInt32()  // numObjects
	_ = f.readInt32()  // fileType
	_ = f.readInt32()  // unknown
	offset := f.readInt64() // data offset
	_ = f.readInt64()  // unknown2
	f.pos = int(offset)
	return nil
}

// Root returns the root ObjInfo, parsing the tree if needed.
func (f *ObjFile) Root() (*ObjInfo, error) {
	if f.root == nil {
		if err := f.parse(); err != nil {
			return nil, err
		}
	}
	return f.root, nil
}

func (f *ObjFile) parse() error {
	// Read the first top-level object.
	root, err := f.readObject(nil)
	if err != nil {
		return err
	}
	f.root = root

	// Some ESF files (e.g. addart.esf) have multiple top-level objects
	// at the same level. If there's more data, keep reading siblings
	// and attach them as children of a synthetic root.
	var siblings []*ObjInfo
	for f.pos+12 <= len(f.data) {
		// Peek at the next type — if 0 or invalid, stop.
		nextType := binary.LittleEndian.Uint16(f.data[f.pos:])
		if nextType == 0 {
			break
		}
		sib, err := f.readObject(nil)
		if err != nil {
			break // non-fatal: stop reading siblings
		}
		siblings = append(siblings, sib)
	}

	if len(siblings) > 0 {
		// Wrap everything under a synthetic root so the tree stays navigable.
		synth := &ObjInfo{
			Type:          TypeRoot,
			NumSubObjects: int32(1 + len(siblings)),
			Children:      append([]*ObjInfo{root}, siblings...),
		}
		for _, c := range synth.Children {
			c.Parent = synth
		}
		f.root = synth
	}

	if f.Debug {
		log.Printf("parsed %d objects", len(f.objects))
	}
	return nil
}

func (f *ObjFile) readObject(parent *ObjInfo) (*ObjInfo, error) {
	info := &ObjInfo{Parent: parent}

	if f.pos+12 > len(f.data) {
		return nil, fmt.Errorf("unexpected EOF reading object header at pos %d", f.pos)
	}

	info.Type = binary.LittleEndian.Uint16(f.data[f.pos:])
	info.Version = int16(binary.LittleEndian.Uint16(f.data[f.pos+2:]))
	info.Size = int32(binary.LittleEndian.Uint32(f.data[f.pos+4:]))
	info.NumSubObjects = int32(binary.LittleEndian.Uint32(f.data[f.pos+8:]))
	f.pos += 12
	info.Offset = f.pos

	if parent != nil {
		parent.Children = append(parent.Children, info)
	}

	// Read dict ID if this type has one
	if typeHasDictID(info.Type) && info.Size >= 4 {
		info.DictID = int32(binary.LittleEndian.Uint32(f.data[f.pos:]))
	}

	f.objects = append(f.objects, info)

	// Recursively read sub-objects
	for i := int32(0); i < info.NumSubObjects; i++ {
		if _, err := f.readObject(info); err != nil {
			return nil, fmt.Errorf("reading sub-object %d of %s: %w", i, info, err)
		}
	}

	// Skip any unread body bytes
	end := info.Offset + int(info.Size)
	if f.pos < end {
		f.pos = end
	}

	return info, nil
}

// BuildDictionary builds the global ID→ObjInfo lookup table.
func (f *ObjFile) BuildDictionary() error {
	if _, err := f.Root(); err != nil {
		return err
	}
	f.dict = make(map[int32]*ObjInfo)
	for _, o := range f.objects {
		if o.DictID != 0 {
			if _, exists := f.dict[o.DictID]; !exists {
				f.dict[o.DictID] = o
			}
		}
	}
	if f.Debug {
		log.Printf("built dictionary with %d entries", len(f.dict))
	}
	return nil
}

// DictLen returns the number of entries in the dictionary.
func (f *ObjFile) DictLen() int {
	return len(f.dict)
}

// DictKeys returns all dictionary IDs (for debugging).
func (f *ObjFile) DictKeys() []int32 {
	keys := make([]int32, 0, len(f.dict))
	for k := range f.dict {
		keys = append(keys, k)
	}
	return keys
}

// ReadInt32At reads an int32 at the given byte offset in the file.
func (f *ObjFile) ReadInt32At(offset int) int32 {
	return int32(binary.LittleEndian.Uint32(f.data[offset:]))
}

// FindObject looks up an object by dictionary ID.
func (f *ObjFile) FindObject(id int32) (Object, error) {
	if f.dict == nil {
		if err := f.BuildDictionary(); err != nil {
			return nil, err
		}
	}
	info, ok := f.dict[id]
	if !ok {
		return nil, nil
	}
	// ID containers (headers) → return parent object
	if typeIsIDContainer(info.Type) && info.Parent != nil {
		info = info.Parent
	}
	return f.GetObject(info)
}

// GetObject loads and caches an object from its ObjInfo.
func (f *ObjFile) GetObject(info *ObjInfo) (Object, error) {
	if cached, ok := f.objCache[info.Offset]; ok {
		return cached, nil
	}
	obj := f.createObject(info)
	if obj == nil {
		return nil, nil
	}
	// Save/restore position
	savedPos := f.pos
	f.pos = info.Offset
	err := obj.Load(f)
	f.pos = savedPos
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", info, err)
	}
	f.objCache[info.Offset] = obj
	return obj, nil
}

func (f *ObjFile) createObject(info *ObjInfo) Object {
	switch info.Type {
	case TypeSurface:
		return &Surface{info: info}
	case TypeMaterialPalette:
		return &MaterialPalette{info: info}
	case TypeMaterial:
		return &Material{info: info}
	case TypePrimBuffer, TypeSkinPrimBuffer:
		return &PrimBuffer{info: info}
	case TypeColorBuffer:
		return &ColorBuffer{info: info}
	case TypeSimpleSprite:
		return &SimpleSprite{info: info, UsePretrans: true}
	case TypeSimpleSubSprite:
		return &SimpleSubSprite{SimpleSprite: SimpleSprite{info: info, UsePretrans: true}}
	case TypeSkinSubSprite:
		return &SkinSubSprite{SimpleSubSprite: SimpleSubSprite{SimpleSprite: SimpleSprite{info: info, UsePretrans: true}}}
	case TypeGroupSprite:
		return &GroupSprite{SimpleSprite: SimpleSprite{info: info, UsePretrans: true}}
	case TypeHSprite:
		return &HSprite{GroupSprite: GroupSprite{SimpleSprite: SimpleSprite{info: info, UsePretrans: true}}}
	case TypeCSprite:
		return &CSprite{GroupSprite: GroupSprite{SimpleSprite: SimpleSprite{info: info, UsePretrans: true}}}
	case TypeFloraSprite:
		return &FloraSprite{SimpleSprite: SimpleSprite{info: info}}
	case TypeLODSprite:
		return &LODSprite{SimpleSprite: SimpleSprite{info: info, UsePretrans: true}}
	case TypeCSpriteVariant:
		return &SkinLODSprite{info: info}
	case TypeZone:
		return &Zone{info: info}
	case TypeZoneBase:
		return &ZoneBase{info: info}
	case TypeZonePreTranslations:
		return &ZonePreTranslations{info: info}
	case TypeZoneActors:
		return &ZoneActors{info: info}
	case TypeZoneActor:
		return &ZoneActor{info: info}
	case TypeCollBuffer:
		return &CollBuffer{info: info}
	case TypeWorld:
		return &World{info: info}
	case TypeWorldBase:
		return &WorldBase{info: info}
	case TypeWorldBaseHeader:
		return &WorldBaseHeader{info: info}
	case TypeWorldZoneProxies:
		return &WorldZoneProxies{info: info}
	case TypeHSpriteHierarchy:
		return &HSpriteHierarchy{info: info}
	case TypeHSpriteAnim:
		return &HSpriteAnim{info: info}
	case TypeSpellEffect:
		return &SpellEffect{info: info}
	case TypeParticleSprite:
		return &ParticleSprite{info: info}
	case TypeParticleDefinition:
		return &ParticleDefinition{info: info}
	case TypeEffectVolumeSprite:
		return &EffectVolumeSprite{info: info}
	case TypePointLight:
		return &PointLight{info: info}
	default:
		return &GenericObj{info: info}
	}
}

// RawBytes returns a slice of the underlying file data.
func (f *ObjFile) RawBytes(offset, size int) []byte {
	end := offset + size
	if end > len(f.data) {
		end = len(f.data)
	}
	if offset >= end {
		return nil
	}
	return f.data[offset:end]
}

// Seek sets the read position.
func (f *ObjFile) Seek(offset int) {
	f.pos = offset
}

// Pos returns the current read position.
func (f *ObjFile) Pos() int {
	return f.pos
}

// --- Reader helpers (all little-endian) ---

func (f *ObjFile) readByte() byte {
	v := f.data[f.pos]
	f.pos++
	return v
}

func (f *ObjFile) readInt16() int16 {
	v := int16(binary.LittleEndian.Uint16(f.data[f.pos:]))
	f.pos += 2
	return v
}

func (f *ObjFile) readUint16() uint16 {
	v := binary.LittleEndian.Uint16(f.data[f.pos:])
	f.pos += 2
	return v
}

func (f *ObjFile) readInt32() int32 {
	v := int32(binary.LittleEndian.Uint32(f.data[f.pos:]))
	f.pos += 4
	return v
}

func (f *ObjFile) readUint32() uint32 {
	v := binary.LittleEndian.Uint32(f.data[f.pos:])
	f.pos += 4
	return v
}

func (f *ObjFile) readInt64() int64 {
	v := int64(binary.LittleEndian.Uint64(f.data[f.pos:]))
	f.pos += 8
	return v
}

func (f *ObjFile) readFloat32() float32 {
	bits := binary.LittleEndian.Uint32(f.data[f.pos:])
	f.pos += 4
	return float32frombits(bits)
}

func (f *ObjFile) readBytes(n int) []byte {
	v := make([]byte, n)
	copy(v, f.data[f.pos:f.pos+n])
	f.pos += n
	return v
}

func (f *ObjFile) skipBytes(n int) {
	f.pos += n
}

func (f *ObjFile) readPoint() Point {
	return Point{f.readFloat32(), f.readFloat32(), f.readFloat32()}
}

func (f *ObjFile) readBox() Box {
	return Box{
		MinX: f.readFloat32(), MinY: f.readFloat32(), MinZ: f.readFloat32(),
		MaxX: f.readFloat32(), MaxY: f.readFloat32(), MaxZ: f.readFloat32(),
	}
}

func (f *ObjFile) readColor() [4]byte {
	var c [4]byte
	copy(c[:], f.data[f.pos:f.pos+4])
	f.pos += 4
	return c
}

func (f *ObjFile) readString() (string, error) {
	length := int(f.readInt16())
	if length < 0 || length > 1024 {
		return "", fmt.Errorf("readString sanity: len=%d", length)
	}
	s := string(f.data[f.pos : f.pos+length])
	f.pos += length
	return s, nil
}

// AllObjects returns all parsed ObjInfo nodes in file order.
func (f *ObjFile) AllObjects() []*ObjInfo {
	return f.objects
}

// GenericObj is used for unimplemented object types.
type GenericObj struct {
	info *ObjInfo
}

func (g *GenericObj) Load(_ *ObjFile) error { return nil }
func (g *GenericObj) ObjInfo() *ObjInfo      { return g.info }
