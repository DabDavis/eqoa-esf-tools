package esf

import "math"

type Vec2 struct {
	X, Y float32
}

type Point struct {
	X, Y, Z float32
}

func (p Point) Add(o Point) Point {
	return Point{p.X + o.X, p.Y + o.Y, p.Z + o.Z}
}

func (p Point) Sub(o Point) Point {
	return Point{p.X - o.X, p.Y - o.Y, p.Z - o.Z}
}

func (p *Point) AddTo(o Point) {
	p.X += o.X
	p.Y += o.Y
	p.Z += o.Z
}

func (p *Point) MultiplyWith(f float32) {
	p.X *= f
	p.Y *= f
	p.Z *= f
}

func (p *Point) Negate() {
	p.X = -p.X
	p.Y = -p.Y
	p.Z = -p.Z
}

// Rotate turns p by an HPR the way VIMatrix44::Transform (vimatrix44.cpp:394)
// builds an actor's matrix (VIActor::CalcObjectToWorld): rot.X is the
// heading, rot.Y the pitch and rot.Z the roll, and the matrix multiplies a
// ROW vector. With pitch and roll at 0 it is the turn about Y this used to
// be, and that is all it did: 19,888 Tunaria actors have a pitch or a roll.
func (p *Point) Rotate(rot Point) {
	si, ci := sincos32(rot.X)
	sj, cj := sincos32(rot.Y)
	sh, ch := sincos32(rot.Z)
	x, y, z := p.X, p.Y, p.Z
	p.X = x*(ch*ci+sh*sj*si) + y*(-sh*ci+ch*sj*si) + z*(cj*si)
	p.Y = x*(sh*cj) + y*(ch*cj) + z*(-sj)
	p.Z = x*(-ch*si+sh*sj*ci) + y*(sh*si+ch*sj*ci) + z*(cj*ci)
}

func sincos32(a float32) (float32, float32) {
	return float32(math.Sin(float64(a))), float32(math.Cos(float64(a)))
}

type Box struct {
	MinX, MinY, MinZ float32
	MaxX, MaxY, MaxZ float32
}

func NewBox() Box {
	return Box{
		MinX: math.MaxFloat32, MinY: math.MaxFloat32, MinZ: math.MaxFloat32,
		MaxX: -math.MaxFloat32, MaxY: -math.MaxFloat32, MaxZ: -math.MaxFloat32,
	}
}

func (b *Box) Add(x, y, z float32) {
	if x < b.MinX {
		b.MinX = x
	}
	if y < b.MinY {
		b.MinY = y
	}
	if z < b.MinZ {
		b.MinZ = z
	}
	if x > b.MaxX {
		b.MaxX = x
	}
	if y > b.MaxY {
		b.MaxY = y
	}
	if z > b.MaxZ {
		b.MaxZ = z
	}
}

func (b *Box) AddPoint(p Point) {
	b.Add(p.X, p.Y, p.Z)
}

func (b *Box) AddBox(o Box) {
	b.Add(o.MinX, o.MinY, o.MinZ)
	b.Add(o.MaxX, o.MaxY, o.MaxZ)
}

func (b Box) Center() Point {
	return Point{
		0.5 * (b.MinX + b.MaxX),
		0.5 * (b.MinY + b.MaxY),
		0.5 * (b.MinZ + b.MaxZ),
	}
}

func (b Box) Dimensions() Point {
	return Point{b.MaxX - b.MinX, b.MaxY - b.MinY, b.MaxZ - b.MinZ}
}

func (b Box) Size() float32 {
	d := b.Dimensions()
	s := d.X
	if d.Y > s {
		s = d.Y
	}
	if d.Z > s {
		s = d.Z
	}
	return s
}

func (b Box) IsEmpty() bool {
	return b.MinX > b.MaxX
}

func (b Box) ContainsXZ(p Point) bool {
	return b.MinX < p.X && b.MaxX > p.X && b.MinZ < p.Z && b.MaxZ > p.Z
}

// HashResourceID computes the PS2 VIHashResourceID hash for a named resource.
// This is the DictID used to look up sprites/resources in ESF files.
// Algorithm: hash = hash * 131 + byte, for each byte in the name.
func HashResourceID(name string) int32 {
	var hash int32
	for i := 0; i < len(name); i++ {
		hash = hash*131 + int32(int8(name[i]))
	}
	return hash
}
