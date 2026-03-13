package esf

import (
	"fmt"
	"image"
	"log"
	"math"
)

// ZonePreTranslations holds a list of translation vectors used to convert
// PrimBuffer and CollBuffer vertices to world coordinates.
type ZonePreTranslations struct {
	info   *ObjInfo
	Values []Point
}

func (z *ZonePreTranslations) Load(file *ObjFile) error {
	file.Seek(z.info.Offset)
	num := file.readInt32()
	z.Values = make([]Point, num)
	for i := int32(0); i < num; i++ {
		z.Values[i] = file.readPoint()
	}
	return nil
}

func (z *ZonePreTranslations) ObjInfo() *ObjInfo { return z.info }

// ZoneBase contains information about a zone, primarily the pre-translations
// used to offset vertex groups.
type ZoneBase struct {
	info            *ObjInfo
	PreTranslations []Point
}

func (z *ZoneBase) Load(file *ObjFile) error {
	ptInfo := z.info.Child(TypeZonePreTranslations)
	if ptInfo == nil {
		return nil
	}
	obj, err := file.GetObject(ptInfo)
	if err != nil {
		return err
	}
	if obj == nil {
		return nil
	}
	z.PreTranslations = obj.(*ZonePreTranslations).Values
	return nil
}

func (z *ZoneBase) ObjInfo() *ObjInfo { return z.info }

// GetPreTranslations returns the pre-translation vectors.
func (z *ZoneBase) GetPreTranslations() []Point {
	return z.PreTranslations
}

// ZoneActor describes a placed object in a zone, referencing a sprite by
// dictionary ID along with position, rotation, scale, and color.
type ZoneActor struct {
	info      *ObjInfo
	Placement *SpritePlacement
}

func (z *ZoneActor) Load(file *ObjFile) error {
	file.Seek(z.info.Offset)
	actorID := file.readInt32()
	pos := file.readPoint()
	rot := file.readPoint()
	scale := file.readFloat32()
	color := file.readColor()
	z.Placement = &SpritePlacement{
		SpriteID: actorID,
		Pos:      pos,
		Rot:      rot,
		Scale:    scale,
		Color:    color,
	}
	return nil
}

func (z *ZoneActor) ObjInfo() *ObjInfo { return z.info }

// ZoneActors collects all ZoneActor entries from the zone's room hierarchy.
type ZoneActors struct {
	info   *ObjInfo
	Actors []*ZoneActor
}

func (z *ZoneActors) Load(file *ObjFile) error {
	z.Actors = nil
	for _, roomActors := range z.info.ChildrenOfType(TypeZoneRoomActors) {
		ra3 := roomActors.Child(TypeZoneRoomActors3)
		if ra3 == nil {
			continue
		}
		for _, actorInfo := range ra3.ChildrenOfType(TypeZoneActor) {
			obj, err := file.GetObject(actorInfo)
			if err != nil {
				return err
			}
			if obj == nil {
				continue
			}
			z.Actors = append(z.Actors, obj.(*ZoneActor))
		}
	}
	return nil
}

func (z *ZoneActors) ObjInfo() *ObjInfo { return z.info }

// GetActors returns the loaded zone actors.
func (z *ZoneActors) GetActors() []*ZoneActor {
	return z.Actors
}

// RoomActorEntry pairs each actor with its room ID and index within that room.
type RoomActorEntry struct {
	Actor      *ZoneActor
	RoomID     int32 // from ZoneRoomActors2 (0x3280)
	ActorIndex int   // sequential index within the room's actor list
}

