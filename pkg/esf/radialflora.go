package esf

import (
	"fmt"
	"image"
	"math"
)

// RadialFloraDist is one plant of a flora set: ParseZoneFloraSets
// (viesfparse.cpp:2794) reads it from ZoneFloraDistArray (0x32F4) as
// m_id, m_set, m_p10, m_p14, m_p18, and VIRadialFloraSystem::CreateFloraInsts
// says what each one is.
type RadialFloraDist struct {
	Flags     uint32  // m_id: bit 0 tilts the plant to the ground's slope
	Model     int32   // m_set: index into ZoneRadialFlora.Models
	Weight    float32 // m_p10: plants per square unit, summed over the set
	BaseScale float32 // m_p14
	ScaleVar  float32 // m_p18: the scale is BaseScale·(1 ± ScaleVar)

	// Lo and Hi are VIZoneRadialFlora::Precalc's normalised cumulative weight
	// range [Lo, Hi) within the set — what CreateFloraInsts' binary search
	// draws a model against.
	Lo, Hi float32
}

// RadialFloraSet is a run of dists (ZoneFloraSetArray, 0x32F8): the flora
// byte on a collision triangle indexes this array.
type RadialFloraSet struct {
	First  int32   // m_model: the first dist
	Count  int32   // m_dist: how many
	Weight float32 // m_c: Precalc's sum of the dists' weights
}

// RadialFloraModel is one plant model: the FloraSprite (ParseFloraSpriteObj),
// its prim buffer, and the bounding sphere Precalc caches from it.
type RadialFloraModel struct {
	Sprite  *FloraSprite
	MatPal  *MaterialPalette // the sprite's own palette; a prim's Material indexes it
	Mesh    *FloraMesh       // nil when the prim buffer could not be read
	Texture *image.NRGBA
	BBox    Box
	// Sphere is VISphere::Init(bbox): the box centre and the half diagonal.
	SphereC Point
	SphereR float32
}

// ZoneRadialFlora holds all radial flora data for a zone.
// Parsed from ZoneFlora (0x32D0) which lives under ZoneBase (0x3200).
type ZoneRadialFlora struct {
	Models []RadialFloraModel
	Dists  []RadialFloraDist
	Sets   []RadialFloraSet
}

// FloraVertex is a flora prim buffer vertex: a position and a UV, nothing
// else. The 0x1230 buffer carries no normal and no colour.
type FloraVertex struct {
	X, Y, Z float32
	U, V    float32
}

// FloraPrim is one triangle strip and the palette material it is drawn with.
type FloraPrim struct {
	Material int
	Verts    []FloraVertex
}

// FloraMesh is a FloraSprite's prim buffer (0x1230), as ParseFloraPrimBuffer
// (viesfparse.cpp:5193) reads it.
type FloraMesh struct {
	Packing int32 // 7: float positions and UVs; 8: int16 scaled by 2^-vbits / 2^-uvbits
	Prims   []FloraPrim
}

// LoadFloraMesh reads a FloraSprite's 0x1230 prim buffer: an id, the
// packing, the material, prim and vertex counts and the two packing shifts,
// then per prim a vertex count, a material and the vertices. The generic
// PrimBuffer object does not know this type.
func LoadFloraMesh(file *ObjFile, fs *FloraSprite) (*FloraMesh, error) {
	info := fs.primInfo
	if info == nil {
		return nil, nil
	}
	end := info.Offset + int(info.Size)
	file.Seek(info.Offset)
	_ = file.readUint32() // id
	m := &FloraMesh{Packing: file.readInt32()}
	_ = file.readInt32() // numMats
	numPrims := int(file.readInt32())
	_ = file.readInt32() // numVerts
	vbits := file.readInt32()
	uvbits := file.readInt32()
	if m.Packing != 7 && m.Packing != 8 {
		return nil, fmt.Errorf("flora prim buffer: packing %d", m.Packing)
	}
	vscale := float32(1 / math.Pow(2, float64(vbits)))
	uvscale := float32(1 / math.Pow(2, float64(uvbits)))
	stride := 20
	if m.Packing == 8 {
		stride = 10
	}
	for p := 0; p < numPrims; p++ {
		if file.pos+8 > end {
			return nil, fmt.Errorf("flora prim buffer: truncated at prim %d", p)
		}
		nv := int(file.readInt32())
		prim := FloraPrim{Material: int(file.readInt32())}
		if nv < 0 || file.pos+nv*stride > end {
			return nil, fmt.Errorf("flora prim buffer: prim %d has %d vertices", p, nv)
		}
		prim.Verts = make([]FloraVertex, nv)
		for k := range prim.Verts {
			v := &prim.Verts[k]
			if m.Packing == 7 {
				v.X, v.Y, v.Z = file.readFloat32(), file.readFloat32(), file.readFloat32()
				v.U, v.V = file.readFloat32(), file.readFloat32()
			} else {
				v.X = float32(file.readInt16()) * vscale
				v.Y = float32(file.readInt16()) * vscale
				v.Z = float32(file.readInt16()) * vscale
				v.U = float32(file.readInt16()) * uvscale
				v.V = float32(file.readInt16()) * uvscale
			}
		}
		m.Prims = append(m.Prims, prim)
	}
	return m, nil
}

