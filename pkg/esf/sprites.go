package esf

// SimpleSprite is the base 3D object type in ESF files.
type SimpleSprite struct {
	info        *ObjInfo
	matPal      *MaterialPalette
	BBox        Box
	primInfo    *ObjInfo
	collInfo    *ObjInfo
	UsePretrans bool
	matPalID    int32
}

func (s *SimpleSprite) Load(file *ObjFile) error {
	headerInfo := s.info.Child(TypeSimpleSpriteHeader)
	file.Seek(headerInfo.Offset)
	file.skipBytes(4) // dict_id
	s.BBox = file.readBox()
	if headerInfo.Version != 0 {
		_ = file.readFloat32() // unknown_float
	}
	s.primInfo = s.info.Child(TypePrimBuffer)
	s.collInfo = s.info.Child(TypeCollBuffer)
	return nil
}

func (s *SimpleSprite) ObjInfo() *ObjInfo { return s.info }

// GetMatPal loads and returns the material palette from this sprite's children.
func (s *SimpleSprite) GetMatPal(file *ObjFile) (*MaterialPalette, error) {
	if s.matPal != nil {
		return s.matPal, nil
	}
	mpInfo := s.info.Child(TypeMaterialPalette)
	if mpInfo == nil {
		return nil, nil
	}
	obj, err := file.GetObject(mpInfo)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}
	s.matPal = obj.(*MaterialPalette)
	return s.matPal, nil
}

// GetPrimBuffer loads and returns the primitive buffer.
func (s *SimpleSprite) GetPrimBuffer(file *ObjFile) (*PrimBuffer, error) {
	if s.primInfo == nil {
		return nil, nil
	}
	obj, err := file.GetObject(s.primInfo)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}
	return obj.(*PrimBuffer), nil
}

// GetCollBuffer loads and returns the collision buffer.
func (s *SimpleSprite) GetCollBuffer(file *ObjFile) (*CollBuffer, error) {
	if s.collInfo == nil {
		return nil, nil
	}
	obj, err := file.GetObject(s.collInfo)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}
	return obj.(*CollBuffer), nil
}

// SimpleSubSprite extends SimpleSprite for terrain and building pieces.
// Its material palette is referenced by dictionary ID rather than as a child.
type SimpleSubSprite struct {
	SimpleSprite
	MatPalID int32
}

func (s *SimpleSubSprite) Load(file *ObjFile) error {
	headerInfo := s.info.Child(TypeSimpleSubSpriteHeader)
	file.Seek(headerInfo.Offset)
	file.skipBytes(4) // id
	s.MatPalID = file.readInt32()
	s.BBox = file.readBox()
	s.primInfo = s.info.Child(TypePrimBuffer)
	s.collInfo = s.info.Child(TypeCollBuffer)
	return nil
}

func (s *SimpleSubSprite) ObjInfo() *ObjInfo { return s.info }

// GetMatPal loads the material palette by dictionary lookup.
func (s *SimpleSubSprite) GetMatPal(file *ObjFile) (*MaterialPalette, error) {
	if s.matPal != nil {
		return s.matPal, nil
	}
	obj, err := file.FindObject(s.MatPalID)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}
	s.matPal = obj.(*MaterialPalette)
	return s.matPal, nil
}

// SkinSubSprite extends SimpleSubSprite for skinned mesh data.
type SkinSubSprite struct {
	SimpleSubSprite
}

func (s *SkinSubSprite) Load(file *ObjFile) error {
	contentInfo := s.info.Child(TypeSkinSubSprite2)
	file.Seek(contentInfo.Offset)
	_ = file.readInt32() // id
	s.MatPalID = file.readInt32()
	s.BBox = file.readBox()
	s.primInfo = s.info.Child(TypeSkinPrimBuffer)
	return nil
}

func (s *SkinSubSprite) ObjInfo() *ObjInfo { return s.info }

// GroupSprite is a composite of positioned sprites.
type GroupSprite struct {
	SimpleSprite
	Placements []*SpritePlacement
}