// GetActorsWithRoomIDs returns all actors annotated with their room ID and
// per-room actor index. This is needed to match baked static lighting data.
func (z *ZoneActors) GetActorsWithRoomIDs(file *ObjFile) ([]RoomActorEntry, error) {
	var result []RoomActorEntry
	for _, roomActors := range z.info.ChildrenOfType(TypeZoneRoomActors) {
		// Read room ID from ZoneRoomActors2 (0x3280)
		var roomID int32 = -1
		if ra2 := roomActors.Child(TypeZoneRoomActors2); ra2 != nil {
			data := file.RawBytes(int(ra2.Offset), int(ra2.Size))
			if len(data) >= 4 {
				roomID = int32(data[0]) | int32(data[1])<<8 | int32(data[2])<<16 | int32(data[3])<<24
			}
		}

		ra3 := roomActors.Child(TypeZoneRoomActors3)
		if ra3 == nil {
			continue
		}
		actorIdx := 0
		for _, actorInfo := range ra3.ChildrenOfType(TypeZoneActor) {
			obj, err := file.GetObject(actorInfo)
			if err != nil {
				return nil, err
			}
			if obj == nil {
				continue
			}
			result = append(result, RoomActorEntry{
				Actor:      obj.(*ZoneActor),
				RoomID:     roomID,
				ActorIndex: actorIdx,
			})
			actorIdx++
		}
	}
	return result, nil
}

// Zone represents a top-level zone node in the ESF object tree.
type Zone struct {
	info *ObjInfo
}

func (z *Zone) Load(_ *ObjFile) error {
	// Nothing to load directly; sub-objects are loaded on demand.
	return nil
}

func (z *Zone) ObjInfo() *ObjInfo { return z.info }

// GetZoneBase loads and returns the ZoneBase child.
func (z *Zone) GetZoneBase(file *ObjFile) (*ZoneBase, error) {
	baseInfo := z.info.Child(TypeZoneBase)
	if baseInfo == nil {
		return nil, nil
	}
	obj, err := file.GetObject(baseInfo)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}
	return obj.(*ZoneBase), nil
}

// GetZoneActors loads and returns the ZoneActors child.
func (z *Zone) GetZoneActors(file *ObjFile) (*ZoneActors, error) {
	actorsInfo := z.info.Child(TypeZoneActors)
	if actorsInfo == nil {
		return nil, nil
	}
	obj, err := file.GetObject(actorsInfo)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}
	return obj.(*ZoneActors), nil
}