// GetRadialFlora loads radial flora data from the zone.
// Returns nil if the zone has no flora data.
func (z *Zone) GetRadialFlora(file *ObjFile) (*ZoneRadialFlora, error) {
	baseInfo := z.info.Child(TypeZoneBase)
	if baseInfo == nil {
		return nil, nil
	}
	floraInfo := baseInfo.Child(TypeZoneFlora)
	if floraInfo == nil {
		return nil, nil
	}

	rf := &ZoneRadialFlora{}

	// Flora sprites (0x32E0), one model per sub-object in file order: a
	// dist's Model indexes this array (ParseZoneFloraSpriteArray).
	spriteArrayInfo := floraInfo.Child(TypeZoneFloraSpriteArray)
	if spriteArrayInfo != nil {
		for _, fi := range spriteArrayInfo.Children {
			obj, err := file.GetObject(fi)
			fs, ok := obj.(*FloraSprite)
			if err != nil || !ok {
				rf.Models = append(rf.Models, RadialFloraModel{})
				continue
			}

			var tex *image.NRGBA
			mp, err := fs.GetMatPal(file)
			if err == nil && mp != nil && len(mp.Materials) > 0 {
				mat := mp.Materials[0]
				if len(mat.Layers) > 0 && mat.Layers[0].Surface != nil {
					tex = mat.Layers[0].Surface.Image
				}
			}
			mesh, _ := LoadFloraMesh(file, fs)

			b := fs.BBox
			c := Point{(b.MinX + b.MaxX) * 0.5, (b.MinY + b.MaxY) * 0.5, (b.MinZ + b.MaxZ) * 0.5}
			dx, dy, dz := b.MaxX-c.X, b.MaxY-c.Y, b.MaxZ-c.Z
			rf.Models = append(rf.Models, RadialFloraModel{
				Sprite:  fs,
				MatPal:  mp,
				Mesh:    mesh,
				Texture: tex,
				BBox:    b,
				SphereC: c,
				SphereR: float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz))),
			})
		}
	}

	// Distribution (0x32F0 → 0x32F4 dists, 0x32F8 sets).
	setsInfo := floraInfo.Child(TypeZoneFloraSets)
	if setsInfo != nil {
		distInfo := setsInfo.Child(TypeZoneFloraDistArray)
		if distInfo != nil {
			file.Seek(distInfo.Offset)
			count := file.readInt32()
			rf.Dists = make([]RadialFloraDist, max(int(count), 0))
			for i := range rf.Dists {
				rf.Dists[i] = RadialFloraDist{
					Flags:     file.readUint32(),
					Model:     file.readInt32(),
					Weight:    file.readFloat32(),
					BaseScale: file.readFloat32(),
					ScaleVar:  file.readFloat32(),
				}
			}
		}

		setInfo := setsInfo.Child(TypeZoneFloraSetArray)
		if setInfo != nil {
			file.Seek(setInfo.Offset)
			count := file.readInt32()
			rf.Sets = make([]RadialFloraSet, max(int(count), 0))
			for i := range rf.Sets {
				rf.Sets[i] = RadialFloraSet{
					First: file.readInt32(),
					Count: file.readInt32(),
				}
			}
		}
	}
	rf.Precalc()

	if len(rf.Models) == 0 {
		return nil, nil
	}

	return rf, nil
}

// Precalc is VIZoneRadialFlora::Precalc's weight half: each set's summed
// weight, then each dist's normalised cumulative [Lo, Hi) within its set, in
// float32 as the console does it.
func (rf *ZoneRadialFlora) Precalc() {
	for i := range rf.Sets {
		s := &rf.Sets[i]
		s.Weight = 0
		for j := s.First; j < s.First+s.Count; j++ {
			if j >= 0 && int(j) < len(rf.Dists) {
				s.Weight += rf.Dists[j].Weight
			}
		}
		inv := 1 / s.Weight
		var acc float32
		for j := s.First; j < s.First+s.Count; j++ {
			if j < 0 || int(j) >= len(rf.Dists) {
				continue
			}
			d := &rf.Dists[j]
			d.Lo = acc * inv
			acc += d.Weight
			d.Hi = acc * inv
		}
	}
}
