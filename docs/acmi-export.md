# ACMI export guide

[简体中文](acmi-export.zh-CN.md) · [Project README](../README.md) · [Build and release](standalone-build.md)

This guide covers input requirements, common operations, and command-line options for the exporter on the dev branch. Start with a basic conversion, then use the topic references to interpret the exported objects and estimates.

## Prepare and export a replay

Use one `.wrpl` file for a client replay, or a directory containing the shards from one server match. Download and extract server replays first; the exporter does not accept web links or ZIP files.

```powershell
.\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

Server shards are ordered by their headers. Filenames do not need renumbering, but a single server shard is rejected as a file input. Input replays are not modified.

Default lookup data is embedded in the executable. Export does not require adjacent data files, installed terrain, the game, Python, Go, Git, or Tacview. Tacview is used to view the result.

Repeated aircraft sorties are exported automatically as separate Tacview objects. Recorded spawn counters (`F061`, `F030`, and the `F093` state report) distinguish reused unit UIDs; complete ECS handles preserve entity generations. When spawn counters are unavailable, a recorded ECS aircraft replacement provides a fallback. Deaths, radar states, target references, and weapon ownership are resolved within each sortie. Replacing an aircraft without a confirmed kill removes the old object without inventing a destruction event. Position jumps and sampling gaps are not used to guess respawns; missing spawn or identity evidence can limit coverage.

Ground and surface models are cross-checked against their storage names, allowing known unit-directory prefixes such as `tankModels/` and `structures/`. When a ground UID is reused, construction times and full ECS entity IDs separate its vehicle model and coalition into distinct objects. Confirmed kill reports determine death times when the later ECS destruction only removes the wreck. Missing or genuinely contradictory identity evidence remains unknown.

You can also pass one positional input. Put all options before the path because Go's flag parser stops at the first positional argument:

```powershell
.\wrpl-acmi.exe -out "C:\Replays\replay.acmi" "C:\Replays\replay.wrpl"
```

Do not combine this form with `-in` or pass multiple positional paths.

### Output location and naming

Without `-out`, the result is written to the current working directory. Dropping one client file or shard directory onto the EXE follows the same rule.

| Input | Default output |
| --- | --- |
| Client file `replay.wrpl` | `replay.acmi` |
| Complete shard directory `server-session` | `server-session.acmi` |
| Shard directory with a missing tail | `server-session.partial.acmi` |

The optional `run.bat` wrapper changes to the package directory first. Use an explicit `-out` path when the output location matters.

### Incomplete server replays

The default `-allow-incomplete=true` exports available data when the tail is missing and marks the ACMI as Incomplete. Use `-allow-incomplete=false` to require a complete replay.

This exception does not allow missing shard zero, missing internal shards, duplicates, mixed sessions, incompatible headers, or corrupt compressed data. Shards are opened and decompressed one at a time, but parsed tracks still consume memory according to match size. See [server replays](ACMI_SERVER_REPLAYS.md) (Chinese).

## Common settings

Aircraft estimates are enabled by default. To omit estimated aircraft telemetry:

```powershell
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi" -estimate-telemetry=false
```

CSV reports are optional. Supply a separate path for each report you need:

```powershell
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi" -list "tracks.csv" -radar-lock-log "locks.csv"
```

The default format is ACMI 2.1. Select `-acmi-version 2.2` explicitly when required; changing the file version does not establish compatibility with a particular Tacview GUI.

## Command-line options

### Input, output, and file information

| Option | Default | Meaning |
| --- | --- | --- |
| `-in` | None | Client replay file or server shard directory. |
| `-out` | Derived from input | ACMI output path. |
| `-allow-incomplete` | `true` | Allow a missing server replay tail. |
| `-author` | `wrpl-acmi` | Recording author or operator. |
| `-acmi-version` | `2.1` | Select `2.1` or `2.2`. |
| `-names` | None | Display-name JSON overrides using `air:`, `ground:`, `missile:`, `bomb:`, and related keys. |

### Coordinates and lookup tables

| Option | Default | Meaning |
| --- | --- | --- |
| `-terrain-layout` | `geographic` | Reviewed geography with Atlas fallback, or explicit `atlas`. |
| `-map-id` | Automatic | Canonical map name, `WarThunder.Geo1.<name>`, or `WarThunder.Atlas1.<name>`. |
| `-alt` | `0` | Vertical offset in metres; horizontal placement is unchanged. |
| `-ecshashes` | `embedded` | ECS name table; a JSON path overrides it, an empty value disables it. |
| `-missile-models` | `embedded` | Model-completion table; also accepts `off` or a schema 1 JSON path. |
| `-missile-guidance` | `embedded` | Static guidance table; also accepts `off` or a schema 1 JSON path. |

### Content switches

The Boolean options below default to true except `-bookmarks`, which defaults to false. Use `-option=false` to disable an option, or `-bookmarks=true` to enable highlighted bookmarks.

| Option | Meaning |
| --- | --- |
| `-countermeasures` | Export aircraft countermeasures. |
| `-radar-locks` | Export radar target designations. |
| `-radar-modes` | Export supported recorded aircraft radar modes and native pointing/labels; independent of target designations. |
| `-missile-seeker` | Export supported recorded post-launch seeker states and native lock properties/labels. |
| `-ground-units` | Enable the bulk synchronized ground-track export path. |
| `-ground-models` | Identify models from explicit evidence and include derived presentation and ranges. |
| `-airfields` | Export source-configured airport markers and runways. |
| `-estimate-telemetry` | Estimate aircraft heading, speed, Mach, IAS, AOA, and body-axis G. |
| `-bookmarks` | Opt-in highlighted timeline bookmarks; default `false`. |

`-events` selects message categories and defaults to `kill,chat,join,damage,radar`. Use `none` to disable, a comma-separated subset to select categories, or `all` to include the opt-in `award` category. Message selection is independent of highlighted bookmarks. See [message timeline](acmi-information.md#message-timeline).

### CSV reports

| Option | Content |
| --- | --- |
| `-list` | Tracks and current display names. |
| `-missile-flight-log` | Missile flight parameters and raw motion evidence. |
| `-countermeasure-log` | Countermeasure construction records. |
| `-radar-lock-log` | Radar designation state changes. |

Each option takes an output path. A report is written only when its path is nonempty.

See [sensor states and native compatibility](ACMI_SENSOR_STATES.md) for supported
layouts, exact mode names, model association, and runtime validation limits.

## Interpret the output

### Aircraft, metadata, and events

Aircraft positions and flight-model attitude come from the replay. Estimates add `HDG`, `TAS`, `Mach`, `IAS`, `AOA`, and body-axis G under zero-wind and standard-atmosphere assumptions. IAS is an EAS approximation, and G fields estimate specific force. Invalid or missing estimates are cleared rather than carried forward.

File metadata records title, author, UTC export time, replay time origin, and source comments. Confirmed aircraft destruction, weapon release or first observation, countermeasure release, and radar designation changes can create bookmarks; duplicate bookmarks at one timestamp are removed. See [aircraft telemetry and metadata](acmi-information.md).

### Ground units and airfields

Ground units use synchronized tracks where available. Models, coalitions, and destruction times require explicit identity and event evidence; unresolved objects retain generic identities. Disabling `-ground-models` leaves independent coalition evidence available.

A structure without synchronized tracks may use a timestamped ECS construction position when its static category is established. Conflicts, recycled identities, changing or nonfinite positions, and mission placeholders are excluded. See [ground units](ACMI_GROUND_UNITS.md) (Chinese) and [static objects](acmi-information.md#static-ground-objects).

The airfield catalogue covers 71 maps and 457 mission entries, with usable definitions for 67 maps. Unconditional geometry can be selected directly; conditional layouts require matching replay radar identities and positions. Counts include mission, rank, and condition alternatives, not simultaneously active airports. Heading describes the runway axis, not the active landing direction.

PhiPhi's eight checked race missions have air starts and no land-runway source. Caribbean has no reliable runway definition in the checked sources. See [airfields](ACMI_AIRFIELDS.md) and [coverage](ACMI_AIRFIELDS_COVERAGE.md).

### Missiles, bombs, and countermeasures

Missiles retain synchronized positions. Speed, acceleration, turn rate, and G are calculated from the position sequence. Model candidates, static guidance type, and INS duration have their own evidence and should not be confused with motion estimates. See [models](ACMI_MODELS.md) and [guidance](ACMI_GUIDANCE.md) (Chinese).

Bombs use WeaponSync positions when available. A construction-only bomb is marked `Release position only`; no missing trajectory or impact time is inferred. See [bombs](ACMI_BOMBS.md) (Chinese).

Countermeasure types require owner-ammunition evidence identifying one active type. Mixed or unresolved releases display as Chaff with `fallback` confidence and an `UnknownAsChaff` source marker. Recorded positions take priority over explicitly labelled short estimated trajectories. See [countermeasures](ACMI_COUNTERMEASURES.md).

### Radar designations and air-defence ranges

Designations use native Tacview target properties, with slant range and body-relative angles when supported by positions and attitude. Positions must be at or before the event and no more than one second old. Unlocks, target destruction, and invalid geometry clear the applicable fields. These records do not establish STT lock, radar power, or weapon guidance state. See [radar designations](ACMI_RADAR_LOCKS.md) (Chinese).

The static air-defence table contains 255 models. Of these, 142 gun-only profiles with confirmed radar absence and no valid missile range omit the circle. Radar-equipped guns and systems with valid missile ranges retain references. ZSU-37-2 uses an explicitly recorded 2,000 m display override while preserving the 4,000 m source value. See [static ranges](ACMI_AIR_DEFENCE_RANGES.md).

## Terrain and coordinate settings

The default geographic layout uses 14 reviewed WGS84 estimates and 57 Atlas fallbacks across 71 maps. Terrain XML and replay objects must use the same reference configuration. Re-export original WRPL files when matching existing ACMI to updated terrain.

Explicit `-terrain-layout atlas` pairs with `-atlas` in the standalone installer. Terrain installation affects the displayed background and is not required to export objects. Game height assets are not included in the source repository. See [terrain tools](../tools/terrain/README.md), [geographic placement](TERRAIN_GEOGRAPHIC.md), and [Atlas layout](terrain-atlas.md).

## Development build and checks

Use the root `build.ps1` for a complete Windows package, as described in the [build guide](standalone-build.md). To build only the headless exporter, install Go 1.25.6 or newer and run from the repository root:

```powershell
$env:CGO_ENABLED = "0"
go build -o .\wrpl-acmi.exe .\cmd\acmi
go test .\cmd\acmi .\wrpl\...
```

Repository workflows define parser, synthetic terrain, and package checks. Real terrain conversion requires local game files, and Tacview GUI validation needs a separate environment.

## Troubleshooting

**The output file is missing.** Check the current working directory, or the package directory when using `run.bat`. Specify `-out` to choose the location explicitly.

**The filename contains partial.** The server replay has no complete ending. Review the recorded portion, but do not treat it as evidence of the final match result.

**Tracks do not align with terrain.** Check the layout and reference configuration on both sides, restart Tacview after installation, and re-export the original WRPL. The `-alt` option cannot repair horizontal alignment.

**A model or telemetry value is absent.** Identification needs evidence, and estimates need valid samples. An empty value indicates insufficient information or a rejected sample.