// GetSpritePlacements builds a flat list of all sprite placements in the zone
// by combining the zone's resource sprites with placed zone actors.
func (z *Zone) GetSpritePlacements(file *ObjFile) ([]*SpritePlacement, error) {
	var ret []*SpritePlacement

	// Directly export SimpleSubSprites from zone resources.
	resources := z.info.Child(TypeZoneResources)
	if resources != nil {
		for _, spriteInfo := range resources.ChildrenOfType(TypeSimpleSubSprite) {
			obj, err := file.GetObject(spriteInfo)
			if err != nil {
				continue
			}
			if obj == nil {
				continue
			}
			if ss, ok := obj.(*SimpleSubSprite); ok {
				ret = append(ret, &SpritePlacement{Sprite: &ss.SimpleSprite})
			}
		}
	}

	// Load baked static lighting and build lookup map.
	bakedMap := map[int64][]Color32{} // key = roomID<<32 | actorIndex
	lighting, _ := z.GetStaticLighting(file)
	for _, rsl := range lighting {
		for _, entry := range rsl.Entries {
			key := int64(rsl.RoomIndex)<<32 | int64(entry.ActorIndex)
			bakedMap[key] = entry.Colors
		}
	}

	// All other types via ZoneActors with room IDs (for baked lighting matching).
	actors, err := z.GetZoneActors(file)
	if err != nil {
		return nil, fmt.Errorf("loading zone actors: %w", err)
	}
	if actors == nil {
		return ret, nil
	}

	actorsWithRooms, err := actors.GetActorsWithRoomIDs(file)
	if err != nil {
		return nil, fmt.Errorf("loading actors with room IDs: %w", err)
	}

	for _, rae := range actorsWithRooms {
		actor := rae.Actor
		bakedKey := int64(rae.RoomID)<<32 | int64(rae.ActorIndex)
		actorColors := bakedMap[bakedKey]

		obj, err := file.FindObject(actor.Placement.SpriteID)
		if err != nil {
			continue
		}
		if obj == nil {
			continue
		}

		// Type switch must list embedded types (HSprite, CSprite) before
		// GroupSprite, since Go type switches match the concrete type only.
		switch s := obj.(type) {
		case *HSprite:
			// HSprite uses relative rotation: add zone actor rotation.
			sprites, loadErr := s.LoadSprites(file)
			if loadErr != nil {
				continue
			}
			colorOff := 0
			for _, sp := range sprites {
				combined := sp.CombineWith(actor.Placement)
				inner, resolveErr := sp.GetSprite(file)
				if resolveErr != nil || inner == nil {
					continue
				}
				p := &SpritePlacement{
					Sprite:  inner,
					Pos:     combined.Pos,
					Rot:     combined.Rot,
					Scale:   combined.Scale,
					Color:   combined.Color,
					IsFlora: sp.IsFlora,
				}
				colorOff = splitBakedColors(p, inner, file, actorColors, colorOff)
				ret = append(ret, p)
			}
		case *CSprite:
			// CSprite is a GroupSprite variant: absolute rotation.
			colorOff := 0
			for _, sp := range s.GetSprites() {
				combined := sp.CombineWith(actor.Placement)
				combined.Rot = sp.Rot
				inner, resolveErr := sp.GetSprite(file)
				if resolveErr != nil || inner == nil {
					continue
				}
				p := &SpritePlacement{
					Sprite:  inner,
					Pos:     combined.Pos,
					Rot:     combined.Rot,
					Scale:   combined.Scale,
					Color:   combined.Color,
					IsFlora: sp.IsFlora,
				}
				colorOff = splitBakedColors(p, inner, file, actorColors, colorOff)
				ret = append(ret, p)
			}
		case *GroupSprite:
			// GroupSprite children use their own rotation combined with actor rotation.
			colorOff := 0
			for _, sp := range s.GetSprites() {
				combined := sp.CombineWith(actor.Placement)
				combined.Rot = sp.Rot.Add(actor.Placement.Rot)
				inner, resolveErr := sp.GetSprite(file)
				if resolveErr != nil || inner == nil {
					continue
				}
				p := &SpritePlacement{
					Sprite:  inner,
					Pos:     combined.Pos,
					Rot:     combined.Rot,
					Scale:   combined.Scale,
					Color:   combined.Color,
					IsFlora: sp.IsFlora,
				}
				colorOff = splitBakedColors(p, inner, file, actorColors, colorOff)
				ret = append(ret, p)
			}
		case *LODSprite:
			inner, isFlora, resolveErr := s.GetSprite(file)
			if resolveErr != nil || inner == nil {
				continue
			}
			ret = append(ret, &SpritePlacement{
				Sprite:      inner,
				Pos:         actor.Placement.Pos,
				Rot:         actor.Placement.Rot,
				Scale:       actor.Placement.Scale,
				Color:       actor.Placement.Color,
				IsFlora:     isFlora,
				BakedColors: actorColors,
			})
		case *SkinSubSprite:
			ret = append(ret, &SpritePlacement{
				Sprite:      &s.SimpleSprite,
				Pos:         actor.Placement.Pos,
				Rot:         actor.Placement.Rot,
				Scale:       actor.Placement.Scale,
				Color:       actor.Placement.Color,
				BakedColors: actorColors,
			})
		case *SimpleSubSprite:
			ret = append(ret, &SpritePlacement{
				Sprite:      &s.SimpleSprite,
				Pos:         actor.Placement.Pos,
				Rot:         actor.Placement.Rot,
				Scale:       actor.Placement.Scale,
				Color:       actor.Placement.Color,
				BakedColors: actorColors,
			})
		case *ParticleSprite:
			// ParticleSprites are zone-placed particle effects.
			// Skip for now — they need a particle system renderer, not mesh rendering.
			continue
		case *FloraSprite:
			ret = append(ret, &SpritePlacement{
				Sprite:      &s.SimpleSprite,
				Pos:         actor.Placement.Pos,
				Rot:         actor.Placement.Rot,
				Scale:       actor.Placement.Scale,
				Color:       actor.Placement.Color,
				IsFlora:     true,
				BakedColors: actorColors,
			})
		case *SimpleSprite:
			ret = append(ret, &SpritePlacement{
				Sprite:      s,
				Pos:         actor.Placement.Pos,
				Rot:         actor.Placement.Rot,
				Scale:       actor.Placement.Scale,
				Color:       actor.Placement.Color,
				BakedColors: actorColors,
			})
		}
	}

	return ret, nil
}

