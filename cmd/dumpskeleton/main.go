// dumpskeleton dumps full bone hierarchy data from CHAR.ESF CSprites.
// Shows parent chains to determine if positions are absolute or local offsets.
package main

import (
	"fmt"
	"log"
	"math"

	"github.com/eqoa/pkg/pkg/esf"
)

const esfPath = "/home/sdg/claude-eqoa/extracted-assets/CHAR.ESF"

func main() {
	log.SetFlags(0)

	file, err := esf.Open(esfPath)
	if err != nil {
		log.Fatalf("Failed to open ESF: %v", err)
	}
	if err := file.BuildDictionary(); err != nil {
		log.Fatalf("Failed to build dictionary: %v", err)
	}

	// Dump full bone hierarchy for human male.
	dumpFull(file, 1893243078, "Human Male")
	// Dump full bone hierarchy for ogre.
	dumpFull(file, -2071956336, "Ogre")
	// Dump full for spider.
	dumpFull(file, -2145145487, "Spider/Large")
	// Dump full for quadruped.
	dumpFull(file, 335215986, "Quadruped")
}

func dumpFull(file *esf.ObjFile, dictID int32, label string) {
	obj, err := file.FindObject(dictID)
	if err != nil || obj == nil {
		fmt.Printf("=== %s (%d): NOT FOUND ===\n\n", label, dictID)
		return
	}

	fmt.Printf("=== %s (Model %d) ===\n", label, dictID)

	// Get mesh bounding box.
	var placements []*esf.SpritePlacement
	switch s := obj.(type) {
	case *esf.CSprite:
		placements = s.Placements
	case *esf.HSprite:
		placements, _ = s.LoadSprites(file)
	}

	bb := newBBox()
	for _, sp := range placements {
		sprite := sp.Sprite
		if sprite == nil {
			continue
		}
		pb, err := sprite.GetPrimBuffer(file)
		if err != nil || pb == nil {
			continue
		}
		for _, vl := range pb.VertexLists {
			for _, v := range vl.Vertices {
				p := esf.Point{X: v.X, Y: v.Y, Z: v.Z}
				sp.Transform(&p)
				bb.add(p.X, p.Y, p.Z)
			}
		}
	}

	if !bb.empty {
		cx := 0.5 * (bb.minX + bb.maxX)
		cz := 0.5 * (bb.minZ + bb.maxZ)
		fmt.Printf("  Mesh bbox: X=[%.3f, %.3f] Y=[%.3f, %.3f] Z=[%.3f, %.3f]\n",
			bb.minX, bb.maxX, bb.minY, bb.maxY, bb.minZ, bb.maxZ)
		fmt.Printf("  Mesh center: (%.3f, %.3f) yBottom=%.3f\n", cx, cz, bb.minY)
	}

	// Get hierarchy.
	var hier *esf.HSpriteHierarchy
	switch s := obj.(type) {
	case *esf.CSprite:
		hier = s.Hierarchy
	case *esf.HSprite:
		hier = s.Hierarchy
	}

	if hier == nil || len(hier.Nodes) == 0 {
		fmt.Printf("  No hierarchy!\n\n")
		return
	}

	fmt.Printf("\n  === ALL %d BONES ===\n", len(hier.Nodes))
	fmt.Printf("  %4s %6s %10s %10s %10s   %8s %8s %8s %8s   %5s\n",
		"idx", "parent", "X", "Y", "Z", "qX", "qY", "qZ", "qW", "scale")
	for i, n := range hier.Nodes {
		qStr := ""
		if n.Quat[0] != 0 || n.Quat[1] != 0 || n.Quat[2] != 0 || n.Quat[3] != 1.0 {
			qStr = " *"
		}
		fmt.Printf("  [%2d] %6d   %8.3f %8.3f %8.3f   %8.4f %8.4f %8.4f %8.4f   %.3f%s\n",
			i, n.ParentID, n.Pos.X, n.Pos.Y, n.Pos.Z,
			n.Quat[0], n.Quat[1], n.Quat[2], n.Quat[3],
			n.Scale, qStr)
	}

	// Trace parent chain for the head bone (highest Y near centerline).
	headIdx := -1
	var headY float32
	for i, n := range hier.Nodes {
		nearCenter := n.Pos.X > -0.1 && n.Pos.X < 0.1
		if nearCenter && (headIdx == -1 || n.Pos.Y > headY) {
			headY = n.Pos.Y
			headIdx = i
		}
	}

	if headIdx >= 0 {
		fmt.Printf("\n  Head bone parent chain (bone %d → root):\n", headIdx)
		accX, accY, accZ := float32(0), float32(0), float32(0)
		idx := headIdx
		for idx >= 0 && idx < len(hier.Nodes) {
			n := hier.Nodes[idx]
			accX += n.Pos.X
			accY += n.Pos.Y
			accZ += n.Pos.Z
			fmt.Printf("    bone[%d] parent=%d local=(%.3f, %.3f, %.3f) accumulated=(%.3f, %.3f, %.3f)\n",
				idx, n.ParentID, n.Pos.X, n.Pos.Y, n.Pos.Z, accX, accY, accZ)
			if n.ParentID < 0 {
				break
			}
			idx = int(n.ParentID)
		}
		fmt.Printf("  Head STORED position: (%.3f, %.3f, %.3f)\n", hier.Nodes[headIdx].Pos.X, hier.Nodes[headIdx].Pos.Y, hier.Nodes[headIdx].Pos.Z)
		fmt.Printf("  Head ACCUMULATED position: (%.3f, %.3f, %.3f)\n", accX, accY, accZ)
	}

	// Trace for right hand bone (max X).
	maxXBone := -1
	var maxXVal float32
	for i, n := range hier.Nodes {
		if maxXBone == -1 || n.Pos.X > maxXVal {
			maxXVal = n.Pos.X
			maxXBone = i
		}
	}
	if maxXBone >= 0 {
		fmt.Printf("\n  Right hand bone parent chain (bone %d → root):\n", maxXBone)
		accX, accY, accZ := float32(0), float32(0), float32(0)
		idx := maxXBone
		for idx >= 0 && idx < len(hier.Nodes) {
			n := hier.Nodes[idx]
			accX += n.Pos.X
			accY += n.Pos.Y
			accZ += n.Pos.Z
			fmt.Printf("    bone[%d] parent=%d local=(%.3f, %.3f, %.3f) accumulated=(%.3f, %.3f, %.3f)\n",
				idx, n.ParentID, n.Pos.X, n.Pos.Y, n.Pos.Z, accX, accY, accZ)
			if n.ParentID < 0 {
				break
			}
			idx = int(n.ParentID)
		}
		fmt.Printf("  RHand STORED position: (%.3f, %.3f, %.3f)\n", hier.Nodes[maxXBone].Pos.X, hier.Nodes[maxXBone].Pos.Y, hier.Nodes[maxXBone].Pos.Z)
		fmt.Printf("  RHand ACCUMULATED position: (%.3f, %.3f, %.3f)\n", accX, accY, accZ)
	}

	fmt.Println()
}

type bbox struct {
	minX, minY, minZ float32
	maxX, maxY, maxZ float32
	empty            bool
}

func newBBox() *bbox {
	return &bbox{
		minX: float32(math.MaxFloat32), minY: float32(math.MaxFloat32), minZ: float32(math.MaxFloat32),
		maxX: -float32(math.MaxFloat32), maxY: -float32(math.MaxFloat32), maxZ: -float32(math.MaxFloat32),
		empty: true,
	}
}

func (b *bbox) add(x, y, z float32) {
	b.empty = false
	if x < b.minX { b.minX = x }
	if y < b.minY { b.minY = y }
	if z < b.minZ { b.minZ = z }
	if x > b.maxX { b.maxX = x }
	if y > b.maxY { b.maxY = y }
	if z > b.maxZ { b.maxZ = z }
}