func (g *GroupSprite) Load(file *ObjFile) error {
	// PS2: ParseGroupSpriteObj (0x0043B478) reads DictID+BBox from 0x2C10 header child.
	headerInfo := g.info.Child(TypeGroupSpriteHeader)
	if headerInfo != nil {
		file.Seek(headerInfo.Offset)
		file.skipBytes(4) // DictID (already stored in ObjInfo)
		g.BBox = file.readBox()
	}

	// PS2: ParseGroupSpriteMembers (0x0043B708) reads count + per-member:
	// DictID(4) + Pos(12) + Rot(12) + Scale(4) = 32 bytes/entry.
	membersInfo := g.info.Child(TypeGroupSpriteMembers)
	if membersInfo == nil {
		return nil
	}
	file.Seek(membersInfo.Offset)
	nmemb := file.readInt32()
	g.Placements = make([]*SpritePlacement, nmemb)
	for i := int32(0); i < nmemb; i++ {
		membID := file.readInt32()
		pos := file.readPoint()
		scale := file.readFloat32() // PS2: float[3] = scale (always 1.0 in practice)
		rot := file.readPoint()
		g.Placements[i] = &SpritePlacement{SpriteID: membID, Pos: pos, Rot: rot, Scale: scale}
	}
	return nil
}

func (g *GroupSprite) ObjInfo() *ObjInfo { return g.info }

// GetSprites returns the sprite placements for this group.
func (g *GroupSprite) GetSprites() []*SpritePlacement { return g.Placements }

// BoneNode represents a single bone in a skeletal hierarchy.
// Position is the absolute bind-pose position in model space.
type BoneNode struct {
	ParentID int32   // -1 = root
	Pos      Point   // bind-pose position (model space)
	Quat     [4]float32 // bind-pose quaternion (x,y,z,w)
	Scale    float32
}

// HSpriteHierarchy describes the bone hierarchy for an HSprite.
type HSpriteHierarchy struct {
	info  *ObjInfo
	Nodes []BoneNode
}

func (h *HSpriteHierarchy) Load(file *ObjFile) error {
	file.Seek(h.info.Offset)
	ver := h.info.Version
	if ver != 0 {
		num := file.readInt32()
		file.skipBytes(int(num) * 4) // pre-floats
	}
	numNodes := file.readInt32()
	h.Nodes = make([]BoneNode, numNodes)
	for i := int32(0); i < numNodes; i++ {
		h.Nodes[i].ParentID = file.readInt32()
		// Bind pose: 8 floats = quaternion(4) + scale(1) + position(3)
		h.Nodes[i].Quat[0] = file.readFloat32() // qx
		h.Nodes[i].Quat[1] = file.readFloat32() // qy
		h.Nodes[i].Quat[2] = file.readFloat32() // qz
		h.Nodes[i].Quat[3] = file.readFloat32() // qw
		h.Nodes[i].Scale = file.readFloat32()
		h.Nodes[i].Pos.X = file.readFloat32()
		h.Nodes[i].Pos.Y = file.readFloat32()
		h.Nodes[i].Pos.Z = file.readFloat32()
		if ver != 0 {
			file.skipBytes(4) // k
		}
		if ver >= 2 {
			file.skipBytes(3 * 4) // fixScale point
		}
	}
	return nil
}

func (h *HSpriteHierarchy) ObjInfo() *ObjInfo { return h.info }

// HSpriteAttachment holds one entry from the HSpriteAttachments (0x2500) block.
// PS2: ParseHSpriteAttachments (0x00436318) reads per entry:
//   int32 attachType (0=SimpleSprite, 1=CSprite)
//   int32 dictID     (hash referencing an existing named object)
//   int32 parentIndex (bone index, -1 = root)
type HSpriteAttachment struct {
	AttachType  int32 // 0=SimpleSprite, 1=CSprite
	DictID      int32 // attached object DictID
	ParentIndex int32 // bone index (-1 = root)
}

// HSprite is a hierarchical/skeletal sprite built from sub-sprites
// arranged in a bone hierarchy.
type HSprite struct {
	GroupSprite
	SpriteInfos []*ObjInfo
	Hierarchy   *HSpriteHierarchy
	Attachments []HSpriteAttachment
	TriggerIDs  []int32 // animation trigger refID hashes (from 0x2450)
}

