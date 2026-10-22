# wrpl-acmi ACMI export guide

[简体中文](acmi-export.zh-CN.md) · [Project README](../README.md) · [Single-file release build](standalone-build.md)

This page describes the current `dev` implementation of `cmd/acmi`. The exporter converts one War Thunder client replay or one server replay's shard directory into Tacview ACMI. Terrain placement is described in [geographic terrain](TERRAIN_GEOGRAPHIC.md) and [Atlas layout](terrain-atlas.md).

## Input and output

- A client replay is a single `.wrpl` file.
- A server replay is a directory containing the shards for one match. Shards are ordered by their packet headers; filenames do not need to be renumbered. A single server shard is rejected as a direct input.
- `-allow-incomplete=true` is the default. It exports a shard directory whose final segment is missing and marks the ACMI as incomplete. Set `-allow-incomplete=false` to require a complete shard sequence.
- Missing internal shards, a missing shard zero, duplicate shards, mixed sessions, and corrupt compressed data are rejected. Input files are never modified.

Shards are opened and decompressed one at a time. The process does not load every raw shard into memory at once, but the final memory requirement still depends on the size of the parsed tracks.

```powershell
.\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
.\wrpl-acmi.exe -out "C:\Replays\replay.acmi" "C:\Replays\replay.wrpl"
```

Go's flag parser stops at the first positional argument, so put flags before a positional input, or use `-in`. Do not combine `-in` with a positional path and do not pass more than one positional path.

When `-out` is omitted, the file is written in the current working directory. A client replay uses `<input-base>.acmi`; a complete shard directory uses `<directory-name>.acmi`; a directory missing its final segment uses `<directory-name>.partial.acmi`. Dropping one file or one directory onto the executable follows the same rule. The single-file executable does not change this output rule. The runtime package's `run.bat` remains an optional wrapper that changes to the package directory.

`-ecshashes` defaults to `embedded`, so normal ECS component names and dependent metadata work without an adjacent `data/` directory. An explicit JSON path overrides the embedded table; an empty value disables it. Normal lookup tables are compiled into `wrpl-acmi.exe`, which means export also does not need an installed terrain, the War Thunder client, Python, Git, or Tacview.

## Command-line options

| Option | Meaning |
| --- | --- |
| `-in` | Input `.wrpl` file or server shard directory |
| `-out` | Output `.acmi` path |
| `-allow-incomplete` | Export available shards when the tail is missing; default `true` |
| `-terrain-layout` | `geographic` (default) or `atlas` |
| `-map-id` | Explicit canonical map ID when automatic identification is unavailable |
| `-alt` | Vertical height offset in metres; it does not change horizontal placement |
| `-ecshashes` | `embedded` (default), an explicit JSON path override, or an empty value to disable the table |
| `-missile-models` | `embedded` (default), `off`, or a schema-1 model-table JSON path |
| `-missile-guidance` | `embedded` (default), `off`, or a schema-1 guidance-table JSON path |
| `-countermeasures` | Export aircraft countermeasure releases; default `true` |
| `-radar-locks` | Export radar target designations as native Tacview properties; default `true` |
| `-ground-units` | Export recorded `GroundModelPositions` tracks; default `true` |
| `-ground-models` | Associate ground models and derived threat ranges from explicit replay evidence; default `true` |
| `-airfields` | Export source-configured airport markers and runways; default `true` |
| `-estimate-telemetry` | Estimate aircraft heading, speed, Mach, IAS, AOA and body-axis G; default `true` |
| `-bookmarks` | Add event timeline bookmarks; default `true` |
| `-author` | ACMI recording author/operator; default `wrpl-acmi` |
| `-acmi-version` | `2.1` (default) or the explicit `2.2` opt-in |
| `-list` | Optional CSV path for track listings and current names |
| `-missile-flight-log` | Optional CSV path for missile flight parameters and raw motion evidence |
| `-countermeasure-log` | Optional CSV path for every countermeasure construction |
| `-radar-lock-log` | Optional CSV path for radar designation state changes |
| `-names` | Optional JSON overrides for `air:`, `ground:`, `missile:`, `bomb:` and related display names |

The four CSV reports are disabled by default. A CSV is written only when its corresponding option receives a nonempty path. `-estimate-telemetry` is also enabled by default; use `-estimate-telemetry=false` to omit the estimated aircraft fields.

## Exported content

### Aircraft, metadata and bookmarks

