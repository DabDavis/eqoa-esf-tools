# eqoa-esf-tools

Go library and CLI tools for working with EQOA's ESF/CSF game asset files. Parse, inspect, export, and modify zones, models, textures, animations, and spell effects from EverQuest Online Adventures (PS2).

## Tools

### `esfextract` — Inspect and Export

Browse the ESF object tree, list zones, export meshes to OBJ, decompress CSF files.

```bash
# List all zones in TUNARIA
esfextract -list TUNARIA.ESF

# Export Freeport (zone 84) to OBJ
esfextract -zone 84 -o freeport.obj TUNARIA.ESF

# Export collision mesh
esfextract -zone 84 -coll -o freeport_coll.obj TUNARIA.ESF

# Export character models
esfextract -chars -o models.obj CHAR.ESF

# Dump the object tree
esfextract -tree -depth 3 TUNARIA.ESF

# List actors (NPCs, objects) in a zone
esfextract -actors -zone 84 TUNARIA.ESF

# List sprite dictionary
esfextract -sprites CHAR.ESF

# Decompress a CSF to ESF
esfextract -decompress UI.CSF
```

### `esfpatch` — Zone Overlay Patches

Create zone overlay patches for the [eqoa-pipeline](https://github.com/DabDavis/eqoa-pipeline) serve system. Patches modify zone data without touching the ISO.

```bash
# Color all geometry in zone 84 red
esfpatch -zone 84 -red -o patches/ TUNARIA.ESF

# Scale terrain height by 1.5x
esfpatch -zone 84 -yscale 1.5 -o patches/ game.iso

# Swap the nearest actor's model
esfpatch -zone 84 -swap -x 25245 -z 15699 -newid 0x3950ce16 -o patches/ game.iso

# Raise terrain within 200 units of a point
esfpatch -zone 84 -raise 50 -x 25247 -z 15695 -radius 200 -o patches/ game.iso

# List zone byte ranges
esfpatch -list TUNARIA.ESF
```

Output: `zone_N.json` + `zone_N.bin` overlay pairs. The pipeline serve tool reads these at runtime.

### `esfimport` — OBJ Mesh Import

Replace zone geometry with OBJ meshes. Works by replacing existing PrimBuffer data in place (PS2 parser requires fixed node counts).

```bash
# List replaceable sprites in a zone
esfimport -list -zone 84 TUNARIA.ESF

# Replace a specific sprite's mesh
esfimport -obj cube.obj -replace 0 -zone 84 -o zone_84.esf TUNARIA.ESF

# Replace all terrain tiles
esfimport -obj terrain.obj -terrain -zone 84 -o zone_84.esf TUNARIA.ESF
```

### `esfrebuild` — Full ESF Rebuild

Rebuild a complete ESF file with zone replacements. Adjusts all size headers automatically.

```bash
esfrebuild -o TUNARIA-modified.ESF -zones patches/ TUNARIA.ESF
```

### `playlist-dump` — Animation Inspection

Dump CPlayList animation data from CSprite models. Useful for debugging animation mapping in custom clients.

```bash
# Dump a specific model's playlist by DictID
playlist-dump CHAR.ESF 0x82A69570
```

Shows playlist slot-to-animation mapping, speeds, play-once flags, and upper/lower body pairs.

## Library (`pkg/esf`)

The `pkg/esf` package can be imported by other Go programs:

```go
import "github.com/eqoa/pkg/pkg/esf"

// Open any ESF, CSF, or ISO file
file, err := esf.Open("TUNARIA.ESF")      // standalone ESF
file, err := esf.Open("game.iso")          // auto-detects ISO, reads TUNARIA
file, err := esf.Open("UI.CSF")            // auto-decompresses CSF

// Look up objects by DictID
obj, err := file.FindObject(0x82A69570)
cs := obj.(*esf.CSprite)
fmt.Println(cs.PlayList)                   // animation mappings
fmt.Println(cs.Animations)                 // skeletal animations

// Walk zones
for _, zone := range file.Zones {
    base, _ := zone.GetZoneBase(file)      // terrain, collision
    actors, _ := zone.GetZoneActors(file)  // NPCs, objects
    flora, _ := zone.GetRadialFlora(file)  // vegetation
}

// Export to OBJ
exporter := esf.NewObjExporter()
placements, _ := zone.GetSpritePlacements(file)
exporter.AddAll(placements, file)
exporter.Write("zone.obj")
```

### Supported Types

| Type | Code | Description |
|------|------|-------------|
| SimpleSprite | 0x1100 | Basic mesh with material |
| GroupSprite | 0x2100 | Multi-part mesh |
| HSprite | 0x2200 | Hierarchical (skeletal) sprite |
| CSprite | 0x2700 | Character sprite with animations, equipment slots |
| LODSprite | 0x2500 | Level-of-detail wrapper |
| Zone | 0x6100 | World zone container |
| PrimBuffer | 0x4000 | GPU vertex/index buffer |
| CollBuffer | 0x4100 | Collision mesh |
| Surface | 0x3000 | Texture data |
| Material | 0x3200 | Material properties |
| HSpriteAnim | 0x2600 | Skeletal animation |
| SpellEffect | 0x7100 | Spell visual effect |
| EffectVolume | 0xC300 | Fog, dust, leaf effects |

## Install

```bash
go install github.com/eqoa/pkg/cmd/esfextract@latest
go install github.com/eqoa/pkg/cmd/esfpatch@latest
go install github.com/eqoa/pkg/cmd/esfimport@latest
go install github.com/eqoa/pkg/cmd/esfrebuild@latest
```

Or build from source:

```bash
git clone https://github.com/DabDavis/eqoa-esf-tools.git
cd eqoa-esf-tools
go build ./cmd/esfextract
go build ./cmd/esfpatch
go build ./cmd/esfimport
go build ./cmd/esfrebuild
```

## File Formats

**ESF** (EverQuest Sprite File): Binary object tree with typed nodes. Each node has a type code (uint16), version, size, and optional DictID. Nodes contain geometry, textures, materials, animations, and zone layout.

**CSF** (Compressed Sprite File): zlib-compressed ESF. Tools auto-detect and decompress.

**ISO**: Game disc image. Tools auto-detect ISOs and read TUNARIA.ESF from the known sector offset (520000).

## Dependencies

None — Go stdlib only.