// EffectVolumePlacement describes a placed effect volume in a zone.
// splitBakedColors assigns a slice of actorColors to sp based on the vertex
// count of the sprite's PrimBuffer. Returns the updated color offset.
func splitBakedColors(sp *SpritePlacement, sprite *SimpleSprite, file *ObjFile, actorColors []Color32, offset int) int {
	if len(actorColors) == 0 {
		return offset
	}
	pb, err := sprite.GetPrimBuffer(file)
	if err != nil || pb == nil {
		return offset
	}
	verts := 0
	for _, vl := range pb.VertexLists {
		verts += len(vl.Vertices)
	}
	end := offset + verts
	if end > len(actorColors) {
		return offset
	}
	sp.BakedColors = actorColors[offset:end]
	return end
}

type EffectVolumePlacement struct {
	Pos            Point
	BBox           Box              // world-space bounding box (local translated by actor pos)
	EffectType     EffectVolumeType
	Density        float32
	Speed          float32
	SpawnOffsetY   float32
	SpawnOffsetXZ  float32
	Height         float32
	ParticleDictID int32
	Texture        *image.NRGBA     // particle texture from embedded Surface (nil if CSprite/missing)
	MeshVertices   []VertexList     // mesh data from embedded CSprite (flock birds)
}

// GetEffectVolumes extracts all EffectVolumeSprite placements from the zone.
// It iterates zone actors, resolves each actor's SpriteID, and if the resolved
// object is an EffectVolumeSprite, transforms its local bounding box by the actor position.
func (z *Zone) GetEffectVolumes(file *ObjFile) ([]EffectVolumePlacement, error) {
	actors, err := z.GetZoneActors(file)
	if err != nil {
		return nil, fmt.Errorf("loading zone actors for effect volumes: %w", err)
	}
	if actors == nil {
		return nil, nil
	}

	var volumes []EffectVolumePlacement
	for _, actor := range actors.GetActors() {
		obj, err := file.FindObject(actor.Placement.SpriteID)
		if err != nil || obj == nil {
			continue
		}
		ev, ok := obj.(*EffectVolumeSprite)
		if !ok {
			continue
		}

		// Translate local BBox to world space by adding actor position.
		pos := actor.Placement.Pos
		bbox := Box{
			MinX: ev.LocalBBox.MinX + pos.X,
			MinY: ev.LocalBBox.MinY + pos.Y,
			MinZ: ev.LocalBBox.MinZ + pos.Z,
			MaxX: ev.LocalBBox.MaxX + pos.X,
			MaxY: ev.LocalBBox.MaxY + pos.Y,
			MaxZ: ev.LocalBBox.MaxZ + pos.Z,
		}

		volumes = append(volumes, EffectVolumePlacement{
			Pos:            pos,
			BBox:           bbox,
			EffectType:     ev.EffectType,
			Density:        ev.Density,
			Speed:          ev.Speed,
			SpawnOffsetY:   ev.SpawnOffsetY,
			SpawnOffsetXZ:  ev.SpawnOffsetXZ,
			Height:         ev.Height,
			ParticleDictID: ev.ParticleDictID,
			Texture:        ev.Texture,
			MeshVertices:   ev.MeshVertices,
		})
	}

	if len(volumes) > 0 {
		log.Printf("esf: found %d effect volumes in zone", len(volumes))
	}
	return volumes, nil
}

// PointLightPlacement describes a placed point light in a zone.
type PointLightPlacement struct {
	Pos      Point
	Radius   float32
	Color    Color32F
	AlwaysOn bool // true = always visible (blue flames, explicit lights); false = dusk/night only
}