func (h *HSprite) Load(file *ObjFile) error {
	// PS2: ParseHSpriteObj (0x00435BE8) reads DictID+BBox from 0x2210 header child.
	headerInfo := h.info.Child(TypeHSpriteHeader)
	if headerInfo != nil {
		file.Seek(headerInfo.Offset)
		file.skipBytes(4) // DictID (already stored in ObjInfo)
		h.BBox = file.readBox()
	}

	arrayInfo := h.info.Child(TypeHSpriteArray)
	if arrayInfo != nil {
		h.SpriteInfos = arrayInfo.ChildrenOfType(0) // all children
	}
	// Load and retain hierarchy.
	hierInfo := h.info.Child(TypeHSpriteHierarchy)
	if hierInfo != nil {
		obj, _ := file.GetObject(hierInfo)
		if obj != nil {
			h.Hierarchy = obj.(*HSpriteHierarchy)
		}
	}
	// Load attachments (0x2500).
	// PS2: ParseHSpriteAttachments (0x00436318) reads count + per-entry (type, dictID, parentIndex).
	attachInfo := h.info.Child(TypeHSpriteAttachments)
	if attachInfo != nil && attachInfo.Size >= 4 {
		file.Seek(attachInfo.Offset)
		count := file.readInt32()
		if count > 0 {
			h.Attachments = make([]HSpriteAttachment, count)
			for i := int32(0); i < count; i++ {
				h.Attachments[i].AttachType = file.readInt32()
				h.Attachments[i].DictID = file.readInt32()
				h.Attachments[i].ParentIndex = file.readInt32()
			}
		}
	}
	// Load triggers (0x2450).
	// PS2: ParseHSpriteTriggers (0x00436248) reads count + count × int32 (refID hashes).
	// These reference animation keyframe RefIDs for triggering events during playback.
	trigInfo := h.info.Child(TypeHSpriteTriggers)
	if trigInfo != nil && trigInfo.Size >= 4 {
		file.Seek(trigInfo.Offset)
		count := file.readInt32()
		if count > 0 {
			h.TriggerIDs = make([]int32, count)
			for i := int32(0); i < count; i++ {
				h.TriggerIDs[i] = file.readInt32()
			}
		}
	}
	return nil
}

func (h *HSprite) ObjInfo() *ObjInfo { return h.info }

// GetSprites returns the placements, loading them on first call.
func (h *HSprite) GetSprites() []*SpritePlacement {
	return h.Placements
}

// LoadSprites populates placements from HSprite children.
func (h *HSprite) LoadSprites(file *ObjFile) ([]*SpritePlacement, error) {
	if h.Placements != nil {
		return h.Placements, nil
	}
	h.Placements = make([]*SpritePlacement, 0, len(h.SpriteInfos))
	for _, info := range h.SpriteInfos {
		obj, err := file.GetObject(info)
		if err != nil {
			continue
		}
		if obj == nil {
			continue
		}
		switch s := obj.(type) {
		case *SimpleSubSprite:
			s.UsePretrans = false
			h.Placements = append(h.Placements, &SpritePlacement{Sprite: &s.SimpleSprite})
		case *SkinSubSprite:
			s.UsePretrans = false
			h.Placements = append(h.Placements, &SpritePlacement{Sprite: &s.SimpleSprite})
		}
	}
	return h.Placements, nil
}

// PlayListEntry maps a VICSpriteAnimID (playlist index) to animation DictIDs.
// Parsed from CSpritePlayList (0x2910). The PS2 uses these indices for
// SetAnimation/SetAttackAction; the DictIDs resolve to HSpriteAnim entries.
type PlayListEntry struct {
	Index      int32   // VICSpriteAnimID — the playlist slot
	AnimDictID [2]int32 // [0]=upper/primary, [1]=lower/secondary animation DictIDs
	Speed      float32 // playback speed multiplier
	PlayOnce   int32   // 0=loop, 1=play-once
}

// TSlotEntry maps a material index to a texture slot ID within a CSprite.
// Parsed from CSpriteTSlotList (0x2930) child nodes.
// ESF wire order: SlotID, MeshIndex, Flag (3 consecutive int32s).
type TSlotEntry struct {
	MeshIndex int32 // material index within the MaterialPalette
	SlotID    int32 // texture slot: 0=chest,1=bracer,2=gloves,3=legs,4=boots,5+=hair/face/helm/robe
	Flag      int32 // 0 or 1 (1 = base body material, 0 = swappable overlay)
}

