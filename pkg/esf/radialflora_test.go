package esf

import (
	"os"
	"testing"
)

const floraTestISO = "/home/sdg/claude-eqoa/EverQuest - Online Adventures - Frontiers (USA).iso"

// Every world's flora tables parse the way ParseZoneFloraSets reads them:
// sets index dists, dists index models, every model's 0x1230 prim buffer
// reads to its end, and Precalc's ranges tile [0, 1) within each set.
func TestRadialFloraTables(t *testing.T) {
	if _, err := os.Stat(floraTestISO); err != nil {
		t.Skip("no ISO")
	}
	for _, world := range []string{"TUNARIA.ESF", "RATHE.ESF", "ODUS.ESF"} {
		f, err := OpenISOFile(floraTestISO, world)
		if err != nil {
			t.Fatal(err)
		}
		root, _ := f.Root()
		w := root.Child(TypeWorld)
		var zones, withFlora, models, meshes, tris int
		flags := map[uint32]int{}
		packing := map[int32]int{}
		blends := map[int32]int{}
		var minScale, maxScale float32 = 1e9, -1e9
		for _, zi := range w.ChildrenOfType(TypeZone) {
			zones++
			obj, err := f.GetObject(zi)
			if err != nil {
				t.Fatal(err)
			}
			rf, err := obj.(*Zone).GetRadialFlora(f)
			if err != nil {
				t.Fatal(err)
			}
			if rf == nil {
				continue
			}
			withFlora++
			for _, m := range rf.Models {
				models++
				if m.Mesh == nil {
					continue
				}
				meshes++
				packing[m.Mesh.Packing]++
				for _, p := range m.Mesh.Prims {
					if len(p.Verts) > 2 {
						tris += len(p.Verts) - 2
					}
				}
				if mp, _ := m.Sprite.GetMatPal(f); mp != nil {
					for _, mat := range mp.Materials {
						if len(mat.Layers) > 0 {
							blends[mat.Layers[0].BlendMode]++
						}
					}
				}
			}
			for si, s := range rf.Sets {
				if s.First < 0 || int(s.First+s.Count) > len(rf.Dists) {
					t.Fatalf("%s set %d: dists [%d, +%d) of %d", world, si, s.First, s.Count, len(rf.Dists))
				}
				if s.Count > 0 && (rf.Dists[s.First].Lo != 0 || rf.Dists[s.First+s.Count-1].Hi < 0.9999) {
					t.Errorf("%s set %d: cumulative range [%v, %v)", world, si, rf.Dists[s.First].Lo, rf.Dists[s.First+s.Count-1].Hi)
				}
			}
			for _, d := range rf.Dists {
				flags[d.Flags]++
				if d.Model < 0 || int(d.Model) >= len(rf.Models) {
					t.Fatalf("%s: dist model %d of %d", world, d.Model, len(rf.Models))
				}
				lo, hi := d.BaseScale*(1-d.ScaleVar), d.BaseScale*(1+d.ScaleVar)
				if lo < minScale {
					minScale = lo
				}
				if hi > maxScale {
					maxScale = hi
				}
			}
		}
		t.Logf("%s: %d zones, %d with flora; %d models, %d meshes (%d tris), packing %v, layer-0 blends %v, dist flags %v, scale %.2f..%.2f",
			world, zones, withFlora, models, meshes, tris, packing, blends, flags, minScale, maxScale)
		if meshes != models {
			t.Errorf("%s: %d of %d flora meshes read", world, meshes, models)
		}
	}
}