// GetPointLights extracts point lights from the zone.
// Sources: explicit PointLight actors AND particle placements (torches, lamps).
// Particle-based lights use the same flame positions computed by GetZoneParticles
// so light origins match the visible flame, not the actor base.
func (z *Zone) GetPointLights(file *ObjFile) ([]PointLightPlacement, error) {
	actors, err := z.GetZoneActors(file)
	if err != nil {
		return nil, fmt.Errorf("loading zone actors for point lights: %w", err)
	}
	if actors == nil {
		return nil, nil
	}

	// Collect explicit PointLight actors.
	var lights []PointLightPlacement
	for _, actor := range actors.GetActors() {
		obj, err := file.FindObject(actor.Placement.SpriteID)
		if err != nil || obj == nil {
			continue
		}
		if s, ok := obj.(*PointLight); ok {
			lights = append(lights, PointLightPlacement{
				Pos:      actor.Placement.Pos,
				Radius:   s.Radius,
				Color:    s.Color,
				AlwaysOn: true,
			})
		}
	}
	explicitCount := len(lights)

	// Derive lights from zone particle placements (flame positions at correct height).
	particles, err := z.GetZoneParticles(file)
	if err == nil {
		for _, zp := range particles {
			if zp.ParticleDef == nil {
				continue
			}
			g := zp.ParticleDef.Base.Gradient[0]
			luminance := g.R*0.299 + g.G*0.587 + g.B*0.114
			if luminance < 0.3 {
				continue
			}
			// Blue-tinted flames (B > R) are always on; warm torches are dusk only.
			blueFlame := g.B > g.R
			lights = append(lights, PointLightPlacement{
				Pos:      zp.Pos,
				Radius:   15.0,
				Color:    g,
				AlwaysOn: blueFlame,
			})
		}
	}

	if len(lights) > 0 {
		log.Printf("esf: found %d point lights in zone (%d explicit, %d from particles)",
			len(lights), explicitCount, len(lights)-explicitCount)
	}
	return lights, nil
}

// ZoneParticlePlacement describes a zone-placed particle effect (torch flame, etc).
type ZoneParticlePlacement struct {
	AlwaysOn    bool  // true = always visible (blue flames); false = dusk/night only
	Pos         Point
	Rot         Point              // composite rotation (actor + child member HPR)
	ParticleDef *ParticleDefinition
	Texture     *image.NRGBA // resolved particle texture (nil if not found)
}

