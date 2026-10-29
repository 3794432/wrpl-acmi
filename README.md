# WRPL-ACMI

[简体中文](README.zh-CN.md) | [English](README.md)

> A single-file `.exe` tool that converts War Thunder `.wrpl` replays into Tacview-compatible `.acmi` files

<p align="center">
  <img src="docs/01.png" width="48%" />
  <img src="docs/02.png" width="48%" />
</p>

[Export guide](docs/acmi-export.md) | **[Download releases](https://github.com/3794432/wrpl-acmi/releases)** | [Documentation](docs/README.md) | [Build and release](docs/standalone-build.md)

## Features and purpose

### Basic features

* Convert [War Thunder](https://warthunder.com/) air battle replays into a format that can be played in [Tacview](https://www.tacview.net/)
* Convert a single local `.wrpl` replay or a directory of server replay `.wrpl` shards into an `.acmi` file
* Keep repeated aircraft sorties as separate objects, including respawns that reuse a unit UID or the same vehicle model
* Convert replays by dragging a file or directory onto the `.exe`, or pass an input path using options such as `-in`

### Advanced features

* Enable or disable exports of countermeasures, radar targets, and other data
* Export track lists, missile flight parameters, and other `.csv` data files
* Estimate heading, speed, G-load, and other native `.acmi` data from aircraft positions and attitudes
* Export recorded post-launch seeker states and aircraft radar modes using native ACMI properties and readable labels; see [sensor states and compatibility](docs/ACMI_SENSOR_STATES.md)
* Display missile models from full pinned AAM/AGM catalogues, including types absent from the sample replay (such as AIM-120D/B and R-77-1); use report/actual-loadout evidence for variants, or guidance-filtered carrier options for an explicitly inferred family
* Export recorded SAM flight tracks, with explicit launcher identity and coalition, for the [catalogued surface-to-air systems](docs/acmi-information.md#surface-to-air-missiles)
* Identify [air-to-ground missiles](docs/acmi-information.md#air-to-ground-missile-models) from weapon reports and exact owner loadouts, retaining unknown or mixed configurations
* Identify [bomb models](docs/acmi-information.md#bomb-models); preserve recorded flight tracks and add labelled initial-velocity/gravity estimates for release-only bombs when possible
* Unresolved SAM model names default to radar preference and the latest reviewed family variant, marked `Assumed`; use `-sam-model-fallback off` to retain only evidence-based naming
* Include recorded attacker vehicle, player and weapon in native ground-unit destruction events, including posthumous attacks when the report retains a valid player identity
* Omit unbound stationary initialization snapshots at convoy spawn points, avoiding temporary ground objects and misleading opening removal/death notifications

The project is based on [wrpl-inspector v3](https://github.com/maxsupermanhd/wrpl-inspector), retaining its Go replay parser library and dear-imgui graphical inspector alongside a headless `cmd/acmi` exporter. Development takes place on the [dev branch](https://github.com/3794432/wrpl-acmi/tree/dev). The Go module path remains `github.com/maxsupermanhd/wrpl-inspector/v3`.

## Quick start

### Downloads

Download the executables from the [latest release](https://github.com/3794432/wrpl-acmi/releases/latest):

* [`wrpl-acmi.exe`](https://github.com/3794432/wrpl-acmi/releases/latest/download/wrpl-acmi.exe) converts replays to `.acmi` files (required)
* [`wrpl-terrain-installer.exe`](https://github.com/3794432/wrpl-acmi/releases/latest/download/wrpl-terrain-installer.exe) installs terrain for Tacview (optional)

The local-game conversion installer described below is built from the current `dev` branch. Older releases may contain a terrain installer with pre-generated game data instead; check the release notes before following these installation instructions.

### Convert `.wrpl` replays to `.acmi` files

The following example runs the converter in PowerShell:

```powershell
.\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

You can run `wrpl-acmi.exe` on its own from another path. Its default lookup tables are compiled into the program. Converting a file does not require an adjacent directory, the local game, installed terrain, PowerShell, Python, Go, Git, or Tacview. Use Tacview to open the generated ACMI file.

For server replays, download them first, place the shards from the same match in one directory, and pass that directory as the input. The program sorts the shards by their headers and does not accept an individual server shard. See [server replay shard export](docs/ACMI_SERVER_REPLAYS.md) for details.

You can also convert a single client replay file or a server replay directory by dragging it onto `wrpl-acmi.exe`. Without `-out`, the program writes `<input filename>.acmi` in the current working directory. If a server replay is missing its final shard, it writes `<input directory name>.partial.acmi` and marks the replay as incomplete. The optional `run.bat` launcher changes to the package directory before running the program.

A single positional argument is also supported from the command line. All options must precede the input path, and `-in` cannot also be specified.

```powershell
.\wrpl-acmi.exe -out "C:\Replays\replay.acmi" "C:\Replays\replay.wrpl"
```

See the [export guide](docs/acmi-export.md) for complete options, output rules, and common issues.

### Install terrain for use in Tacview

Installing terrain is optional. Without the in-game terrain, the ACMI file still contains the exported replay objects, but Tacview's background terrain may not match the game map.

The standalone installer contains no pre-generated `.data` terrain. Put `wrpl-terrain-installer.exe` in your War Thunder game root (the directory containing `levels/` and `aces.vromfs.bin`), then run it:

```powershell
.\wrpl-terrain-installer.exe
```

By default, it reads the game directory containing the EXE, regardless of the current working directory. It converts the local assets at resolution 4096 and installs them to `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`. Game assets remain read-only. Portable CPython and four pinned packages are bundled in the EXE: users do not need to install Python, Go, or Git, and runtime operation needs no network or game-data download. The installation helpers use system Windows PowerShell.

The first run converts all 71 maps one at a time and can take several minutes. Low overall CPU usage is expected during this serial conversion. Reserve at least 8 GB of free space for the cache, installation, and temporary files at the default resolution. Later runs reuse unchanged converted maps; deleting the reported map cache means converting them again.

Use `-game <directory>` to select another game installation, `-cache <directory>` to change the default `%LOCALAPPDATA%\wrpl-acmi\terrain` cache, and `-resolution 4096` to select the conversion resolution. Map conversion results and logs are retained for resuming; source hashes are checked before reuse. At completion, the installer prints the cache path and a reminder to delete it manually when no longer needed. It does not clean caches automatically. `-profile auto`, `-profile 1.9.4`, and `-profile 1.9.5` select Tacview height encoding. `-validate-only` checks inputs without converting all maps or changing the installation destination; `-convert-only` creates the converted cache without installing. `-source <directory>` is a manual override for an existing converted terrain directory and skips automatic game reading. See the [terrain guide](tools/terrain/README.md) for commands.

After installation, fully quit and restart Tacview, then re-export previously exported replays from their original `.wrpl` files to ensure that tracks and terrain use the same coordinate configuration.

Exports and installations use the geographic layout by default. Of the 71 maps, 14 use reviewed WGS84 estimates and 57 use Atlas fallback coordinates. Atlas coordinates are for technical display, not real-world placement. To select Atlas explicitly, pair the installer's `-atlas` option with the exporter's `-terrain-layout atlas`; `-alt` changes only the vertical offset.

The terrain set includes the tournament maps Japanese Port (`avn_japan`) and Red Desert (`avg_red_desert`), both using Atlas fallback. Update both executables, run the new installer, and re-export the original replay to use these maps. Existing map coordinates are unchanged. The installer supports upgrading an older 69-map installation, including the Tacview 1.9.4 format.

The automatic installer reads terrain heights from local `levels/*.bin` files and boundaries, water levels, and other configuration from local `aces.vromfs.bin`. The separate developer pipeline can use [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) configuration. Geographic latitude and longitude placement comes from the reference data in [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE). The known catalogue remains 71 maps; this installer change adds no runway geometry.

## ACMI content and data types

An `.acmi` file converted from `.wrpl` replay data contains original replay records, estimates derived from those records, and static configuration data. Use each object's source fields to distinguish these three kinds of information. The default output is ACMI 2.1.

| Content | Data type |
| --- | --- |
| Aircraft and ground units | Positions come from the replay, and aircraft retain their attitude. Headings for moving ground units are inferred, and destroyed units are marked `Destroyed` before removal. Eligible static structures can use timestamped ECS construction positions. |
| Missiles and bombs | Actual positions are preferred where available, and missile motion parameters are calculated from positions. Bombs with only construction records use labelled initial-velocity/gravity trajectory estimates by default; `-estimate-bomb-trajectories=false` retains release-position markers. |
| Countermeasures | When reliable ammunition evidence distinguishes Flare and Chaff, the identified type is used; unresolved types are shown as Chaff. Countermeasure tracks are estimated. |
| Airfields and runways | Configured from the map, mission, and verifiable radar records. The catalogue covers 71 maps and 457 missions, with usable runway information for 67 maps. |
| Radar target designations | Writes Tacview's `LockedTarget` property and calculable distance and angle information. A designation may include tracking information but does not mean the radar is in tracking mode. |
| Air-defence reference ranges | Hard-coded references for 255 models. |
| Metadata and events | Includes titles, authors, timestamps, source notes, coalition groups, and replay event messages. Award and highlighted bookmarks are disabled by default; aircraft telemetry estimates are enabled by default. |

By default, `-events=kill,chat,join,damage,radar` outputs kills with clear attribution and weapon information, chat, player roster changes, Critical/Severe Damage, and radar target designation changes. Kills with missing or conflicting attribution are skipped, and an aircraft kill time must match its destruction time. Use `-events=none` to disable these events, or select a comma-separated subset. Award is disabled by default; add `award` or use `-events=all` to enable it. Highlighted bookmarks are disabled by default, retaining ordinary Chaff/Flare logs and avoiding duplicate highlighted rows; use `-bookmarks=true` when needed. See [event message details](docs/acmi-information.md#message-timeline).

CSV reports for track lists, missile flight parameters, countermeasure constructions, and radar designation changes are all optional outputs. Each report is generated only when its output path is supplied.

Aircraft speed, G-load, and related values are calculated under no-wind, standard-atmosphere conditions. IAS is an EAS approximation, and the three-axis G values estimate specific force. Static guidance types are independent of seeker operating state, and runway information cannot prove that the runway is active in the current game. See the documentation for details.

## Build from source

Building a Windows release package requires PowerShell and Go 1.25.6 or later. Fetch the source and run the build script in PowerShell:

```powershell
git clone --branch dev --single-branch https://github.com/3794432/wrpl-acmi.git
Set-Location .\wrpl-acmi
.\build.ps1
```

The default output directory is `dist\wrpl-acmi`, containing two independently usable programs:

* `wrpl-acmi.exe` converts replays; its default data is embedded.
* `wrpl-terrain-installer.exe` bundles the conversion runtime, reads the user's local game, and installs converted terrain in Tacview. It contains no pre-generated `.data` files.

By default, `build.ps1` fetches hash-pinned open-source CPython runtime and wheel artifacts at build time, using `tools/terrain/runtime-lock.json`; it downloads no game data. `-PythonRuntime` selects a prepared runtime directory, and `-RuntimeCache` selects the build cache. The former `-WithTerrain` and `-TerrainSource` options are rejected with a migration error; converted game assets are no longer packaged into the public installer.

The manual developer pipeline `tools/terrain/build-terrain.ps1` still uses Python and a pinned Datamine checkout, separately from end-user automatic local conversion. See the [terrain tool guide](tools/terrain/README.md#build-from-scratch) and [build and release guide](docs/standalone-build.md). Historical releases may lag behind dev; build from that branch when you need the current implementation.

## Responsibility, related projects, and license

This project is still in early development. You are responsible for the consequences of running builds and release artifacts from this repository. The repository's [source-build workflow](.github/workflows/source-build.yml) configures exporter, parser-library, and synthetic terrain checks. Building the standalone installer does not require game assets; converting real terrain requires the user's local game. Loading and rendering in the Tacview GUI also require separate verification.

This project is based on [wrpl-inspector](https://github.com/maxsupermanhd/wrpl-inspector) v3, retaining its Go replay parser library and dear-imgui graphical inspector alongside a headless `cmd/acmi` exporter. The Go module path remains `github.com/maxsupermanhd/wrpl-inspector/v3`.

Some project content comes from [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) and [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE).

This project and the [wrpl-inspector](https://github.com/maxsupermanhd/wrpl-inspector) library are released under the [AGPL-3.0 license](LICENSE). Upstream copyright and license notices are retained. Maps and game assets are owned by [Gaijin Entertainment](https://gaijinent.com/).

Bundled runtime and dependency notices are included in the extracted runtime's `LICENSES/` directory. Avoiding pre-generated game-data distribution reduces redistribution, but does not establish permission to extract, convert, use, or redistribute game assets.
