# Terrain extraction, conversion, and installation

[简体中文](README.zh-CN.md) · [Project README](../../README.md)

The source tree includes the converter and the installer. The converter reads compiled terrain from a local War Thunder installation and level configuration from a pinned Datamine checkout. It writes a 69-map package that the installer validates and copies to Tacview. No game terrain assets are stored in Git.

## Prerequisites

- Windows and PowerShell.
- Go 1.25.6 or newer to build `wrpl-acmi` (`go.mod` currently declares Go 1.25.6).
- 64-bit Python 3.12 and the converter's pip requirements.
- Git and a local War Thunder client whose `levels/*.bin` files contain the terrain heights and geometry.
- A Datamine checkout at the compatible commit `f7952e4291e9fd7b6fe38e84020dddef925c3457`.
- Tacview only when installing the converted terrain.

## Build from scratch

The following PowerShell commands clone the current `dev` branch, create the Python environment, install the converter dependencies, pin Datamine, build the replay exporter together with all 69 converted maps, and put the result under `dist/wrpl-acmi`:

```powershell
git clone --branch dev --single-branch https://github.com/3794432/wrpl-acmi.git
Set-Location .\wrpl-acmi
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install --upgrade pip
.\.venv\Scripts\python.exe -m pip install -r .\tools\terrain\converter\requirements.txt
git clone --depth 1 --filter=blob:none --no-checkout https://github.com/gszabi99/War-Thunder-Datamine.git .\War-Thunder-Datamine
git -C .\War-Thunder-Datamine fetch --depth 1 origin f7952e4291e9fd7b6fe38e84020dddef925c3457
git -C .\War-Thunder-Datamine sparse-checkout init --cone
git -C .\War-Thunder-Datamine sparse-checkout set aces.vromfs.bin_u/levels
git -C .\War-Thunder-Datamine checkout --detach FETCH_HEAD
.\build.ps1 -WithTerrain -GameRoot "C:\Path\To\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -Python ".\.venv\Scripts\python.exe"
```

The sparse checkout selects only `aces.vromfs.bin_u/levels`; the converter needs the 69 target `.blkx` files listed in `AtlasReferences.json` from the pinned revision, not a full Datamine checkout. `requirements.txt` pins NumPy 2.3.5, Pillow 12.3.0, pyooz 0.0.8, and zstandard 0.25.0.

`build.ps1` accepts `-OutputRoot`, which defaults to `dist\wrpl-acmi`, plus `-WithTerrain`, `-ResumeTerrain`, `-GameRoot`, `-DatamineRoot`, and `-Python` (default `python`). With `-WithTerrain`, the script calls `tools/terrain/build-terrain.ps1` and includes its converted maps in `dist/wrpl-acmi/terrain`. The build does not install anything into Tacview. Use the explicit venv interpreter for `-Python` when that is not the default `python` on `PATH`.

If conversion stops partway through, rerun the same command with `-ResumeTerrain`; the flag requires `-WithTerrain` and is passed to the converter. Without it, a repeated terrain build refuses the existing nonempty output directory.

To build only the terrain package, invoke the converter wrapper directly after installing the requirements:

```powershell
.\tools\terrain\build-terrain.ps1 -GameRoot "C:\Path\To\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -OutputRoot ".\converted-terrain" -Python ".\.venv\Scripts\python.exe"
```

`-ValidateOnly` checks all inputs without writing output. After an interrupted run, use `-Resume` with the same output directory. `-Resolution` defaults to 4096 pixels and `-TimeoutSeconds` defaults to 1200 per map.

The artifact contains:

```text
dist/wrpl-acmi/
  wrpl-acmi.exe
  run.bat
  data/
  tools/terrain/                 # converter, installer, metadata and license notices
  terrain/                       # present after a successful -WithTerrain build
    GeoReferences.json
    maps/<id>/<id>.data
    maps/<id>/metadata.json
    maps/<id>/CustomHeightmapList.xml
```

The complete package contains 69 map directories. The raster is signed little-endian 16-bit metres, ordered north-to-south and west-to-east; `metadata.json` records dimensions, sampling bounds, and SHA-256. The XML starts from the zero-origin template expected by the installer. Synthetic `smalltests` terrain is only for installer tests.

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

Run from the repository root after a successful `-WithTerrain` build:

```powershell
.\dist\wrpl-acmi\tools\terrain\Install-All-Terrains.cmd -SourceRoot .\dist\wrpl-acmi\terrain -AtlasReferences .\dist\wrpl-acmi\tools\terrain\AtlasReferences.json
```

The installer validates all map metadata and hashes, rewrites the XML placement, and reuses the raster bytes. Its default destination is Tacview's `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`. `-Destination` can select another install location. The authoritative catalog contains exactly 69 maps: 14 use reviewed geographic estimates and 55 use the Atlas fallback. The complete reference set is required for normal geographic export and installation, so keep it synchronized between the converted terrain, exporter configuration, and installer metadata.

Use `-AtlasOnly` with exporter `-terrain-layout atlas` when deliberately choosing technical coordinates. The exporter's `-alt` option remains a vertical offset in metres. Full details are in [TERRAIN_GEOGRAPHIC.md](../../docs/TERRAIN_GEOGRAPHIC.md), [terrain-atlas.md](../../docs/terrain-atlas.md), and [coverage](georeference/coverage.json).

Restart Tacview after installation and re-export WRPL files with the current exporter. To place an existing ACMI with the current terrain, use its original WRPL as the input.

## Export

From the repository root:

```powershell
.\dist\wrpl-acmi\run.bat -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\dist\wrpl-acmi\run.bat -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

`run.bat` keeps its working directory inside the package so `data/ecshashes.json` and packaged defaults are found. The exporter reads a client `.wrpl` or one match's server shard directory. It writes Tacview ACMI 2.1; it does not extract or install terrain by itself. Use `-terrain-layout geographic` (the default) for the 14-estimate and 55-Atlas-fallback catalog, or `-terrain-layout atlas` with the installer's `-AtlasOnly` for the technical layout. A vertical altitude offset can be applied with `-alt`.

## Installer checks

These tests use synthetic fixtures and do not read game assets:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\test-install-all-terrains.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\geographic\New-Fixture.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\geographic\test-install-geographic-terrains.ps1
```