// ASlotEntry maps an attachment slot to a bone index within a CSprite.
// Parsed from CSpriteASlotList (0x2920).
// PS2: ParseCSpriteASlotList (0x00437F18), stored at VICSprite+0x110C, stride 12.
// Slot 0 = right hand (weapon), 1 = left hand (shield), 2 = two-handed.
type ASlotEntry struct {
	SlotID    int32 // attachment slot (0=right hand, 1=left hand, 2=two-handed)
	BoneIndex int32 // bone index for this attachment point
}

// NodeIDEntry maps a named node index to a bone index within a CSprite.
// Parsed from CSpriteNodeIDList (0x2915).
// PS2: ParseCSpriteNodeIDList (0x00437E30), stored at VICSprite+0x0F14.
// Provides canonical bone identification (head, hands, etc.) without heuristics.
type NodeIDEntry struct {
	NodeIndex int32 // named node slot (0=right hand, 1=left hand, etc.)
	BoneIndex int32 // bone index
}

// CSprite represents a character or NPC sprite, composed of sub-sprites.
type CSprite struct {
	GroupSprite
	DictID       int32
	SkelType     int32   // VICSpriteSkelType enum (ver >= 1)
	DefaultScale float32 // uniform scale factor (PS2 VIHSprite+0xD0, ver >= 2)
	Race         int32   // VICSpriteRace enum (ver >= 3, default 9)
	Sex          int32   // VICSpriteSex enum (ver >= 3, default 0)
	Hierarchy    *HSpriteHierarchy
	PlayList     []PlayListEntry // VICSpriteAnimID → animation DictID mapping (from 0x2910)
	TSlotList    []TSlotEntry    // material→texture slot mapping from ESF
	ASlotList    []ASlotEntry    // attachment slot → bone index (from 0x2920)
	NodeIDList   []NodeIDEntry   // named node → bone index (from 0x2915)
	Animations   []*HSpriteAnim  // skeletal animations (from 0x2600 children)
	BoneRefMap   *BoneRefMap     // refID hash → bone index mapping (from RefMap 0x5000)
}

