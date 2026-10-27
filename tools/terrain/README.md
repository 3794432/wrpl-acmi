# Terrain extraction, conversion, and installation

[简体中文](README.zh-CN.md) · [Project README](../../README.md)

The terrain tools convert local War Thunder level data into a 71-map Tacview height set. The build script can then package that set in a standalone installer. Game terrain assets are not stored in Git.

If you already have an installer containing terrain data, go to [installation](#install-converted-terrain). The conversion environment is needed only when extracting or rebuilding terrain.

## Prerequisites

- Windows PowerShell and Go 1.25.6 or newer to build the executables (`go.mod` currently declares Go 1.25.6).
- To convert terrain from scratch: 64-bit Python 3.12, the converter's pip requirements, Git, a local War Thunder client with the compiled `levels/*.bin` files, and a Datamine checkout at `f7952e4291e9fd7b6fe38e84020dddef925c3457`.
- To package an existing converted directory with `-TerrainSource`: no Python, Git, game installation, or Tacview is required on the build machine.
- At install time: system Windows PowerShell (`powershell.exe`) and embedded or external converted terrain. Tacview is needed to view the result. Allow at least 8 GB of free space for the full map set at default resolution. The game, converter scripts, Python, Go, and Git are not required.

## Build from scratch

The following steps fetch dev, prepare conversion inputs, and build both executables with terrain included in the installer. First confirm that `python` is 64-bit Python 3.12. Replace the example `-GameRoot` with your game directory.

### Get the source and prepare Python

```powershell
git clone --branch dev --single-branch https://github.com/3794432/wrpl-acmi.git
Set-Location .\wrpl-acmi
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install --upgrade pip
.\.venv\Scripts\python.exe -m pip install -r .\tools\terrain\converter\requirements.txt
```

### Get the pinned level configuration

```powershell
git clone --depth 1 --filter=blob:none --no-checkout https://github.com/gszabi99/War-Thunder-Datamine.git .\War-Thunder-Datamine
git -C .\War-Thunder-Datamine fetch --depth 1 origin f7952e4291e9fd7b6fe38e84020dddef925c3457
git -C .\War-Thunder-Datamine sparse-checkout init --cone
git -C .\War-Thunder-Datamine sparse-checkout set aces.vromfs.bin_u/levels
git -C .\War-Thunder-Datamine checkout --detach FETCH_HEAD
```

### Convert and build

```powershell
.\build.ps1 -WithTerrain -GameRoot "C:\Path\To\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -Python ".\.venv\Scripts\python.exe"
```

The sparse checkout selects only `aces.vromfs.bin_u/levels`; the converter needs the 71 target `.blkx` files listed in `AtlasReferences.json` from the pinned revision, not a full Datamine checkout. `requirements.txt` pins NumPy 2.3.5, Pillow 12.3.0, pyooz 0.0.8, and zstandard 0.25.0.

`build.ps1` accepts `-OutputRoot` (default `dist\wrpl-acmi`), `-TerrainSource`, `-WithTerrain`, `-ResumeTerrain`, `-GameRoot`, `-DatamineRoot`, and `-Python` (default `python`). Every build produces `wrpl-acmi.exe` and `wrpl-terrain-installer.exe`. With `-WithTerrain`, the script calls `tools/terrain/build-terrain.ps1`, stages the converted maps in `dist/wrpl-acmi/terrain`, and appends them to the installer. Use the explicit venv interpreter for `-Python` when that is not the default `python` on `PATH`.

## Separate conversion and packaging

To package an existing 71-map directory:

```powershell
.\build.ps1 -TerrainSource ".\converted-terrain"
```

`-TerrainSource` and `-WithTerrain` are mutually exclusive. An ordinary `build.ps1` invocation creates a terrain installer with logic only, not a complete offline terrain package. The build does not install anything into Tacview. Packaging does not change the converter's output rule: omitting converter `-out` still writes to the current working directory.

If conversion stops partway through, rerun the same command with `-ResumeTerrain`; the flag requires `-WithTerrain` and is passed to the converter. Without it, a repeated terrain build refuses the existing nonempty output directory.

To build only the terrain package, invoke the converter wrapper directly after installing the requirements:

```powershell
.\tools\terrain\build-terrain.ps1 -GameRoot "C:\Path\To\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -OutputRoot ".\converted-terrain" -Python ".\.venv\Scripts\python.exe"
```

`-ValidateOnly` checks all inputs without writing output. After an interrupted run, use `-Resume` with the same output directory. `-Resolution` defaults to 4096 pixels and `-TimeoutSeconds` defaults to 1200 per map.

## Package structure and format

The main terrain-related files are shown below. The package also contains project documentation, license files, and other supporting tools.

```text
dist/wrpl-acmi/
  wrpl-acmi.exe
  wrpl-terrain-installer.exe        # payload included with -TerrainSource or -WithTerrain
  build-manifest.json
  run.bat
  data/                            # auxiliary package copy; the exporter embeds its defaults
  docs/
  tools/terrain/                   # converter, installer, metadata and license notices
  terrain/                         # staging source present after a -WithTerrain build
    AtlasReferences.json
    GeoReferences.json
    maps/<id>/<id>.data
    maps/<id>/metadata.json
    maps/<id>/CustomHeightmapList.xml
```

Both `AtlasReferences.json` and `GeoReferences.json` are required at the terrain source root for `-TerrainSource` packaging and installation.

The complete terrain source contains 71 map directories. The raster is signed little-endian 16-bit metres, ordered north-to-south and west-to-east; `metadata.json` records dimensions, sampling bounds, and SHA-256. The XML starts from the zero-origin template expected by the installer. Synthetic `smalltests` terrain is only for installer tests. The standalone EXE appends the payload; it does not depend on adjacent `data/`, `terrain/`, or `tools/` directories. See [Single-file release build](../../docs/standalone-build.md) for distribution details.

## Input sources and licensing

| Input | Role | Source and license |
| --- | --- | --- |
| Compiled client terrain | Actual heights and geometry, including `levels/<id>.bin` | The user's War Thunder installation. Game assets belong to Gaijin Entertainment and are not distributed by this repository. |
| Datamine level configuration | Map bounds, water level, and related `.blkx` settings | [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) at commit `f7952e4291e9fd7b6fe38e84020dddef925c3457`; repo data, no license file was found at that revision. Used locally as conversion input and not distributed. |
| Geographic references | Reviewed WGS84 estimates placed in `GeoReferences.json` | [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE) commit `c26c0069558079df6597a7bc147cb91391509f89`; MIT. See `georeference/UPSTREAM-NOTICE.md` and `georeference/UPSTREAM-MIT.txt`. |
| Python modules | Decoding and raster work | NumPy 2.3.5 is BSD-3-Clause; pyooz 0.0.8 is GPL-3.0-or-later; zstandard 0.25.0 is BSD-3-Clause; Pillow 12.3.0 is HPND. |

This project's code is AGPL-3.0. Each dependency and upstream source keeps its own license. Datamine supplies configuration only; the actual terrain heights must come from the installed game's compiled level files. Renaming a `.bin` or `.blkx` file to `.data` does not convert it.

The GPL-3.0-or-later `ooz` decoder shipped by pyooz handles the Oodle-compressed terrain streams. A fresh pip environment needs no proprietary Oodle DLL or key.

## Install converted terrain

The preferred release path is the standalone installer at the package root:

```powershell
.\dist\wrpl-acmi\wrpl-terrain-installer.exe
```

When the installer was built without a payload, or when installing a different converted directory, pass `-source`:

```powershell
.\dist\wrpl-acmi\wrpl-terrain-installer.exe -source "C:\Terrain\converted-terrain"
```

`-source` is optional for a payload-bearing installer and required for a logic-only build. The default destination is Tacview's `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`; use `-destination <directory>` for another location. The installer verifies source metadata and SHA-256 values before writing the target. `-validate-only` checks the source without writing the destination.

`-profile` selects height encoding: `auto` is the default and detects the installed Tacview version; if Tacview cannot be found, it falls back to `1.9.4`. Explicit values are `1.9.4` and `1.9.5`. Running the installer again can reuse its cache, but temporary extraction plus the installed maps can require several gigabytes of free disk space.

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

The 71-map catalogue includes Japanese Port (`avn_japan`) and Red Desert (`avg_red_desert`). Their ground/naval filename prefixes do not exclude them from the explicit build targets. Both use Atlas fallback, including in the default geographic layout; existing map anchors are unchanged. Red Desert requires the full-map terrain decoder plus its partial HM2 overlay, not a stretched local height patch. These entries add terrain support; source-backed runway geometry for these two maps has not yet been reviewed. Rebuild both executables and reinstall the complete terrain set before re-exporting these replays.