Aircraft use recorded positions and flight-model attitude. Enabled telemetry adds native `HDG`, `TAS`, `Mach`, `IAS`, `VerticalGForce`, `LongitudinalGForce`, `LateralGForce` and `AOA` properties to aircraft updates. `HDG` comes from recorded attitude. `TAS` uses recorded path speed under a zero-wind assumption. `Mach` uses a layered standard atmosphere, and `IAS` is the density-ratio EAS approximation `TAS * sqrt(rho / rho0)`. AOA comes from the zero-wind velocity direction relative to the body axes. The three G fields are body-axis specific-force estimates. Missing or rejected samples explicitly clear the estimated values.

The header includes map/mode/replay time in `Title`, the `-author` value, UTC `RecordingTime`, `Category=War Thunder`, `DataRecorder=wrpl-acmi`, and provenance comments describing the build, layout, georeference source, telemetry assumptions, countermeasure policy, and airfield limits. `ReferenceTime` remains the replay time origin.

Objects get coalition groups. Confirmed aircraft destruction, missile launch or first observation, bomb release or first observation, countermeasure release, and radar designation changes can produce timeline bookmarks. Duplicate bookmarks at one timestamp are removed. Radar designations do not prove STT lock or radar power state. See [metadata, bookmarks and aircraft estimates](acmi-information.md).

### Ground units, static objects and airfields

Ground units use recorded synchronized tracks. Explicit UID, model, and team evidence associates names and coalitions; unsupported identities remain generic, and destruction is applied only when the event evidence matches. `-ground-units=false` disables the bulk synchronized track path. `-ground-models=false` disables model-derived presentation and threat ranges while leaving independent coalition evidence available.