func (c *CSprite) Load(file *ObjFile) error {
	// PS2 ParseCSpriteObj (0x00437450) reads DictID+BBox+fields from 0x2710 header child.
	// The header child's version controls field layout, not the parent 0x2700 version.
	c.DefaultScale = 1.0 // PS2 default when header ver < 2
	c.Race = 9           // PS2 default when header ver < 3
	headerInfo := c.info.Child(TypeCSpriteHeader)
	if headerInfo != nil {
		file.Seek(headerInfo.Offset)
		c.DictID = file.readInt32()
		c.BBox = file.readBox()
		ver := headerInfo.Version
		if ver != 0 {
			c.SkelType = file.readInt32()
		}
		if ver >= 2 {
			c.DefaultScale = file.readFloat32()
		}
		if ver >= 3 {
			c.Race = file.readInt32()
			c.Sex = file.readInt32()
		}
		if ver >= 4 {
			file.skipBytes(4) // ExtraFlag
		}
	}

	c.Placements = nil
	arrayInfo := c.info.Child(TypeCSpriteArray)
	if arrayInfo != nil {
		// Collect sprite children, tagging variant-wrapped meshes.
		// Direct children (non-variant) get Variant=nil (base, always drawn).
		// Variant-wrapped children (0x2A40 → 0x2A60) get a VariantTag.
		//
		// Analysis of CHAR.ESF shows the 0x2A50 header contains bounding box
		// data (ID + 6 floats), not slot/set mappings. All CSprites have
		// exactly 1 variant containing the body meshes. Armor appearance
		// is controlled by texture swapping via PS2 static tables, not by
		// mesh variant selection. We tag variant meshes as ArmorSlotBase
		// (always drawn) for now; future texture-based armor rendering can
		// extend this.
		variantIndex := 0
		for _, child := range arrayInfo.ChildrenOfType(0) {
			if child.Type == TypeCSpriteVariant {
				meshesInfo := child.Child(TypeCSpriteVariantMeshes)
				if meshesInfo == nil {
					variantIndex++
					continue
				}
				// Tag all meshes from this variant.
				tag := &VariantTag{Slot: ArmorSlotBase, Set: byte(variantIndex)}
				for _, si := range meshesInfo.ChildrenOfType(0) {
					c.loadPlacement(file, si, tag)
				}
				variantIndex++
			} else {
				// Direct child — base mesh, always drawn.
				c.loadPlacement(file, child, nil)
			}
		}
	}

	// Load and retain bone hierarchy.
	hierInfo := c.info.Child(TypeHSpriteHierarchy)
	if hierInfo != nil {
		obj, _ := file.GetObject(hierInfo)
		if obj != nil {
			c.Hierarchy = obj.(*HSpriteHierarchy)
		}
	}

	// Parse TSlotList (material → texture slot mapping).
	tslotInfo := c.info.Child(TypeCSpriteTSlotList)
	if tslotInfo != nil {
		file.Seek(tslotInfo.Offset)
		count := int(file.readInt32())
		if count > 0 && count < 256 {
			c.TSlotList = make([]TSlotEntry, count)
			for i := 0; i < count; i++ {
				c.TSlotList[i] = TSlotEntry{
					SlotID:    file.readInt32(),
					MeshIndex: file.readInt32(),
					Flag:      file.readInt32(),
				}
			}
		}
	}

	// Parse ASlotList (attachment slot → bone index mapping).
	// PS2: ParseCSpriteASlotList (0x00437F18), tag 0x2920.
	// Wire format per entry: slotID(i32), boneIndex(i32).
	aslotInfo := c.info.Child(TypeCSpriteASlotList)
	if aslotInfo != nil && aslotInfo.Size >= 4 {
		file.Seek(aslotInfo.Offset)
		count := int(file.readInt32())
		if count > 0 && count < 64 {
			c.ASlotList = make([]ASlotEntry, count)
			for i := 0; i < count; i++ {
				c.ASlotList[i] = ASlotEntry{
					SlotID:    file.readInt32(),
					BoneIndex: file.readInt32(),
				}
			}
		}
	}

	// Parse NodeIDList (named node → bone index mapping).
	// PS2: ParseCSpriteNodeIDList (0x00437E30), tag 0x2915.
	// Wire format per entry: nodeIndex(i32), boneIndex(i32).
	nodeIDInfo := c.info.Child(TypeCSpriteNodeIDList)
	if nodeIDInfo != nil && nodeIDInfo.Size >= 4 {
		file.Seek(nodeIDInfo.Offset)
		count := int(file.readInt32())
		if count > 0 && count < 64 {
			c.NodeIDList = make([]NodeIDEntry, count)
			for i := 0; i < count; i++ {
				c.NodeIDList[i] = NodeIDEntry{
					NodeIndex: file.readInt32(),
					BoneIndex: file.readInt32(),
				}
			}
		}
	}

	// Parse CPlayList (VICSpriteAnimID → animation DictID mapping).
	// ESF type 0x2910, version 3. Each entry maps a playlist index to animation
	// DictIDs that resolve to HSpriteAnim entries via FindTyped.
	plInfo := c.info.Child(TypeCSpritePlayList)
	if plInfo != nil {
		file.Seek(plInfo.Offset)
		plCount := int(file.readInt32())
		if plCount > 0 && plCount < 256 {
			c.PlayList = make([]PlayListEntry, plCount)
			for i := 0; i < plCount; i++ {
				var e PlayListEntry
				e.AnimDictID[0] = file.readInt32() // primary/upper animation DictID
				if plInfo.Version >= 1 {
					e.AnimDictID[1] = file.readInt32() // secondary/lower animation DictID
				}
				e.Index = file.readInt32()    // playlist slot (VICSpriteAnimID)
				e.Speed = file.readFloat32()  // playback speed
				e.PlayOnce = file.readInt32() // 0=loop, 1=play-once
				// Version >= 2: group animation fields (not needed for Go client)
				if plInfo.Version >= 2 {
					file.skipBytes(8) // groupAnimDictID1 + speed1
					if plInfo.Version >= 3 {
						file.skipBytes(4) // groupFloat1
					}
					file.skipBytes(8) // groupAnimDictID2 + speed2
					if plInfo.Version >= 3 {
						file.skipBytes(4) // groupFloat2
					}
				}
				c.PlayList[i] = e
			}
		}
	}

	// Load animations from 0x2610 container (holds 0x2600 HSpriteAnim children).
	animContainer := c.info.Child(0x2610)
	if animContainer != nil {
		for _, ai := range animContainer.ChildrenOfType(TypeHSpriteAnim) {
			obj, err := file.GetObject(ai)
			if err != nil || obj == nil {
				continue
			}
			if anim, ok := obj.(*HSpriteAnim); ok {
				c.Animations = append(c.Animations, anim)
			}
		}
	}

	// Load bone RefMap — the RefMap with count == numBones.
	numBones := int32(0)
	if c.Hierarchy != nil {
		numBones = int32(len(c.Hierarchy.Nodes))
	}
	if numBones > 0 {
		for _, child := range c.info.Children {
			if child.Type == TypeRefMap {
				rm := ParseBoneRefMap(file, child)
				if rm != nil && int32(len(rm.Entries)) == numBones {
					c.BoneRefMap = rm
					break
				}
			}
		}
	}

	return nil
}

