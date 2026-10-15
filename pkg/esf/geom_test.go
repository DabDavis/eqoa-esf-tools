package esf

import (
	"math"
	"math/rand"
	"testing"
)

// transformRow is VIMatrix44::Transform(hpr, scale, t) (vimatrix44.cpp:394)
// as written, applied to a row vector: p·M.
func transformRow(p, hpr Point, scale float32, t Point) Point {
	s := func(a float32) float32 { return float32(math.Sin(float64(a))) }
	c := func(a float32) float32 { return float32(math.Cos(float64(a))) }
	si, ci := s(hpr.X), c(hpr.X)
	sj, cj := s(hpr.Y), c(hpr.Y)
	sh, ch := s(hpr.Z), c(hpr.Z)
	m := [3][3]float32{
		{(ch*ci + sh*sj*si) * scale, (sh * cj) * scale, (-ch*si + sh*sj*ci) * scale},
		{(-sh*ci + ch*sj*si) * scale, (ch * cj) * scale, (sh*si + ch*sj*ci) * scale},
		{(cj * si) * scale, (-sj) * scale, (cj * ci) * scale},
	}
	return Point{
		p.X*m[0][0] + p.Y*m[1][0] + p.Z*m[2][0] + t.X,
		p.X*m[0][1] + p.Y*m[1][1] + p.Z*m[2][1] + t.Y,
		p.X*m[0][2] + p.Y*m[1][2] + p.Z*m[2][2] + t.Z,
	}
}

func near3(a, b Point) bool {
	d := func(x, y float32) bool { return math.Abs(float64(x-y)) < 1e-3 }
	return d(a.X, b.X) && d(a.Y, b.Y) && d(a.Z, b.Z)
}

// A placement is the actor's matrix: heading, pitch and roll, then the scale
// and the position.
func TestPlacementIsTheActorMatrix(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	r := func(k float32) float32 { return (rng.Float32()*2 - 1) * k }
	for i := 0; i < 200; i++ {
		sp := SpritePlacement{
			Pos:   Point{r(1000), r(100), r(1000)},
			Rot:   Point{r(4), r(1), r(1)},
			Scale: 0.2 + rng.Float32()*3,
		}
		p := Point{r(10), r(10), r(10)}
		want := transformRow(p, sp.Rot, sp.Scale, sp.Pos)
		got := p
		sp.Transform(&got)
		if !near3(got, want) {
			t.Fatalf("%+v of %v: got %v, want %v", sp, p, got, want)
		}
	}
}

// Heading only, it is the turn about Y it always was; a pitch tilts Y.
func TestRotateHeadingOnly(t *testing.T) {
	p := Point{1, 2, 3}
	p.Rotate(Point{0.7, 0, 0})
	s, c := float32(math.Sin(0.7)), float32(math.Cos(0.7))
	if !near3(p, Point{3*s + 1*c, 2, 3*c - 1*s}) {
		t.Fatalf("heading only: %v", p)
	}
	q := Point{0, 0, 1}
	q.Rotate(Point{0, 0.5, 0})
	if q.Y > -0.4 {
		t.Fatalf("a pitch of 0.5 left +Z level: %v", q)
	}
}