// GetZoneParticles extracts ParticleSprite placements from the zone.
// These are fire/flame/smoke effects on torches, lamps, campfires, etc.
func (z *Zone) GetZoneParticles(file *ObjFile) ([]ZoneParticlePlacement, error) {
	actors, err := z.GetZoneActors(file)
	if err != nil {
		return nil, fmt.Errorf("loading zone actors for particles: %w", err)
	}
	if actors == nil {
		return nil, nil
	}

	// resolveParticleTex looks up the texture for a ParticleDefinition.
	resolveParticleTex := func(pd *ParticleDefinition) *image.NRGBA {
		if pd.TexDictID == 0 {
			return nil
		}
		texObj, err := file.FindObject(pd.TexDictID)
		if err != nil || texObj == nil {
			return nil
		}
		if surf, ok := texObj.(*Surface); ok && surf.Image != nil {
			return surf.Image
		}
		return nil
	}

	// addParticleSprite adds a ParticleSprite placement at the given position and rotation.
	addParticleSprite := func(ps *ParticleSprite, pos, rot Point, placements *[]ZoneParticlePlacement) {
		if ps.ParticleDef == nil {
			return
		}
		// Blue-tinted flames (B > R) are always on; warm torches are dusk only.
		g := ps.ParticleDef.Base.Gradient[0]
		*placements = append(*placements, ZoneParticlePlacement{
			AlwaysOn:    g.B > g.R,
			Pos:         pos,
			Rot:         rot,
			ParticleDef: ps.ParticleDef,
			Texture:     resolveParticleTex(ps.ParticleDef),
		})
	}

	var placements []ZoneParticlePlacement
	for _, actor := range actors.GetActors() {
		obj, err := file.FindObject(actor.Placement.SpriteID)
		if err != nil || obj == nil {
			continue
		}

		switch s := obj.(type) {
		case *ParticleSprite:
			addParticleSprite(s, actor.Placement.Pos, actor.Placement.Rot, &placements)

		case *GroupSprite:
			// GroupSprites contain child placements — some may be ParticleSprites.
			// Find sibling mesh height so we can place flame near the top.
			var modelMaxY float32
			for _, sp := range s.GetSprites() {
				childObj, err := file.FindObject(sp.SpriteID)
				if err != nil || childObj == nil {
					continue
				}
				if ss, ok := childObj.(*SimpleSprite); ok {
					if ss.BBox.MaxY > modelMaxY {
						modelMaxY = ss.BBox.MaxY
					}
				}
			}
			for _, sp := range s.GetSprites() {
				childObj, err := file.FindObject(sp.SpriteID)
				if err != nil || childObj == nil {
					continue
				}
				if cs, ok := childObj.(*ParticleSprite); ok {
					// Scale flame offset based on model height:
					// tall lamp posts (4+ units) → 90% height
					// small candles (<1 unit) → 30% height
					// wall torches (angled, rot.X ~1.57) → 100% (top of model)
					// interpolate linearly between small and tall
					var pct float32
					if math.Abs(float64(actor.Placement.Rot.X)) > 0.5 {
						// Wall-mounted torch — place at top
						pct = 1.0
					} else if modelMaxY >= 4.0 {
						pct = 0.90
					} else if modelMaxY <= 1.0 {
						pct = 0.30
					} else {
						// lerp: 0.30 at 1.0 → 0.90 at 4.0
						t := (modelMaxY - 1.0) / 3.0
						pct = 0.30 + t*0.60
					}
					flameY := modelMaxY * pct
					worldPos := Point{
						X: actor.Placement.Pos.X + sp.Pos.X,
						Y: actor.Placement.Pos.Y + sp.Pos.Y + flameY,
						Z: actor.Placement.Pos.Z + sp.Pos.Z,
					}
					compositeRot := Point{
						X: actor.Placement.Rot.X + sp.Rot.X,
						Y: actor.Placement.Rot.Y + sp.Rot.Y,
						Z: actor.Placement.Rot.Z + sp.Rot.Z,
					}
					// Wall-mounted torches: zero nozzle rotation so flame goes
					// straight up, and offset flame to torch tip (0.5 out from wall).
					// The torch extends along local -Z. Apply Y rotation to find
					// the world direction.
					if math.Abs(float64(actor.Placement.Rot.X)) > 0.5 {
						compositeRot = Point{}
						// Offset flame 0.5 units outward from wall.
						// Actor rot.X sign determines which side of wall:
						// positive → one direction, negative → opposite.
						dir := float32(1.0)
						if actor.Placement.Rot.X < 0 {
							dir = -1.0
						}
						worldPos.Z += dir * 0.5
					}
					addParticleSprite(cs, worldPos, compositeRot, &placements)
				}
			}

		case *LODSprite:
			// LODSprite may reference a ParticleSprite at its LOD level.
			if s.Level1 != nil {
				childObj, err := file.GetObject(s.Level1)
				if err == nil && childObj != nil {
					if ps, ok := childObj.(*ParticleSprite); ok {
						addParticleSprite(ps, actor.Placement.Pos, actor.Placement.Rot, &placements)
					}
				}
			}
		}
	}

	if len(placements) > 0 {
		log.Printf("esf: found %d zone particle effects", len(placements))
	}
	return placements, nil
}

// World represents a top-level world container (0x8100).
// PS2: ParseWorldObj (0x00439AB0). No body data — children are WorldBase + Zones.
type World struct {
	info *ObjInfo
}

func (w *World) Load(_ *ObjFile) error { return nil }
func (w *World) ObjInfo() *ObjInfo     { return w.info }

// WorldBaseHeader holds the world grid and spatial parameters (0x8220).
// PS2: ParseWorldBaseHeader (0x00439D88).
//
// Layout (ver != 0):
//
//	dictID(i32) + origin(3f) + bboxPoint(3f) + gridX(i32) + gridZ(i32) + treeDepth(i32) + zoneSize(f32) + bbox2Point(3f)
//
// Layout (ver == 0):
//
//	origin(3f) + bboxPoint(3f) + gridX(i32) + gridZ(i32) + zoneSize(f32) + bbox2Point(3f)
//	dictID defaults to 1, treeDepth defaults to 4.
type WorldBaseHeader struct {
	info      *ObjInfo
	DictID    int32
	Origin    Point
	BBoxPoint Point
	GridX     int32
	GridZ     int32
	TreeDepth int32
	ZoneSize  float32
	BBox2     Point
}

