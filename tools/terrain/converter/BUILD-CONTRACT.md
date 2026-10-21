# Terrain converter build contract

This directory is the complete source for cataloging, decoding, writing, and
checking the Tacview terrain package. It contains no game assets, compiled
extensions, downloaded level configs, or generated catalogs.

## Requirements

- Windows x64 and Python 3.12 are the tested runtime.
- Install the exact dependencies from this directory:

```powershell
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r requirements.txt
```

`requirements.txt` pins NumPy 2.3.5, Pillow 12.3.0, pyooz 0.0.8, and
zstandard 0.25.0. The wrapper checks these versions and fails before writing.

Inputs are a local War Thunder installation containing `levels/*.bin`, a clone
or complete local extraction of War Thunder Datamine with
`aces.vromfs.bin_u/levels/*.blkx` (a flattened `levels` directory is also
accepted), `AtlasReferences.json`, and `GeoReferences.json`. The build performs
no network access. Level configs must match the local game assets.

## Build package

The authoritative package membership is `../AtlasReferences.json`: exactly 69
IDs, including `avg_egypt_sinai` and excluding the unavailable
`air_race_africa_canyon`. Catalog and batch results are checked against that
file; omissions or extra IDs fail the build.

```powershell
..\build-terrain.ps1 `
  -GameRoot '<War Thunder install>' `
  -DatamineRoot '<Datamine root>' `
  -OutputRoot '<new empty package directory>' `
  -Python '<venv>\Scripts\python.exe'
```

Optional parameters are `-Resolution` (default 4096, range 2-8192),
`-TimeoutSeconds` (default 1200), `-Resume`, `-SkipVerify`, and
`-ValidateOnly`. It performs no installation into Tacview AppData.

The wrapper validates all 69 sources/configs, builds `catalog.json`, converts
serially through one worker per map, writes `summary.json`, runs the independent
verifier by default, and creates `terrain-build.json`. Each worker installs a
2 GiB Windows Job object limit before importing NumPy. `-Resume` reuses only a
raster whose source SHA-256, level-config SHA-256, bounds, water level, format,
and raster SHA-256 still match.

`-ValidateOnly` checks inputs, target count, output-path safety, Python, and
dependencies. It writes no files and returns from the script without exiting
the caller.

The output root must be outside the game and Datamine trees, must neither be a
source directory nor contain one, and cannot be the repository root or its
ancestor. Without `-Resume`, an existing nonempty output root is rejected.

## Output

```text
<output>/
  AtlasReferences.json
  GeoReferences.json
  catalog.json
  manifest.json
  summary.json
  verification.json       # unless -SkipVerify
  overview.png            # unless -SkipVerify
  terrain-build.json
  maps/<id>/<id>.data
  maps/<id>/metadata.json
  maps/<id>/CustomHeightmapList.xml
```

Every `.data` is signed little-endian int16 metres, north-to-south and
west-to-east. XMLs use zero-origin world-coordinate corners; the installer
applies the selected geographic or Atlas placement later.

`avg_egypt_sinai` must finish as `render_mesh_hm2_overlay`: no lmap ray tracer,
`render_mesh` as the base surface, a positive HM2 overlay contribution, and
both HM2 and FRT blocks present. Finalization checks this mode, all raster
hashes and sizes, exact 69-map directory membership, the complete manifest,
zero-origin XML, and the copied reference hashes. It rejects extra or missing
maps.

## Commands

Run from this directory or pass absolute script paths.

```powershell
# Catalog only
python -B catalog.py --client '<game>' --datamine '<datamine>' `
  --targets ..\AtlasReferences.json --expected-targets 69 `
  --output '<output>\catalog.json' --air-only

# Conversion only
python -B batch.py --catalog '<output>\catalog.json' `
  --references ..\AtlasReferences.json --expected-targets 69 `
  --output '<output>' --resolution 4096 --timeout 1200

# Summary, verification, and strict finalization
python -B summarize.py --root '<output>' --catalog '<output>\catalog.json'
python -B verify_outputs.py --root '<output>' --catalog '<output>\catalog.json'
python -B finalize.py --root '<output>' --references ..\AtlasReferences.json `
  --catalog '<output>\catalog.json' --expected-count 69 `
  --special avg_egypt_sinai=render_mesh_hm2_overlay
```

The canonical regression command is:

```powershell
# cwd: tools/terrain/converter
python -B -m unittest discover -s tests
```

The suite is synthetic and does not convert the installed game. Set
`WT_TERRAIN_TEST_LEVEL` to a local `.bin` only when the optional read-only lmap
header test is wanted.

## Provenance and licenses

The decoder modules and tests are adapted from this project's prior
`terrain-export` pipeline. See [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md)
for module provenance, upstream format sources, attribution, and dependency
licenses. The repository's AGPL-3.0 text is retained in `LICENSES/AGPL-3.0.txt`;
`pyooz` is GPL-3.0-or-later, zstandard and NumPy are BSD-3-Clause, and Pillow is
HPND. Full texts supplied with those packages are in `LICENSES/`.

War Thunder and its assets belong to Gaijin Entertainment. This project is not
affiliated with or endorsed by Gaijin. No game binary or level configuration is
distributed here.
