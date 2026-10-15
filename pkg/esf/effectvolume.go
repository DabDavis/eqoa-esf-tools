package esf

import (
	"fmt"
	"image"
	"log"
)

// EffectVolumeType identifies the visual effect produced by a volume.
type EffectVolumeType int32

// PS2 VIEffectVolumeType enum (matches runtime jump table in VIEffectVolume::Process).
const (
	EffectTypeNone          EffectVolumeType = 0
	EffectTypeFallingLeaves EffectVolumeType = 1
	EffectTypeLightShafts   EffectVolumeType = 2
	EffectTypeGroundFog     EffectVolumeType = 3
	EffectTypeDustStorm     EffectVolumeType = 4
	EffectTypeFlock         EffectVolumeType = 5
)

func (t EffectVolumeType) String() string {
	switch t {
	case EffectTypeNone:
		return "None"
	case EffectTypeFallingLeaves:
		return "FallingLeaves"
	case EffectTypeLightShafts:
		return "LightShafts"
	case EffectTypeGroundFog:
		return "GroundFog"
	case EffectTypeDustStorm:
		return "DustStorm"
	case EffectTypeFlock:
		return "Flock"
	default:
		return fmt.Sprintf("Unknown(%d)", int32(t))
	}
}

// EffectVolumeSprite represents an ESF EffectVolumeSprite (type 0xC300).
// PS2: ParseEffectVolumeSpriteObj (0x0043C4A0), sprite type 14, dict type 26.
//
// Binary layout (3 children):
//
//	0xC310 (28 bytes): DictID(int32) + LocalBBox(6 floats: MinX,MinY,MinZ, MaxX,MaxY,MaxZ)
//	0xC320 (~2088 bytes): Particle visual — contains embedded Surface (texture) or CSprite
//	0xC330 (32-36 bytes): Effect params, in ParseEffectVolumeSpriteObj's order:
//	  type(int32), p34, p38, p30, p28, density (f32), surface DictID(int32),
//	  sprite DictID(int32), [ver>=2: flags(uint32)]
//
// The names are what VIEffectVolume does with each (vieffectvolume.cpp).
type EffectVolumeSprite struct {
	info       *ObjInfo
	DictID     int32
	LocalBBox  Box              // from 0xC310, local coordinates
	EffectType EffectVolumeType // from 0xC330
	// HourStart and HourEnd (m_p34, m_p38) bound the hours the volume shows,
	// with a five-minute ramp either side; [0, 0] is always. 110 Tunaria fog
	// volumes are [18, 7.75] — night only.
	HourStart float32
	HourEnd   float32
	// FadeRange (m_p30): full strength within 0.75 of it from the camera,
	// nothing beyond it. 100 in every Tunaria volume.
	FadeRange float32
	// Intensity (m_p28): the fog and dust alpha, the dust card size and life,
	// and the falling leaves' life.
	Intensity float32
	// Density (m_density): the particle count, and the (1-d)/2 + 1/2 alpha
	// factor of fog, dust and light shafts.
	Density        float32
	ParticleDictID int32        // m_surface: embedded texture in 0xC320 (PS2 dict type 1)
	CSpriteDictID  int32        // m_sprite: the flock's bird (PS2 dict type 9)
	Flags          uint32       // ver >= 2, m_flags: bit 1 = light shafts follow a 9-15 h schedule and lean
	Texture        *image.NRGBA // particle texture extracted from 0xC320 Surface child
	MeshVertices   []VertexList // mesh data from 0xC320 CSprite child (flock birds)
}

func (e *EffectVolumeSprite) Load(file *ObjFile) error {
	// Child 0xC310: DictID + local bounding box.
	hdr := e.info.Child(TypeEffectVolumeSpriteHeader)
	if hdr != nil {
		e.DictID = hdr.DictID
		file.Seek(hdr.Offset)
		if hdr.Size >= 28 {
			_ = file.readInt32() // DictID (already in hdr.DictID)
			e.LocalBBox = file.readBox()
		}
	}

	// Child 0xC330: effect parameters, read in ParseEffectVolumeSpriteObj's
	// order (viesfparse.cpp): t, p34, p38, p30, p28, density, surf, spr, [flags].
	params := e.info.Child(TypeEffectVolumeParams)
	if params != nil {
		file.Seek(params.Offset)
		if params.Size >= 32 {
			e.EffectType = EffectVolumeType(file.readInt32())
			e.HourStart = file.readFloat32()
			e.HourEnd = file.readFloat32()
			e.FadeRange = file.readFloat32()
			e.Intensity = file.readFloat32()
			e.Density = file.readFloat32()
			e.ParticleDictID = file.readInt32()
			e.CSpriteDictID = file.readInt32()
		}
		if params.Version >= 2 && params.Size >= 36 {
			e.Flags = uint32(file.readInt32())
		}
	}

	// Child 0xC320: particle visual — contains Surface (texture) OR CSprite (3D model).
	particle := e.info.Child(TypeEffectVolumeParticle)
	if particle != nil {
		// Try Surface first (fog, dust, leaves, light shafts).
		surfInfo := particle.Child(TypeSurface)
		if surfInfo != nil {
			obj, err := file.GetObject(surfInfo)
			if err == nil && obj != nil {
				if surf, ok := obj.(*Surface); ok && surf.Image != nil {
					e.Texture = surf.Image
				}
			}
		}
		// Try CSprite (flock birds) — extract mesh geometry.
		csInfo := particle.Child(TypeCSprite)
		if csInfo != nil {
			obj, err := file.GetObject(csInfo)
			if err == nil && obj != nil {
				if cs, ok := obj.(*CSprite); ok {
					// Walk CSprite's sub-sprites to find PrimBuffer mesh data.
					for _, child := range csInfo.Children {
						if child.Type == TypeCSpriteArray {
							for _, sub := range child.Children {
								pbInfo := sub.Child(TypeSkinPrimBuffer)
								if pbInfo == nil {
									pbInfo = sub.Child(TypePrimBuffer)
								}
								if pbInfo != nil {
									pbObj, err := file.GetObject(pbInfo)
									if err == nil && pbObj != nil {
										if pb, ok := pbObj.(*PrimBuffer); ok && len(pb.VertexLists) > 0 {
											e.MeshVertices = pb.VertexLists
											log.Printf("esf: EffectVolume CSprite mesh: %d strips, %d total verts",
												len(pb.VertexLists), pb.NumberOfVertices())
										}
									}
								}
							}
						}
					}
					_ = cs // CSprite loaded successfully
				}
			}
		}
	}

	texSize := ""
	if e.Texture != nil {
		b := e.Texture.Bounds()
		texSize = fmt.Sprintf(" tex=%dx%d", b.Dx(), b.Dy())
	}
	if len(e.MeshVertices) > 0 {
		texSize += " mesh=CSprite"
	}
	log.Printf("esf: EffectVolumeSprite dictID=0x%08x type=%s intensity=%.2f density=%.2f hours=[%.2f,%.2f]%s bbox=(%.1f,%.1f,%.1f)-(%.1f,%.1f,%.1f)",
		uint32(e.DictID), e.EffectType, e.Intensity, e.Density, e.HourStart, e.HourEnd, texSize,
		e.LocalBBox.MinX, e.LocalBBox.MinY, e.LocalBBox.MinZ,
		e.LocalBBox.MaxX, e.LocalBBox.MaxY, e.LocalBBox.MaxZ)

	return nil
}

func (e *EffectVolumeSprite) ObjInfo() *ObjInfo { return e.info }
