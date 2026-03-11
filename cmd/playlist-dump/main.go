package main

import (
	"fmt"
	"os"

	"github.com/eqoa/pkg/pkg/esf"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: playlist-dump <esf-file> <dictid-hex>\n")
		os.Exit(1)
	}

	file, err := esf.Open(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}

	var targetID uint32
	fmt.Sscanf(os.Args[2], "0x%x", &targetID)
	if targetID == 0 {
		fmt.Sscanf(os.Args[2], "%x", &targetID)
	}

	obj, err := file.FindObject(int32(targetID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "find: %v\n", err)
		os.Exit(1)
	}
	if obj == nil {
		fmt.Fprintf(os.Stderr, "DictID 0x%08X not found\n", targetID)
		os.Exit(1)
	}

	cs, ok := obj.(*esf.CSprite)
	if !ok {
		fmt.Fprintf(os.Stderr, "DictID 0x%08X is %T, not CSprite\n", targetID, obj)
		os.Exit(1)
	}

	fmt.Printf("=== CSprite DictID=0x%08X ===\n", targetID)
	fmt.Printf("  Race=%d Sex=%d SkelType=%d Scale=%.2f\n", cs.Race, cs.Sex, cs.SkelType, cs.DefaultScale)
	fmt.Printf("\n  PlayList: %d entries\n", len(cs.PlayList))
	for _, e := range cs.PlayList {
		fmt.Printf("    slot %2d: anim[0]=0x%08X  anim[1]=0x%08X  speed=%.2f  playOnce=%d\n",
			e.Index, uint32(e.AnimDictID[0]), uint32(e.AnimDictID[1]), e.Speed, e.PlayOnce)
	}
	fmt.Printf("\n  Animations: %d\n", len(cs.Animations))
	for i, a := range cs.Animations {
		fmt.Printf("    [%2d] DictID=0x%08X\n", i, uint32(a.DictID))
	}

	// Cross-reference: which playlist slots map to which animation indices
	fmt.Printf("\n  PlayList → Animation index mapping:\n")
	animIdx := make(map[int32]int)
	for i, a := range cs.Animations {
		animIdx[a.DictID] = i
	}
	for _, e := range cs.PlayList {
		idx0, ok0 := animIdx[e.AnimDictID[0]]
		idx1, ok1 := animIdx[e.AnimDictID[1]]
		s0, s1 := "MISSING", "MISSING"
		if e.AnimDictID[0] == 0 { s0 = "none" }
		if e.AnimDictID[1] == 0 { s1 = "none" }
		if ok0 { s0 = fmt.Sprintf("anim[%d]", idx0) }
		if ok1 { s1 = fmt.Sprintf("anim[%d]", idx1) }
		fmt.Printf("    slot %2d → upper=%s  lower=%s\n", e.Index, s0, s1)
	}
}