// loadPlacement loads a sprite object and appends it to Placements with the given variant tag.
func (c *CSprite) loadPlacement(file *ObjFile, info *ObjInfo, tag *VariantTag) {
	obj, err := file.GetObject(info)
	if err != nil || obj == nil {
		return
	}
	var sp *SpritePlacement
	switch s := obj.(type) {
	case *SkinSubSprite:
		sp = &SpritePlacement{Sprite: &s.SimpleSprite}
	case *SimpleSubSprite:
		sp = &SpritePlacement{Sprite: &s.SimpleSprite}
	case *SimpleSprite:
		sp = &SpritePlacement{Sprite: s}
	}
	if sp != nil {
		sp.Variant = tag
		c.Placements = append(c.Placements, sp)
	}
}

func (c *CSprite) ObjInfo() *ObjInfo { return c.info }

// AttachBone returns the bone index for an attachment slot, or -1 if not found.
// Slot 0 = right hand (weapon), 1 = left hand (shield), 2 = two-handed.
func (c *CSprite) AttachBone(slotID int32) int32 {
	for i := range c.ASlotList {
		if c.ASlotList[i].SlotID == slotID {
			return c.ASlotList[i].BoneIndex
		}
	}
	return -1
}

// NodeBone returns the bone index for a named node, or -1 if not found.
func (c *CSprite) NodeBone(nodeIndex int32) int32 {
	for i := range c.NodeIDList {
		if c.NodeIDList[i].NodeIndex == nodeIndex {
			return c.NodeIDList[i].BoneIndex
		}
	}
	return -1
}

// FloraSprite represents a procedurally placed vegetation object (grass, flowers).
// PS2: ParseFloraSpriteObj (0x0043C230), type 0x2F00.
// Contains header (0x2F01: DictID + BBox), MaterialPalette (0x1110),
// and FloraPrimBuffer (0x1230: flora-specific vertex data).
type FloraSprite struct {
	SimpleSprite
}

func (f *FloraSprite) Load(file *ObjFile) error {
	headerInfo := f.info.Child(TypeFloraSpriteHeader)
	if headerInfo != nil {
		file.Seek(headerInfo.Offset)
		file.skipBytes(4) // DictID (already in headerInfo.DictID)
		f.BBox = file.readBox()
	}
	f.primInfo = f.info.Child(TypeFloraPrimBuffer)
	f.collInfo = f.info.Child(TypeCollBuffer)
	return nil
}

func (f *FloraSprite) ObjInfo() *ObjInfo { return f.info }

// LODLowLevel controls whether LODSprite selects the least detailed level.
var LODLowLevel bool

// LODSprite contains several levels of detail for a model and selects one.
type LODSprite struct {
	SimpleSprite
	Level1 *ObjInfo
}

func (l *LODSprite) Load(file *ObjFile) error {
	file.Seek(l.info.Offset)
	file.skipBytes(4) // dict_id
	l.BBox = file.readBox()
	arrayInfo := l.info.NextSibling()
	if arrayInfo != nil {
		children := arrayInfo.ChildrenOfType(0)
		if len(children) > 0 {
			if LODLowLevel {
				l.Level1 = children[len(children)-1]
			} else {
				l.Level1 = children[0]
			}
		}
	}
	return nil
}

func (l *LODSprite) ObjInfo() *ObjInfo { return l.info }

