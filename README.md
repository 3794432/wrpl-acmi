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
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi"
.\wrpl-acmi.exe -in ".\server-replays\session-id"
```

Go's `flag` parser stops at the first positional argument, so flags go before the file. A single positional path also works:

```
.\wrpl-acmi.exe -out "replay.acmi" "replay.wrpl"
```

Do not combine `-in` with a positional path. For a server replay, put the shards from one match in a directory. Passing one server shard as a file is rejected.

When a `.wrpl` file or shard directory is dropped onto the EXE, an omitted `-out` writes `<input-base>.acmi` in the current working directory. A directory missing its final segment writes `<directory-name>.partial.acmi` and marks the ACMI as incomplete; missing middle shards, duplicate parts, mixed matches, or corrupt compressed data are rejected. A runtime package with `run.bat` first changes the working directory to the package folder.

By default the exporter reads `data/ecshashes.json`. If it is missing, the program degrades: model names, teams, pilot links, and missile construction data may be unavailable.

## Build from current dev

`go.mod` requires Go 1.25.6. The headless exporter does not need GUI dependencies; build with CGO disabled:

```
$env:CGO_ENABLED = "0"
go build -o .\wrpl-acmi.exe ./cmd/acmi
go test ./cmd/acmi ./wrpl/...
```

On a POSIX shell the equivalent build is `CGO_ENABLED=0 go build -o wrpl-acmi ./cmd/acmi`. An older release may lag behind `dev`; build from the current branch when you need its behavior.

## Maps and terrain

The default is `-terrain-layout geographic`. The catalog contains 69 maps: 14 use reviewed WGS84 estimates (5 high confidence and 9 medium confidence), while 55 fall back to technical Atlas coordinates. Known bad placements for Sinai, Grand Canyon, and Israel are not used.

Terrain elevation XML and aircraft, missile, bomb, ground-unit, and countermeasure tracks share the same GeoReferences transform. Current builds do not guarantee coordinate compatibility with old ACMI files; re-export an existing ACMI from its WRPL. `-terrain-layout atlas` is an explicit technical layout. Only `-terrain-layout legacy` permits manual `-lat`/`-lon`/`-center-x`/`-center-z`.

Install terrain with (the converted output must contain GeoReferences.json):

```
.\tools\terrain\Install-All-Terrains.cmd -SourceRoot "<converted-terrain-output>" -AtlasReferences .\tools\terrain\AtlasReferences.json
```

Use `-AtlasOnly` with the same installer for the matching Atlas layout. Map `.data` files are not tracked in Git and terrain assets are not bundled; see the [terrain tool guide](tools/terrain/README.md) for installation and directory requirements.

Terrain heights come from an installed War Thunder client's `levels/*.bin`, not from
[War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine). That repository supplies level configuration such as map bounds and water level; it does not supply the compiled level geometry needed by this conversion. Geographic placement metadata is a separate input from WT-MAP-ANALYZE.

**This repository currently includes the terrain installers, not the full terrain extraction/conversion toolchain.** Cloning Datamine or building `cmd/acmi` does not create a terrain package. `-SourceRoot` must point to an already converted package, not the Datamine checkout or the game's `levels` directory. The installer validates and installs `.data`, metadata and XML; it does not decode `.bin` files. See [input sources and the required package layout](tools/terrain/README.md#input-sources--输入来源).

## Documentation

- [Current export guide](docs/acmi-export.md)
- [Server shards](docs/ACMI_SERVER_REPLAYS.md) · [Ground units](docs/ACMI_GROUND_UNITS.md) · [Bombs](docs/ACMI_BOMBS.md) · [Radar locks](docs/ACMI_RADAR_LOCKS.md)
- [Static air-defence ranges](docs/ACMI_AIR_DEFENCE_RANGES.md) · [Missile models](docs/ACMI_MODELS.md) · [Guidance evidence](docs/ACMI_GUIDANCE.md)
- [Geographic terrain](docs/TERRAIN_GEOGRAPHIC.md) · [Atlas and legacy](docs/terrain-atlas.md) · [Upstream inspector notes](docs/README.md)

## Limits of the data

Missile speed, acceleration, and G values come from time differences between synchronized positions. Countermeasure paths without position records are explicitly estimated. A bomb with only a construction record is a release-position marker. Static guidance types and air-defence ranges do not mean a target was locked, a radar was on, or an engagement was possible at a given moment. Visual rendering in Tacview was not part of this validation.

This project and the `wrpl` library are released under the GNU Affero General Public License v3; see [LICENSE](LICENSE). Upstream attribution is preserved. Map and game assets belong to Gaijin Entertainment. Geographic placement is estimated from a pinned WT-MAP-ANALYZE commit and remains uncertain. This project is not affiliated with or endorsed by Gaijin.