func (h *WorldBaseHeader) Load(file *ObjFile) error {
	file.Seek(h.info.Offset)
	ver := h.info.Version
	if ver != 0 {
		h.DictID = file.readInt32()
	} else {
		h.DictID = 1
	}
	h.Origin = file.readPoint()
	h.BBoxPoint = file.readPoint()
	h.GridX = file.readInt32()
	h.GridZ = file.readInt32()
	if ver != 0 {
		h.TreeDepth = file.readInt32()
	} else {
		h.TreeDepth = 4
	}
	h.ZoneSize = file.readFloat32()
	h.BBox2 = file.readPoint()
	return nil
}

func (h *WorldBaseHeader) ObjInfo() *ObjInfo { return h.info }

// WorldBase is a container for world spatial data (0x8200).
// PS2: ParseWorldBase (0x00439CA0).
// Children: WorldBaseHeader (0x8220), WorldTree (0x8230), WorldZoneProxies (0x8210),
// WorldRegions (0x8240, ver >= 2).
type WorldBase struct {
	info   *ObjInfo
	Header *WorldBaseHeader
}

func (w *WorldBase) Load(file *ObjFile) error {
	hdrInfo := w.info.Child(TypeWorldBaseHeader)
	if hdrInfo != nil {
		obj, err := file.GetObject(hdrInfo)
		if err != nil {
			return err
		}
		if obj != nil {
			w.Header = obj.(*WorldBaseHeader)
		}
	}
	return nil
}

func (w *WorldBase) ObjInfo() *ObjInfo { return w.info }

// ZoneProxy describes the location and bounds of a zone within the world.
type ZoneProxy struct {
	ZoneOffset int64
	BaseOffset int64
	Center     Point
	BBox       Box
	Name       string
}

// WorldZoneProxies holds bounding boxes and offsets for all zones in a world.
type WorldZoneProxies struct {
	info  *ObjInfo
	Zones []ZoneProxy
}

func (w *WorldZoneProxies) Load(file *ObjFile) error {
	file.Seek(w.info.Offset)
	numZones := file.readInt32()
	w.Zones = make([]ZoneProxy, numZones)
	for i := int32(0); i < numZones; i++ {
		z := &w.Zones[i]
		z.ZoneOffset = file.readInt64()
		z.BaseOffset = file.readInt64()
		_ = file.readInt32() // field_18
		z.Center = file.readPoint()
		z.BBox = file.readBox()
		if w.info.Version == 1 {
			name, err := file.readString()
			if err != nil {
				return fmt.Errorf("reading zone proxy %d name: %w", i, err)
			}
			z.Name = name
		}
	}
	return nil
}

func (w *WorldZoneProxies) ObjInfo() *ObjInfo { return w.info }

// GetZoneProxy finds a zone proxy by its zone offset, or nil if not found.
func (w *WorldZoneProxies) GetZoneProxy(offset int64) *ZoneProxy {
	for i := range w.Zones {
		if w.Zones[i].ZoneOffset == offset {
			return &w.Zones[i]
		}
	}
	return nil
}

// --- ZoneStaticTable (0x32C0) — per-room streaming index ---
//
// PS2: ParseZoneStaticTable at 0x004395B0. Child of ZoneBase.
// Maps each room index to file offsets/sizes for its ZoneRoomActors and
// ZoneRoomStaticLightings data. Used for lazy streaming on PS2.

// ZoneStaticTableEntry maps a room to its actor and lighting data offsets.
type ZoneStaticTableEntry struct {
	ActorsOffset   int64  // ESF byte offset to ZoneRoomActors, -1 if none
	ActorsSize     uint32 // byte size of the ZoneRoomActors block
	LightingOffset int64  // ESF byte offset to ZoneRoomStaticLightings, -1 if none
	LightingSize   uint32 // byte size of the ZoneRoomStaticLightings block
}

// ZoneStaticTable holds the per-room streaming index.
type ZoneStaticTable struct {
	info    *ObjInfo
	Entries []ZoneStaticTableEntry
}

func (z *ZoneStaticTable) ObjInfo() *ObjInfo { return z.info }