// GetSprite loads and returns the selected level-of-detail sprite.
func (l *LODSprite) GetSprite(file *ObjFile) (*SimpleSprite, bool, error) {
	if l.Level1 == nil {
		return nil, false, nil
	}
	obj, err := file.GetObject(l.Level1)
	if err != nil {
		return nil, false, err
	}
	if obj == nil {
		return nil, false, nil
	}
	switch s := obj.(type) {
	case *FloraSprite:
		return &s.SimpleSprite, true, nil
	case *SimpleSprite:
		return s, false, nil
	case *SimpleSubSprite:
		return &s.SimpleSprite, false, nil
	case *SkinSubSprite:
		return &s.SimpleSprite, false, nil
	}
	return nil, false, nil
}

// LODLevel holds a distance threshold for one level-of-detail mesh.
type LODLevel struct {
	SpriteInfo *ObjInfo // mesh at this LOD
	Distance   float32  // switch distance threshold
}

// SkinLODSprite is a skinned level-of-detail sprite parsed from CSpriteVariant (0x2A40).
// PS2: ParseSkinLODSpriteObj (0x0043B0C8).
// Contains a header (0x2A50: DictID + BBox), mesh array (0x2A60: SkinSubSprite children),
// and LOD footer (0x2A70: per-level distance thresholds).
type SkinLODSprite struct {
	info       *ObjInfo
	DictID     int32
	BBox       Box
	MeshInfos  []*ObjInfo // SkinSubSprite children from 0x2A60
	LODLevels  []LODLevel // distance thresholds from 0x2A70
}

func (s *SkinLODSprite) ObjInfo() *ObjInfo { return s.info }

func (s *SkinLODSprite) Load(file *ObjFile) error {
	// Read 0x2A50 header (CSpriteVariantHeader): DictID + BBox
	headerInfo := s.info.Child(TypeCSpriteVariantHeader)
	if headerInfo != nil {
		file.Seek(headerInfo.Offset)
		s.DictID = file.readInt32()
		s.BBox = file.readBox()
	}

	// Collect mesh children from 0x2A60 (CSpriteVariantMeshes)
	meshesInfo := s.info.Child(TypeCSpriteVariantMeshes)
	if meshesInfo != nil {
		s.MeshInfos = meshesInfo.ChildrenOfType(0)
	}

	// Parse 0x2A70 footer (CSpriteVariantFooter): LOD distance thresholds
	footerInfo := s.info.Child(TypeCSpriteVariantFooter)
	if footerInfo != nil {
		file.Seek(footerInfo.Offset)
		count := file.readInt32()
		if count > 0 && count < 32 {
			s.LODLevels = make([]LODLevel, count)
			for i := int32(0); i < count; i++ {
				dictID := file.readInt32()
				// Find the sprite object for this LOD level by DictID
				var sprInfo *ObjInfo
				if file.dict != nil {
					sprInfo = file.dict[dictID]
				}
				dist := file.readFloat32()
				s.LODLevels[i] = LODLevel{
					SpriteInfo: sprInfo,
					Distance:   dist,
				}
			}
		}
	}

	return nil
}

// GetSprite loads and returns the highest-detail mesh (first LOD level or first mesh child).
func (s *SkinLODSprite) GetSprite(file *ObjFile) (*SimpleSprite, error) {
	// Prefer LOD level 0 if available
	if len(s.LODLevels) > 0 && s.LODLevels[0].SpriteInfo != nil {
		obj, err := file.GetObject(s.LODLevels[0].SpriteInfo)
		if err != nil {
			return nil, err
		}
		if obj != nil {
			switch sp := obj.(type) {
			case *SimpleSprite:
				return sp, nil
			case *SimpleSubSprite:
				return &sp.SimpleSprite, nil
			case *SkinSubSprite:
				return &sp.SimpleSprite, nil
			}
		}
	}
	// Fallback: first mesh child
	if len(s.MeshInfos) > 0 {
		obj, err := file.GetObject(s.MeshInfos[0])
		if err != nil {
			return nil, err
		}
		if obj != nil {
			switch sp := obj.(type) {
			case *SimpleSprite:
				return sp, nil
			case *SimpleSubSprite:
				return &sp.SimpleSprite, nil
			case *SkinSubSprite:
				return &sp.SimpleSprite, nil
			}
		}
	}
	return nil, nil
}
