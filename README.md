# wrpl-acmi

[中文](README.zh-CN.md) · [single-file release build](docs/standalone-build.md) · [dev branch](https://github.com/3794432/wrpl-acmi/tree/dev) · [upstream](https://github.com/maxsupermanhd/wrpl-inspector)

Export War Thunder replays to Tacview ACMI. wrpl-acmi inherits the Go replay library and dear-imgui inspector from upstream wrpl-inspector v3, and adds the headless `cmd/acmi` exporter. The exporter reads a client `.wrpl` file or one server replay's shard directory and writes Tacview ACMI 2.1. The project is published on the `dev` branch of `3794432/wrpl-acmi`; the Go module path remains `github.com/maxsupermanhd/wrpl-inspector/v3`.

## What it exports

- Airport runways selected by map and mission (`-airfields=true`). The catalogue covers 69 maps and 457 mission entries: 67 maps have usable source definitions. Conditional layouts require a matching replay radar identity. PhiPhi's eight race missions define air starts and no sourced land runway; Caribbean has no reliable mission or runway source. Radar labels remain the game abbreviations `AFF Radar` and `AFM Radar` (flight and main airfield radar), with full models in `WT_GroundModel`. See [airport positioning](docs/ACMI_AIRFIELDS.md) and [map coverage](docs/ACMI_AIRFIELDS_COVERAGE.md).
- File metadata (`Title`, `Author`, `RecordingTime`, category and provenance comments), event bookmarks, coalition groups, and body-relative radar designation angles. Estimated aircraft `HDG`, `TAS`, `Mach`, `IAS`, `AOA`, and three body-axis G values are enabled by default; set `-estimate-telemetry=false` to omit them. See [fields and estimation limits](docs/acmi-information.md).
- Aircraft and ground units use recorded positions. Aircraft carry flight-model attitude; ground units carry exact tracks, model and team evidence, and confirmable destruction records. Static buildings and fortifications without synchronized tracks can use a timestamped ECS construction position and the `WT_GroundStatic` provenance fields.
 - Missiles use actual synchronized positions, with speed, acceleration, turn rate, and G values estimated from the position sequence. Model candidates, static guidance type, and INS seconds remain in ACMI fields and the `INS x.xx s` label.
- Bombs with WeaponSync records export their actual position sequence. Bombs with only a construction record are marked `Release position only`; no flight path is invented.
- Countermeasures use native Flare/Chaff types when ammunition evidence identifies them. Unresolved releases default to Chaff with an explicit fallback marker; no type heuristic is used. Recorded positions take priority over labelled trajectory estimates. See [countermeasure identity](docs/ACMI_COUNTERMEASURES.md).
- Radar target designations become native Tacview `LockedTarget` properties, with geometric slant range and body-relative azimuth/elevation where the recorded positions support them. Re-export and enable Display Radar Locks to view the designation. This does not establish STT lock, radar power state, or a guidance mode.
- CSV diagnostics for track listings, missile flight evidence, countermeasure constructions, and radar lock changes are disabled by default. Each file is written only when its output path is supplied with `-list`, `-missile-flight-log`, `-countermeasure-log`, or `-radar-lock-log`.
- The static air-defence range table covers 255 models. Gun or machine-gun configurations with sourced radar absence get no threat circle (142 entries); ZA-35, ZSU-23-4, ZSU-37-2, and infrared or mixed-guidance SAMs remain. ZSU-37-2 displays the user-approved 2,000 m radius; its source field records that the pinned Datamine `attackMaxDist` was 4,000 m. These ranges are static model references, not replay radar state or a live engagement envelope.
- ACMI 2.1 remains the compatibility default. `-acmi-version 2.2` is an explicit opt-in.

In the replay set checked by the project, model and team evidence was linked for all 69 aircraft and 140 ground units; confirmable destruction records are retained only when the evidence supports them. Coverage for other game versions, maps, and server shards still needs to be checked per replay.

## Quick start

```
.\dist\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\dist\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

`wrpl-acmi.exe` is self-contained. You can copy it out of the package and run it from any directory; it reads its normal lookup tables, including `ecshashes`, from compiled-in data. A `data/` directory, installed terrain, the War Thunder client, Python, Go, Git and Tacview are not needed for export. Tacview is needed only to view the result. The package's `run.bat` remains an optional wrapper and changes to the package directory before invoking the executable.

Go's `flag` parser stops at the first positional argument, so flags go before the file. A single positional path also works:

```
.\wrpl-acmi.exe -out "C:\Replays\replay.acmi" "C:\Replays\replay.wrpl"
```

Do not combine `-in` with a positional path. For a server replay, put the shards from one match in a directory. Passing one server shard as a file is rejected.

When a `.wrpl` file or shard directory is dropped onto the EXE, an omitted `-out` writes `<input-base>.acmi` in the current working directory. The single-file build does not change this behavior. A directory missing its final segment writes `<directory-name>.partial.acmi` and marks the ACMI as incomplete; missing middle shards, duplicate parts, mixed matches, or corrupt compressed data are rejected.

`-ecshashes` defaults to `embedded`, so ECS component names and dependent replay metadata work without a data file. Pass an explicit JSON path to override the table, or `-ecshashes ""` to disable it. See the [single-file release guide](docs/standalone-build.md) for both executables and terrain packaging.

## Build from current dev

The exporter needs Go 1.25.6 or newer (`go.mod` currently declares Go 1.25.6). `build.ps1` always builds `wrpl-acmi.exe` and `wrpl-terrain-installer.exe`. A build without terrain input creates a terrain installer with logic only; it is not a complete offline terrain package. Terrain conversion from scratch additionally needs 64-bit Python 3.12, Git, and War Thunder's local client files.

From a clean PowerShell session, these commands fetch the current `dev` branch, prepare the converter environment, convert the maps, build both executables, and embed the terrain payload in the installer:

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

Use an actual 64-bit Python 3.12 interpreter for `-Python`; `python` is the default. `-OutputRoot` defaults to `dist\wrpl-acmi`. Omit `-WithTerrain`, `-GameRoot`, and `-DatamineRoot` to get a build whose terrain installer has logic only. The build does not install terrain into Tacview. If terrain conversion is interrupted, rerun with the same parameters plus `-ResumeTerrain`; a repeated `-WithTerrain` build rejects the existing nonempty terrain directory unless that switch is present.

To package an already converted 69-map directory without Python, Git, a game installation, or Tacview on the build machine:

```powershell
.\build.ps1 -TerrainSource ".\converted-terrain"
```

`-TerrainSource` and `-WithTerrain` are mutually exclusive. The converter's default `-out` remains the current working directory when omitted; packaging a directory does not rewrite its maps or change drag-and-drop output naming.

The runtime package contains both executables plus `run.bat`, `README.md`, `README.zh-CN.md`, `LICENSE`, the copied `docs/`, `data/`, and the `tools/terrain`, `tools/airfields`, and `tools/countermeasures` trees. Those copied folders support the project and its tools; `wrpl-acmi.exe` does not need them. When `-WithTerrain` succeeds, `terrain/` contains the 69 converted map sources. Terrain data is appended to the installer rather than stored in Git. `build-manifest.json` records output hashes and whether the installer includes data. The executables are built with CGO disabled. An older release may lag behind `dev`; build from the current branch when you need its behavior.

The [source-build workflow](https://github.com/3794432/wrpl-acmi/blob/dev/.github/workflows/source-build.yml) is configured for exporter and library checks, synthetic terrain decoding, airfield terrain sampling, installer checks, terrain format conversion, and a package build. Its coverage does not convert real terrain because that requires locally installed game files. No CI result is claimed here, and CI does not validate Tacview GUI loading or rendering.

## Maps and terrain

The default is `-terrain-layout geographic`. The authoritative catalog contains exactly 69 maps: 14 use reviewed WGS84 estimates (5 high confidence and 9 medium confidence), while 55 use the Atlas fallback. Keep this complete reference set synchronized across the converted terrain, exporter configuration, and installer metadata. Known bad placements for Sinai, Grand Canyon, and Israel are not used.

Terrain elevation XML and aircraft, missile, bomb, ground-unit, and countermeasure tracks share the same GeoReferences transform. To place an existing ACMI with the current terrain, re-export it directly from its original WRPL. `-terrain-layout atlas` is the current technical layout option. `-alt` remains available as a vertical offset in metres.

Install terrain with the standalone installer built alongside the exporter:

```powershell
.\dist\wrpl-acmi\wrpl-terrain-installer.exe
```

The default destination is `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`. Use `-source <converted-terrain>` to install an external directory, `-validate-only` to validate without writing the target, and `-profile auto` (the default), `1.9.4`, or `1.9.5` for height encoding. Automatic detection falls back to 1.9.4 when Tacview cannot be found. Use `-atlas` only with exporter `wrpl-acmi.exe -terrain-layout atlas`; geographic remains the default. The terrain installer needs system Windows PowerShell and several gigabytes of free disk space for temporary extraction and installation. Map `.data` files are not tracked in Git. See the [single-file release guide](docs/standalone-build.md) and [terrain tool guide](tools/terrain/README.md).

Terrain heights and geometry come from the installed War Thunder client's `levels/*.bin`. [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) supplies level configuration such as map bounds and water level; Datamine alone does not contain the compiled heights. The conversion workflow is pinned to Datamine commit `f7952e4291e9fd7b6fe38e84020dddef925c3457`. Geographic placement metadata is a separate MIT-licensed estimate from WT-MAP-ANALYZE. See the [terrain tool guide](tools/terrain/README.md#input-sources-and-licensing) for the full toolchain, source roles, package layout, and licensing details.

## Documentation

- [Export guide and command options](docs/acmi-export.md)
- [Single-file release and terrain installer](docs/standalone-build.md)
- [Server shards](docs/ACMI_SERVER_REPLAYS.md) · [Ground units](docs/ACMI_GROUND_UNITS.md) · [Airfields](docs/ACMI_AIRFIELDS.md) · [Airfield coverage](docs/ACMI_AIRFIELDS_COVERAGE.md)
- [Metadata, bookmarks and aircraft estimates](docs/acmi-information.md) · [Countermeasure identity](docs/ACMI_COUNTERMEASURES.md) · [Bombs](docs/ACMI_BOMBS.md) · [Radar locks](docs/ACMI_RADAR_LOCKS.md)
- [Static air-defence ranges](docs/ACMI_AIR_DEFENCE_RANGES.md) · [Missile models](docs/ACMI_MODELS.md) · [Guidance evidence](docs/ACMI_GUIDANCE.md)
- [Geographic terrain](docs/TERRAIN_GEOGRAPHIC.md) · [Atlas layout](docs/terrain-atlas.md) · [Upstream inspector notes](docs/README.md)

## Limits of the data

Missile speed, acceleration, and G values come from time differences between synchronized positions. Aircraft telemetry is estimated from recorded position and attitude under stated zero-wind, standard-atmosphere assumptions; IAS is an EAS approximation, and the body-axis G fields are specific-force estimates. Countermeasure paths without position records are explicitly estimated, and an unresolved countermeasure type defaults to Chaff without claiming identity. A bomb with only a construction record is a release-position marker. Static guidance types and air-defence ranges do not mean a target was locked, a radar was on, or an engagement was possible at a given moment. Runway markers preserve source geometry and coverage limits; they do not prove that a runway was active in a particular game state.

Terrain placement and track coordinates remain estimates where the catalogue says so. Tacview GUI loading and rendering are outside the validation described here.

This project and the `wrpl` library are released under the GNU Affero General Public License v3; see [LICENSE](LICENSE). Upstream attribution is preserved. Map and game assets belong to Gaijin Entertainment. Geographic placement is estimated from a pinned WT-MAP-ANALYZE commit and remains uncertain. This project is not affiliated with or endorsed by Gaijin.
