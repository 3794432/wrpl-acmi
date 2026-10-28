# Terrain converter build contract

This directory is the complete source for cataloging, decoding, writing, and
checking the Tacview terrain package. It contains no game assets, compiled
extensions, downloaded level configs, or generated catalogs.

## Standalone local-game runtime contract

The public `wrpl-terrain-installer.exe` bundles portable CPython, the converter,
reference configuration, and four pinned packages. It contains no pre-generated
`.data` terrain or game-data payload. Exact runtime and wheel versions and hashes
are pinned in `../runtime-lock.json`; the four packages are NumPy, Pillow, pyooz,
and zstandard. Users need no separately installed Python, Go, or Git, no network,
and no game-data download at runtime. Installation helpers use system Windows
PowerShell.

The automatic path reads local `levels/*.bin` and configuration from local
`aces.vromfs.bin`. By default the game root is the directory containing the EXE,
not the current working directory. `-game <dir>` overrides it. Game assets remain
read-only by default. This path does not require a Datamine checkout.

`-resolution` defaults to 4096. The default converted-map cache is
`%LOCALAPPDATA%\wrpl-acmi\terrain`, overridden by `-cache <dir>`. It retains
resumable map results and logs and checks source hashes before reuse. The default
installation target is `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`;
`-destination <dir>` overrides it. Height profiles are `-profile auto`, `1.9.4`,
and `1.9.5`.

`-validate-only` checks inputs with no destination changes. It does not convert
all maps or prove full conversion succeeds. `-convert-only` creates the converted
cache without installing. `-source <dir>` is a manual existing-converted-source
override and bypasses automatic game reading. The known catalogue remains 71
maps; this installer change adds no runway geometry.

The root `build.ps1` builds this self-contained installer by default. It fetches
hash-pinned open-source runtime and wheel artifacts at **build time**, never game
data. `-PythonRuntime <dir>` selects a prepared runtime and `-RuntimeCache <dir>`
selects the build artifact cache, separately from the runtime converted-map cache.
Legacy `-WithTerrain` and `-TerrainSource` options are rejected with a migration
error. Do not append converted game assets to the public installer.

The following sections describe the separate developer manual Python/Datamine
pipeline, `../build-terrain.ps1`, rather than prerequisites for end users.

## Manual pipeline requirements

- The manual pipeline targets Windows x64 and Python 3.12.
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
accepted), `AtlasReferences.json`, and `GeoReferences.json`. Once its inputs and
dependencies are prepared, the manual conversion performs no network access.
Level configs must match the local game assets. Fetching runtime artifacts during
the standalone EXE build is a separate build-time operation.

## Build a manual converted source

The authoritative package membership is `../AtlasReferences.json`: currently 71
IDs, including `avg_egypt_sinai`, `avn_japan`, and `avg_red_desert` and excluding the unavailable
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

The wrapper validates all 71 sources/configs, builds `catalog.json`, converts
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
hashes and sizes, exact 71-map directory membership, the complete manifest,
zero-origin XML, and the copied reference hashes. It rejects extra or missing
maps.

## Commands

Run from this directory or pass absolute script paths.

```powershell
# Catalog only
python -B catalog.py --client '<game>' --datamine '<datamine>' `
  --targets ..\AtlasReferences.json --expected-targets 71 `
  --output '<output>\catalog.json' --air-only

# Conversion only
python -B batch.py --catalog '<output>\catalog.json' `
  --references ..\AtlasReferences.json --expected-targets 71 `
  --output '<output>' --resolution 4096 --timeout 1200

# Summary, verification, and strict finalization
python -B summarize.py --root '<output>' --catalog '<output>\catalog.json'
python -B verify_outputs.py --root '<output>' --catalog '<output>\catalog.json'
python -B finalize.py --root '<output>' --references ..\AtlasReferences.json `
  --catalog '<output>\catalog.json' --expected-count 71 `
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

The bundled portable runtime retains CPython and dependency notices in its
extracted `LICENSES/` directory. Preserve those notices when preparing or
distributing a runtime. Omitting pre-generated game data reduces redistribution
but does not establish permission to extract, convert, use, or redistribute game
assets.

War Thunder and its assets belong to Gaijin Entertainment. This project is not
affiliated with or endorsed by Gaijin. No game binary or level configuration is
distributed here.

The wrapper derives target count from `AtlasReferences.json` (bounded to 1-128 maps). Japanese Port and Red Desert are explicit tournament targets despite their `avn_`/`avg_` prefixes. Their pinned level configs define the full 65536 m square; the HM2 patch in Red Desert covers only 4096 m square and must be overlaid on the full terrain.

Map conversion caches and logs are retained, including after installation. At completion the program prints the map-cache directory and a reminder to remove it manually when no longer needed. Removing it requires conversion again for a later installation. No automatic cache cleanup is performed.
