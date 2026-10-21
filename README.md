# wrpl-acmi

[中文](README.zh-CN.md) · [dev branch](https://github.com/3794432/wrpl-acmi/tree/dev) · [upstream](https://github.com/maxsupermanhd/wrpl-inspector)

Export War Thunder replays to Tacview ACMI. wrpl-acmi inherits the Go replay library and dear-imgui inspector from upstream wrpl-inspector v3, and adds the headless `cmd/acmi` exporter. The exporter reads a client `.wrpl` file or one server replay's shard directory and writes Tacview ACMI 2.1. The project is published on the `dev` branch of `3794432/wrpl-acmi`; the Go module path remains `github.com/maxsupermanhd/wrpl-inspector/v3`.

## What it exports

- Aircraft and ground units use recorded positions. Aircraft carry flight-model attitude; ground units carry exact tracks, model and team evidence, and confirmable destruction records.
 - Missiles use actual synchronized positions, with speed, acceleration, turn rate, and G values estimated from the position sequence. Model candidates, static guidance type, and INS seconds remain in ACMI fields and the `INS x.xx s` label.
- Bombs with WeaponSync records export their actual position sequence. Bombs with only a construction record are marked `Release position only`; no flight path is invented.
- Countermeasures export recorded positions when present; otherwise a short trajectory is estimated from the release point and marked as estimated.
- Radar target designations become native Tacview `LockedTarget` properties. Re-export and enable Display Radar Locks to view them.
- The static air-defence range table covers 255 models. Gun or machine-gun configurations with verified radar absence get no threat circle (142 are in the audited set); ZA-35, ZSU-23-4, ZSU-37-2, and infrared or mixed-guidance SAMs remain. These ranges are static model references, not replay radar state or a live engagement envelope.

In the replay set checked by the project, model and team evidence was linked for all 69 aircraft and 140 ground units; confirmable destruction records are retained only when the evidence supports them. Coverage for other game versions, maps, and server shards still needs to be checked per replay.

## Quick start

```
cd .\dist\wrpl-acmi
.\run.bat -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\run.bat -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

You can also run `wrpl-acmi.exe` directly from the package directory. The package's `run.bat` keeps the working directory inside `dist\wrpl-acmi`, so the bundled `data/ecshashes.json` and `run.bat` defaults are found.

Go's `flag` parser stops at the first positional argument, so flags go before the file. A single positional path also works:

```
.\wrpl-acmi.exe -out "C:\Replays\replay.acmi" "C:\Replays\replay.wrpl"
```

Do not combine `-in` with a positional path. For a server replay, put the shards from one match in a directory. Passing one server shard as a file is rejected.

When a `.wrpl` file or shard directory is dropped onto the EXE, an omitted `-out` writes `<input-base>.acmi` in the current working directory. A directory missing its final segment writes `<directory-name>.partial.acmi` and marks the ACMI as incomplete; missing middle shards, duplicate parts, mixed matches, or corrupt compressed data are rejected. A runtime package with `run.bat` first changes the working directory to the package folder.

By default the exporter reads `data/ecshashes.json`. If it is missing, the program degrades: model names, teams, pilot links, and missile construction data may be unavailable.

## Build from current dev

The exporter needs Go 1.25.6 or newer (`go.mod` currently declares Go 1.25.6). Terrain conversion additionally needs 64-bit Python 3.12, Git, and War Thunder's local client files. The package build copies the repository's runtime data and, with `-WithTerrain`, converts terrain from the local game before adding it to the package.

From a clean PowerShell session, these commands fetch the current `dev` branch, prepare the converter environment, and build the package:

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

The sparse checkout selects only `aces.vromfs.bin_u/levels`; a full Datamine clone is not required. The converter validates the 69 target configurations listed in `AtlasReferences.json`. `tools/terrain/converter/requirements.txt` pins NumPy 2.3.5, Pillow 12.3.0, pyooz 0.0.8, and zstandard 0.25.0.

Use an actual 64-bit Python 3.12 interpreter for `-Python`; `python` is the default. `-OutputRoot` defaults to `dist\wrpl-acmi`. Omit `-WithTerrain`, `-GameRoot`, and `-DatamineRoot` for an exporter-only build. The build does not install terrain into Tacview. If terrain conversion is interrupted, rerun with the same parameters plus `-ResumeTerrain`; a repeated `-WithTerrain` build rejects the existing nonempty terrain directory unless that switch is present.

The runtime package contains `wrpl-acmi.exe`, `run.bat`, `data/`, and `tools/terrain/`. When `-WithTerrain` succeeds, it also contains `terrain/` with all 69 converted maps. The executable is built with CGO disabled. An older release may lag behind `dev`; build from the current branch when you need its behavior.

The [source-build workflow](https://github.com/3794432/wrpl-acmi/blob/dev/.github/workflows/source-build.yml) runs the Go tests, synthetic Python decoder tests, PowerShell installer tests, and an exporter-only package build. CI does not convert real terrain because that requires locally installed game files.

## Maps and terrain

The default is `-terrain-layout geographic`. The authoritative catalog contains exactly 69 maps: 14 use reviewed WGS84 estimates (5 high confidence and 9 medium confidence), while 55 use the Atlas fallback. Keep this complete reference set synchronized across the converted terrain, exporter configuration, and installer metadata. Known bad placements for Sinai, Grand Canyon, and Israel are not used.

Terrain elevation XML and aircraft, missile, bomb, ground-unit, and countermeasure tracks share the same GeoReferences transform. To place an existing ACMI with the current terrain, re-export it directly from its original WRPL. `-terrain-layout atlas` is the current technical layout option. `-alt` remains available as a vertical offset in metres.

Install terrain with (the converted output must contain GeoReferences.json):

```
.\dist\wrpl-acmi\tools\terrain\Install-All-Terrains.cmd -SourceRoot .\dist\wrpl-acmi\terrain -AtlasReferences .\dist\wrpl-acmi\tools\terrain\AtlasReferences.json
```

Use `-AtlasOnly` with the same installer for the matching Atlas layout. Map `.data` files are not tracked in Git and terrain assets are not bundled; see the [terrain tool guide](tools/terrain/README.md) for installation and directory requirements.

Terrain heights and geometry come from the installed War Thunder client's `levels/*.bin`. [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) supplies level configuration such as map bounds and water level; Datamine alone does not contain the compiled heights. The conversion workflow is pinned to Datamine commit `f7952e4291e9fd7b6fe38e84020dddef925c3457`. Geographic placement metadata is a separate MIT-licensed estimate from WT-MAP-ANALYZE. See the [terrain tool guide](tools/terrain/README.md#input-sources-and-licensing) for the full toolchain, source roles, package layout, and licensing details.

## Documentation

- [Current export guide](docs/acmi-export.md)
- [Server shards](docs/ACMI_SERVER_REPLAYS.md) · [Ground units](docs/ACMI_GROUND_UNITS.md) · [Bombs](docs/ACMI_BOMBS.md) · [Radar locks](docs/ACMI_RADAR_LOCKS.md)
- [Static air-defence ranges](docs/ACMI_AIR_DEFENCE_RANGES.md) · [Missile models](docs/ACMI_MODELS.md) · [Guidance evidence](docs/ACMI_GUIDANCE.md)
- [Geographic terrain](docs/TERRAIN_GEOGRAPHIC.md) · [Atlas layout](docs/terrain-atlas.md) · [Upstream inspector notes](docs/README.md)

## Limits of the data

Missile speed, acceleration, and G values come from time differences between synchronized positions. Countermeasure paths without position records are explicitly estimated. A bomb with only a construction record is a release-position marker. Static guidance types and air-defence ranges do not mean a target was locked, a radar was on, or an engagement was possible at a given moment. Visual rendering in Tacview was not part of this validation.

This project and the `wrpl` library are released under the GNU Affero General Public License v3; see [LICENSE](LICENSE). Upstream attribution is preserved. Map and game assets belong to Gaijin Entertainment. Geographic placement is estimated from a pinned WT-MAP-ANALYZE commit and remains uncertain. This project is not affiliated with or endorsed by Gaijin.