func (z *ZoneStaticTable) Load(file *ObjFile) error {
	file.Seek(z.info.Offset)
	count := file.readInt32()
	if count <= 0 || count > 4096 {
		return nil
	}
	z.Entries = make([]ZoneStaticTableEntry, count)
	for i := int32(0); i < count; i++ {
		z.Entries[i] = ZoneStaticTableEntry{
			ActorsOffset:   file.readInt64(),
			ActorsSize:     file.readUint32(),
			LightingOffset: file.readInt64(),
			LightingSize:   file.readUint32(),
		}
	}
	return nil
}

// --- Static Lighting (per-room baked vertex colors) ---
//
// Hierarchy: ZoneStaticLightnings(0x32B0) → ZoneRoomStaticLightings(0x3290)
//   → ZoneRoomStaticLightings2(0x32A0) [room index]
//   → ZoneRoomStaticLightings3(0x6030) → StaticLighting(0x6010)
//     → StaticLightingObj(0x6020) [header: primBufIndex + flags]
//     → ColorBuffer(0x1220) [per-vertex RGBA]

// StaticLightingEntry holds baked per-vertex colors for one actor in a room.
type StaticLightingEntry struct {
	ActorIndex int32     // index into the room's actor list (from StaticLightingObj)
	Flags      int32     // typically 1
	Colors     []Color32 // per-vertex RGBA baked lighting
	DictID     int32     // ColorBuffer DictID
}

// RoomStaticLighting holds all baked lighting for a room.
type RoomStaticLighting struct {
	RoomIndex int32
	Entries   []StaticLightingEntry
}

// GetStaticLighting extracts all per-room baked vertex color data from the zone.
func (z *Zone) GetStaticLighting(file *ObjFile) ([]RoomStaticLighting, error) {
	staticLtn := z.info.Child(TypeZoneStaticLightnings)
	if staticLtn == nil || len(staticLtn.Children) == 0 {
		return nil, nil
	}

	var result []RoomStaticLighting

	// Each child of ZoneStaticLightnings is a ZoneRoomStaticLightings (0x3290)
	// Note: type 0x3290 is labeled TypeZoneActors in types.go but here it's
	// used as ZoneRoomStaticLightings when parented under 0x32B0.
	for _, roomLtn := range staticLtn.Children {
		if roomLtn.Type != TypeZoneActors { // 0x3290
			continue
		}

		rsl := RoomStaticLighting{RoomIndex: -1}

		// Get room index from ZoneRoomStaticLightings2 (0x32A0)
		if ri := roomLtn.Child(TypeZoneRoomStaticLightings2); ri != nil {
			data := file.RawBytes(int(ri.Offset), int(ri.Size))
			if len(data) >= 4 {
				rsl.RoomIndex = int32(data[0]) | int32(data[1])<<8 | int32(data[2])<<16 | int32(data[3])<<24
			}
		}

		// Walk ZoneRoomStaticLightings3 → StaticLighting → StaticLightingObj + ColorBuffer
		roomLtn3 := roomLtn.Child(TypeZoneRoomStaticLightings3)
		if roomLtn3 == nil {
			continue
		}

		for _, slInfo := range roomLtn3.ChildrenOfType(TypeStaticLighting) {
			entry := StaticLightingEntry{ActorIndex: -1}

			// StaticLightingObj header (8 bytes: primBufIndex + flags)
			if slObj := slInfo.Child(TypeStaticLightingObj); slObj != nil {
				data := file.RawBytes(int(slObj.Offset), int(slObj.Size))
				if len(data) >= 8 {
					entry.ActorIndex = int32(data[0]) | int32(data[1])<<8 | int32(data[2])<<16 | int32(data[3])<<24
					entry.Flags = int32(data[4]) | int32(data[5])<<8 | int32(data[6])<<16 | int32(data[7])<<24
				}
			}

			// ColorBuffer — take the first one (multiple are variants)
			for _, cbInfo := range slInfo.ChildrenOfType(TypeColorBuffer) {
				obj, err := file.GetObject(cbInfo)
				if err != nil || obj == nil {
					continue
				}
				cb := obj.(*ColorBuffer)
				entry.Colors = cb.Colors
				entry.DictID = cb.DictID
				break // use first ColorBuffer
			}

			if len(entry.Colors) > 0 {
				rsl.Entries = append(rsl.Entries, entry)
			}
		}

		if len(rsl.Entries) > 0 {
			result = append(result, rsl)
		}
	}

	return result, nil
}