A structure or fortification without a synchronized track may use a successfully decoded, timestamped ECS construction translation when explicit static evidence exists. Conflicts, recycled identities, changing or nonfinite positions, and `dummy` mission placeholders are excluded. Accepted objects use `Ground+Static+Building`, retain model/team evidence, and expose `WT_GroundPositionSource=ECSConstructionTransform` and `WT_GroundStatic=1`. Existing synchronized tracks take precedence. See [ground units](ACMI_GROUND_UNITS.md) and [static ground objects](acmi-information.md#static-ground-objects).

The airport catalogue contains 69 map IDs and 457 mission entries. Sixty-seven maps have usable source-backed definitions; definition counts include mission, rank and conditional alternatives, so they do not represent airports active simultaneously. Conditional layouts require a matching main and flight-radar identity from the replay within the implemented geometric tolerance. Unconditional source placements can be exported directly; ambiguous or unsourced geometry is omitted rather than filled with a generic runway.

`air_race_phiphi_islands` (PhiPhi) has eight race missions defined with air starts and no sourced land runway. `caribbean_islands` (Caribbean) has no reliable mission or land-runway source in the bounded catalogue scan. These states do not claim that a map can never contain a runway in every game version. Airport markers preserve source commit/path, prefab, mission group, radar identity, position and elevation provenance in `WT_Airfield*` fields. Runway headings describe geometry, not an active landing direction. Radar labels remain `AFF Radar` and `AFM Radar` for airport flight and main radars; the full model remains in `WT_GroundModel`. See [airport positioning](ACMI_AIRFIELDS.md) and [coverage by map](ACMI_AIRFIELDS_COVERAGE.md).

### Missiles, bombs and countermeasures

Missile tracks retain synchronized positions. Speed, acceleration, turn rate and G are derived from position time differences, with model candidates, static guidance type and INS seconds carried in ACMI fields and the `INS x.xx s` label. These are playback estimates, not decoded per-frame flight-model values. See [missile models](ACMI_MODELS.md) and [guidance evidence](ACMI_GUIDANCE.md).

Bombs with `WeaponSync` records export their recorded positions. A bomb with only a construction record is a `Release position only` marker at the release location; no missing flight path or impact time is invented. See [bombs](ACMI_BOMBS.md).

Countermeasures use native Flare or Chaff types only when owner ammunition evidence identifies a single active type. Unresolved and mixed releases default to Chaff with `WT_CountermeasureTypeConfidence=fallback` and an `UnknownAsChaff` source marker. That is an explicit display policy, not replay identification. Recorded positions take priority; otherwise a short trajectory is generated and marked as estimated. See [countermeasure identity](ACMI_COUNTERMEASURES.md).

### Radar designations and air-defence ranges

Radar target designations use native `LockedTarget`, `LockedTarget2` through `LockedTarget9`, `LockedTargetMode` and `LockedTargetRange` fields. Slant range comes from current or up-to-one-second-old positions. When geometry is available, `LockedTargetAzimuth` and `LockedTargetElevation` are body-relative degrees, positive right and positive up. Unlocks, invalid geometry and target destruction clear the applicable fields. These are designation records, not proof of STT lock, radar mode, or weapon guidance. See [radar designations](ACMI_RADAR_LOCKS.md).

The embedded static threat table has 255 air-defence models. Gun-only entries with sourced radar absence emit no threat circle; 142 entries meet that policy. Radar-equipped guns, ZA-35, ZSU-23-4, ZSU-37-2, and infrared or mixed-guidance SAM systems remain. ZSU-37-2 exports the user-approved 2,000 m display radius, while `WT_EngagementRangeSource` records the pinned Datamine `attackMaxDist` value of 4,000 m. The circle is a model reference and does not reflect a live radar state or engagement envelope. See [static air-defence ranges](ACMI_AIR_DEFENCE_RANGES.md).

## Maps and terrain

The default layout is `-terrain-layout geographic`. The authoritative map catalogue contains 69 maps: 14 use reviewed WGS84 estimates (five high confidence and nine medium confidence), while 55 use the Atlas fallback. Terrain elevation XML and aircraft, missile, bomb, ground-unit and countermeasure tracks share the same GeoReferences transform. Re-export an original WRPL to place an existing ACMI with the current terrain catalogue. `-alt` only adds a vertical offset.

`-terrain-layout atlas` is the technical alternate layout and pairs with `-atlas` in the standalone installer. Map `.data` files are not stored in Git, and the source repository does not include terrain assets. A payload-bearing installer contains the converted maps; it does not require `terrain/`, `data/`, converter scripts, the game, Python, Go, or Git at install time. Tacview is needed only to view the ACMI. Without War Thunder terrain installed, Tacview may use its default ocean or background terrain, but that backdrop does not change the exported replay objects. See [geographic terrain](TERRAIN_GEOGRAPHIC.md), [Atlas layout](terrain-atlas.md), the [single-file release guide](standalone-build.md), and the [terrain conversion and installation guide](../tools/terrain/README.md).

## Build and checks

The exporter requires Go 1.25.6 or newer; `go.mod` currently declares version 1.25.6. From the repository root, `build.ps1` builds `wrpl-acmi.exe` and `wrpl-terrain-installer.exe`. A build without terrain options produces a terrain installer with logic only. Convert and embed terrain with:

```powershell
.\build.ps1 -WithTerrain -GameRoot "C:\Path\To\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -Python ".\.venv\Scripts\python.exe"
```

Package an existing converted 69-map directory without Python, Git, a game installation, or Tacview:

```powershell
.\build.ps1 -TerrainSource ".\converted-terrain"
```

`-TerrainSource` and `-WithTerrain` are mutually exclusive. See the [single-file release guide](standalone-build.md) for installer options, payload behavior, profile selection, and disk requirements.

For a direct development build of the exporter:

```powershell
$env:CGO_ENABLED = "0"
go build -o .\wrpl-acmi.exe .\cmd\acmi
go test .\cmd\acmi .\wrpl\...
```

The package build copies both executables, `README.md`, `README.zh-CN.md`, `LICENSE`, `docs/`, `data/`, and the `tools/terrain`, `tools/airfields`, and `tools/countermeasures` tool trees into `dist/wrpl-acmi`; `-WithTerrain` adds the converted `terrain/` staging directory. `wrpl-acmi.exe` runs without those adjacent folders. Existing releases can lag behind `dev`; build from the current branch for current behavior.

The source-build workflow is configured for exporter and library checks, synthetic terrain decoding, airfield terrain sampling, installer checks, terrain format conversion, and a package build. Real terrain conversion is not run in CI because it needs local game files. No CI success is claimed here, and CI does not claim Tacview GUI loading or rendering compatibility.

## Evidence limits

Missile and aircraft motion values are derived from recorded position and attitude, not complete aerodynamic, thrust, mass, fuel, weather or sensor state. Aircraft IAS is an EAS approximation and is not a cockpit reading. Countermeasure types remain uncertain when ammunition evidence is missing, and unresolved types are displayed as Chaff by policy. Countermeasure paths without recorded positions are labelled estimates. Bombs with only construction records are release-position markers. Static guidance types and air-defence ranges do not prove that a target was locked, a radar was radiating, or an engagement was possible at a particular time.

Airfield coverage depends on the pinned source catalogue and on replay evidence for conditional layouts. A catalogue entry does not guarantee an active runway, and an unresolved source is not filled by guessing. Tacview GUI loading and visual rendering remain outside the scope of these instructions.
