# Terrain extraction, conversion, and installation

[简体中文](README.zh-CN.md) · [Project README](../../README.md) · [Build and release](../../docs/standalone-build.md)

The standalone installer reads the user's local War Thunder assets, converts the 71 known maps, and installs them in Tacview. It bundles portable CPython and four pinned packages, with no pre-generated `.data` terrain. Game assets are not stored in Git or included in the new installer.

For normal use, go to [installation](#install-converted-terrain). The manual Python/Datamine workflow below is for developers and is separate from the installer's automatic local reader.

## Prerequisites

| Operation | Requirements |
| --- | --- |
| Build the executables | Windows PowerShell, Go 1.25.6 or newer, and source. The default build fetches hash-pinned open-source runtime/wheel artifacts at build time, not game data. |
| Run automatic local conversion and installation | Windows with system Windows PowerShell, the standalone installer, and local `levels/*.bin` plus `aces.vromfs.bin`. No user-installed Python, Go, Git, network, or game-data download at runtime. |
| Run manual developer conversion | 64-bit Python 3.12, converter requirements, local compiled game levels, and matching Datamine configuration. Git is used to obtain the pinned checkout below. |
| Export replays | Standalone `wrpl-acmi.exe`; terrain conversion or installation is optional. |

## Build from scratch

### Build the standalone programs

From the source root:

```powershell
.\build.ps1
```

This builds both EXEs, including the self-contained terrain installer. `tools/terrain/runtime-lock.json` pins the exact runtime version, four packages, and artifact hashes. `-PythonRuntime <directory>` selects a prepared runtime, and `-RuntimeCache <directory>` selects the build cache. No game or Datamine checkout is needed to build the installer.

The former `-WithTerrain` and `-TerrainSource` build options are rejected with a migration error. Converted game data is no longer packaged into the public installer. Use runtime `-source` for a manual existing-converted-source override.

### Prepare manual developer conversion

These steps apply only to `tools/terrain/build-terrain.ps1`. End users do not need this environment or a Datamine download.

```powershell
git clone --branch dev --single-branch https://github.com/3794432/wrpl-acmi.git
Set-Location .\wrpl-acmi
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r .\tools\terrain\converter\requirements.txt

git clone --depth 1 --filter=blob:none --no-checkout https://github.com/gszabi99/War-Thunder-Datamine.git .\War-Thunder-Datamine
git -C .\War-Thunder-Datamine fetch --depth 1 origin f7952e4291e9fd7b6fe38e84020dddef925c3457
git -C .\War-Thunder-Datamine sparse-checkout init --cone
git -C .\War-Thunder-Datamine sparse-checkout set aces.vromfs.bin_u/levels
git -C .\War-Thunder-Datamine checkout --detach FETCH_HEAD
```

Confirm that `python` is 64-bit Python 3.12. `requirements.txt` pins NumPy 2.3.5, Pillow 12.3.0, pyooz 0.0.8, and zstandard 0.25.0. The sparse checkout provides the 71 target level configs listed in `AtlasReferences.json`. Manual configs must match the local game assets.

### Convert manually

```powershell
.\tools\terrain\build-terrain.ps1 -GameRoot "C:\Games\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -OutputRoot ".\converted-terrain" -Python ".\.venv\Scripts\python.exe"
```

This generates a converted source directory, without building an installer or installing into Tacview.

| Manual wrapper option | Meaning |
| --- | --- |
| `-ValidateOnly` | Check inputs without writing converted output. |
| `-Resume` | Resume in the same output directory; without it a nonempty output is rejected. |
| `-Resolution` | Output resolution; defaults to 4096. |
| `-TimeoutSeconds` | Per-map timeout; defaults to 1200 seconds. |

See [BUILD-CONTRACT.md](converter/BUILD-CONTRACT.md) for the output safety rules, conversion commands, and verification contract. This manual wrapper's options are distinct from the standalone EXE's lowercase options.

## Package structure and format

The release contains `wrpl-acmi.exe` and `wrpl-terrain-installer.exe`, documentation, license notices, and supporting files. The installer carries the runtime and conversion tools, not converted maps; it needs no adjacent `data/`, `terrain/`, or `tools/` directory.

Conversion creates a local cache (default `%LOCALAPPDATA%\wrpl-acmi\terrain`), including resumable map results and logs. A complete converted source, including a manual source for `-source`, has this terrain structure:

```text
<converted-source>/
  AtlasReferences.json
  GeoReferences.json
  maps/<id>/<id>.data
  maps/<id>/metadata.json
  maps/<id>/CustomHeightmapList.xml
```

The catalogue has 71 maps. Rasters use signed little-endian 16-bit metres, ordered north-to-south and west-to-east. Metadata records dimensions, bounds, and SHA-256; source XML uses the zero-origin template that the installer places geographically or in Atlas. The manual wrapper also produces catalogues and verification reports described in the build contract. Synthetic `smalltests` terrain is for tests only.

## Input sources and licensing

| Input | Role | Source and license |
| --- | --- | --- |
| Compiled client terrain | Actual heights and geometry, including `levels/<id>.bin` | The user's War Thunder installation. Game assets belong to Gaijin Entertainment and are not distributed by this repository. |
| Local game configuration | Bounds, water levels, and related settings for automatic conversion | Read from the user's local `aces.vromfs.bin`; not downloaded at runtime. |
| Datamine level configuration | Map bounds, water level, and related `.blkx` settings | [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) at commit `f7952e4291e9fd7b6fe38e84020dddef925c3457`; repo data, no license file was found at that revision. Used locally as conversion input and not distributed. |
| Geographic references | Reviewed WGS84 estimates placed in `GeoReferences.json` | [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE) commit `c26c0069558079df6597a7bc147cb91391509f89`; MIT. See `georeference/UPSTREAM-NOTICE.md` and `georeference/UPSTREAM-MIT.txt`. |
| Python modules | Decoding and raster work | NumPy 2.3.5 is BSD-3-Clause; pyooz 0.0.8 is GPL-3.0-or-later; zstandard 0.25.0 is BSD-3-Clause; Pillow 12.3.0 is HPND. |

This project's code is AGPL-3.0. Each dependency and upstream source keeps its own license. Datamine supplies configuration only; the actual terrain heights must come from the installed game's compiled level files. Renaming a `.bin` or `.blkx` file to `.data` does not convert it.

Datamine is used only by the manual developer workflow above; the automatic installer reads local game configuration. The runtime and wheel pins are authoritative in `runtime-lock.json`. CPython and dependency notices are included in the extracted runtime's `LICENSES/` directory; preserve them when preparing or distributing the runtime. Converter attribution is in [THIRD-PARTY-NOTICES.md](converter/THIRD-PARTY-NOTICES.md). Reducing redistribution of game data does not establish permission to extract, convert, use, or redistribute game assets.

The GPL-3.0-or-later `ooz` decoder shipped by pyooz handles the Oodle-compressed terrain streams. A fresh pip environment needs no proprietary Oodle DLL or key.

## Install converted terrain

Put the standalone EXE in the War Thunder game root containing `levels/` and `aces.vromfs.bin`, then run it:

```powershell
.\wrpl-terrain-installer.exe
```

The default game root is the EXE directory, not the current working directory. Game assets remain read-only by default. The bundled runtime performs conversion offline; no user-installed Python, Go, Git, or game-data download is required. To run from elsewhere, select the game directory explicitly:

```powershell
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -resolution 4096
```

The default destination is `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`; use `-destination <directory>` for another location. The default resolution is 4096. `-cache <directory>` overrides `%LOCALAPPDATA%\wrpl-acmi\terrain`, which stores resumable maps and logs. Rerunning checks source hashes before reusing results. Keep the reported logs when investigating a failed conversion or installation.

`-profile` selects height encoding: `auto` detects Tacview and falls back to `1.9.4` if it cannot be found. Explicit values are `1.9.4` and `1.9.5`.

```powershell
# Validate inputs without changing the destination or converting all maps
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -validate-only

# Convert into the cache without installing
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -cache "D:\TerrainCache" -convert-only

# Manual existing-converted-source override: no automatic game reading
.\wrpl-terrain-installer.exe -source "C:\Terrain\converted-terrain"
```

`-validate-only` checks inputs without destination changes; it does not convert the full catalogue or prove every map can be converted. `-convert-only` creates the converted cache without installing. `-source` requires a complete converted directory with both reference JSON files, and bypasses the automatic local-game reader. The installer verifies converted metadata and SHA-256 before installation. With `-source`, validation checks that supplied source. Neither validation mode establishes Tacview GUI rendering.

### Coordinate layout

The reference catalogue contains 71 maps: 14 use reviewed geographic estimates and 57 use Atlas fallback. Converted terrain, exporter configuration, and installer metadata must use the same complete reference set.

Use installer `-atlas` with exporter `-terrain-layout atlas` when deliberately choosing technical coordinates. These two settings must come from the same layout; the default on both sides is geographic. The exporter's `-alt` option remains a vertical offset in metres. Full details are in [TERRAIN_GEOGRAPHIC.md](../../docs/TERRAIN_GEOGRAPHIC.md), [terrain-atlas.md](../../docs/terrain-atlas.md), and [coverage](georeference/coverage.json).

Restart Tacview after installation and re-export WRPL files with the current exporter. To place an existing ACMI with the current terrain, use its original WRPL as the input.

### Terrain height encoding

The standalone installer applies the encoding selected by `-profile auto`, `1.9.4`, or `1.9.5`. The source-tree format utility remains available when an existing installation needs inspection or a manual profile change:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\dist\wrpl-acmi\tools\terrain\Set-Tacview-Terrain-Format.ps1 -Profile 1.9.4
# To restore the native signed-height layout:
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\dist\wrpl-acmi\tools\terrain\Set-Tacview-Terrain-Format.ps1 -Profile 1.9.5
```

The default `-TerrainRoot` is `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`; supply that parameter for a custom installation. The 1.9.4 profile creates a root-level heightmap index and unsigned 16-bit height copies with an altitude offset of -32768 m. Original signed rasters remain unchanged. The 1.9.5 profile restores the native per-map signed-height definitions. Both profiles retain the same map placement and ACMI coordinates. The format utility's `-ValidateOnly` inspects the installed profile; the installer's `-validate-only` checks its source and does not write the target.

Restart Tacview after switching the installed format. Synthetic format and round-trip checks cover generated files; they do not establish GUI rendering for either Tacview version.

## Export

The exporter is a self-contained executable. Run it directly from the package or copy it elsewhere:

```powershell
.\dist\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\dist\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

The exporter reads a client `.wrpl` or one match's server shard directory. Its normal lookup data, including `ecshashes`, is compiled in, so neither `run.bat`, a `data/` directory, nor an installed terrain set is required to export. Tacview is needed only to view the ACMI. Use `-terrain-layout geographic` (the default) for the 14-estimate and 57-Atlas-fallback catalog, or `-terrain-layout atlas` with the installer's `-atlas` for the technical layout. A vertical altitude offset can be applied with `-alt`.

## Installer checks

These source-tree checks use synthetic fixtures and do not read game assets:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\test-install-all-terrains.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\geographic\New-Fixture.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\geographic\test-install-geographic-terrains.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\test-terrain-194-encoding.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\test-terrain-format.ps1
```

The presence of these checks does not assert that every test or build has passed in the current working tree.

## Tournament air maps

The 71-map catalogue includes Japanese Port (`avn_japan`) and Red Desert (`avg_red_desert`). Their ground/naval filename prefixes do not exclude them from the explicit targets. Both use Atlas fallback, including in the default geographic layout; existing map anchors are unchanged. Red Desert requires the full-map terrain decoder plus its partial HM2 overlay, not a stretched local height patch. The local-game installer change adds no runway geometry; source-backed runway geometry for these two maps has not yet been reviewed. Update both executables, run the local-game installer, and re-export the original replays.

Map conversion caches and logs are retained, including after installation. At completion the program prints the map-cache directory and a reminder to remove it manually when no longer needed. Removing it requires conversion again for a later installation. No automatic cache cleanup is performed.
