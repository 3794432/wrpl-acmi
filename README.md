# wrpl-acmi

[中文](README.zh-CN.md) · [Export guide](docs/acmi-export.md) · [Build and release](docs/standalone-build.md) · [Documentation](docs/README.md)

wrpl-acmi converts War Thunder replays into Tacview ACMI files for reviewing tracks, events, and related objects. It accepts a client `.wrpl` file or a directory of server replay shards from one match, and writes ACMI 2.1 by default.

The project builds on [wrpl-inspector v3](https://github.com/maxsupermanhd/wrpl-inspector), retaining its Go replay library and dear-imgui inspector alongside the headless `cmd/acmi` exporter. Development takes place on the [dev branch](https://github.com/3794432/wrpl-acmi/tree/dev). The Go module path remains `github.com/maxsupermanhd/wrpl-inspector/v3`.

## Quick start

With a release package available, run the exporter from PowerShell. These examples assume the package is in `dist\wrpl-acmi`:

```powershell
.\dist\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\dist\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

`wrpl-acmi.exe` can be copied and run on its own. Its default lookup tables, including `ecshashes`, are compiled into the executable. Export does not require an adjacent `data/` directory, installed terrain, the game, Python, Go, Git, or Tacview. Use Tacview to open the resulting ACMI.

For a server replay, download and extract the shards into a separate directory for that match. The exporter orders them by their headers and rejects a single server shard passed as a file. See [server replay input](docs/ACMI_SERVER_REPLAYS.md) (Chinese).

You can also drop one client replay file or one shard directory onto the EXE. Without `-out`, the result is written to the current working directory as `<input-name>.acmi`. A server replay missing its tail becomes `<directory-name>.partial.acmi` and is marked incomplete. The optional `run.bat` wrapper changes to the package directory before running the exporter.

A single positional input is also supported. Put all options before it, and do not combine it with `-in`:

```powershell
.\wrpl-acmi.exe -out "C:\Replays\replay.acmi" "C:\Replays\replay.wrpl"
```

See the [export guide](docs/acmi-export.md) for all options, input checks, and output rules.

## Exported data

The output combines recorded replay data, estimates derived from that data, and static configuration references. Source fields identify which kind of information each object carries.

| Content | Source and main limitation |
| --- | --- |
| Aircraft and ground units | Recorded positions; aircraft retain flight-model attitude. Models, coalitions, and destruction times require supporting evidence. Eligible static structures can use timestamped ECS construction positions. |
| Missiles and bombs | Synchronized positions where available. Missile motion values are estimates; bombs with construction data alone are labelled `Release position only`. |
| Countermeasures | Flare or Chaff when ammunition evidence identifies the type. Unresolved types display as Chaff with a fallback marker. Short estimated paths are labelled separately. |
| Radar designations | Native `LockedTarget` properties and supported distance and angle calculations. A designation does not establish STT lock or radar mode. |
| Airfields | Source geometry selected by map, mission, and any required radar evidence. The catalogue covers 69 maps and 457 mission entries; 67 maps have usable runway definitions. |
| Air-defence ranges | Static references for 255 models. These describe configuration or an explicitly recorded display setting, not a live engagement envelope. |
| Metadata and events | Titles, authors, timestamps, source comments, coalition groups, and bookmarks. Aircraft telemetry estimates are enabled by default; use `-estimate-telemetry=false` to disable them. |

Track listings, missile flight data, countermeasure constructions, and radar designation changes can also be written to CSV. Each report requires an explicit output path.

Aircraft estimates assume zero wind and a standard atmosphere. IAS is an EAS approximation, and body-axis G values estimate specific force. Static guidance types do not establish seeker state, and a runway definition does not prove that the runway is active in a particular match. The topic guides below explain these limits in detail.

## Build from source

A Windows package build needs PowerShell and Go 1.25.6 or newer:

```powershell
git clone --branch dev --single-branch https://github.com/3794432/wrpl-acmi.git
Set-Location .\wrpl-acmi
.\build.ps1
```

The default output directory is `dist\wrpl-acmi`. It contains two independently usable programs:

| Program | Purpose |
| --- | --- |
| `wrpl-acmi.exe` | Exports replays using embedded default lookup data. |
| `wrpl-terrain-installer.exe` | Installs converted game terrain for Tacview. A basic build contains installation logic but no terrain data, so an external converted directory is required at installation time. |

To include an existing converted terrain directory in the installer:

```powershell
.\build.ps1 -TerrainSource ".\converted-terrain"
```

Converting terrain from the game additionally requires 64-bit Python 3.12, converter dependencies, Git, local game files, and pinned Datamine configuration. Follow the [terrain tool guide](tools/terrain/README.md) for setup and the [build guide](docs/standalone-build.md) for packaging and resume options. Older releases may lag behind dev.

## Terrain in Tacview

Installing game terrain is optional. Without it, replay objects still export, but Tacview's background terrain may not match the game map.

Run an installer that contains terrain data with:

```powershell
.\dist\wrpl-acmi\wrpl-terrain-installer.exe
```

The default destination is `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`. The installer uses system Windows PowerShell. Allow at least 8 GB of free space for the complete 69-map set at the default resolution. After installation, restart Tacview and re-export original WRPL files to ensure that tracks and terrain use the same coordinate configuration.

Both programs default to geographic layout: 14 maps use reviewed WGS84 estimates, while 55 use Atlas fallback coordinates. Atlas coordinates are for technical display, not real-world placement. For an explicit Atlas layout, pair installer `-atlas` with exporter `-terrain-layout atlas`. The `-alt` option only changes vertical offset.

Heights come from the game's `levels/*.bin` files; Datamine supplies configuration such as boundaries and water levels, while WT-MAP-ANALYZE supplies estimated geographic placement. Source versions and licensing are documented in [input sources and licensing](tools/terrain/README.md#input-sources-and-licensing).

## Documentation

| Task | Guides |
| --- | --- |
| Export and build | [Export options](docs/acmi-export.md), [build and release](docs/standalone-build.md), [server replays](docs/ACMI_SERVER_REPLAYS.md) |
| Interpret objects and events | [Aircraft estimates and metadata](docs/acmi-information.md), [ground units](docs/ACMI_GROUND_UNITS.md), [bombs](docs/ACMI_BOMBS.md), [countermeasures](docs/ACMI_COUNTERMEASURES.md), [radar designations](docs/ACMI_RADAR_LOCKS.md) |
| Inspect static references | [Airfields](docs/ACMI_AIRFIELDS.md), [map coverage](docs/ACMI_AIRFIELDS_COVERAGE.md), [air-defence ranges](docs/ACMI_AIR_DEFENCE_RANGES.md), [missile models](docs/ACMI_MODELS.md), [guidance](docs/ACMI_GUIDANCE.md) |
| Configure terrain | [Terrain tools](tools/terrain/README.md), [geographic coordinates](docs/TERRAIN_GEOGRAPHIC.md), [Atlas layout](docs/terrain-atlas.md) |
| Review upstream tools and evidence | [Documentation index and inspector](docs/README.md), [model evidence](docs/evidence/missile-models.md), [guidance evidence](docs/evidence/missile-guidance.md) |

Some specialist guides are currently available only in Chinese; the [documentation index](docs/README.md) identifies their language.

## Validation and license

Previously checked samples linked models and coalitions for 69 aircraft and 140 ground units. This is sample-specific coverage, not a guarantee for every game version or replay. The [source-build workflow](.github/workflows/source-build.yml) defines exporter, library, and synthetic terrain checks. Real terrain conversion needs local game files, and Tacview GUI loading and rendering require separate validation.

This project and the `wrpl` library are released under the GNU Affero General Public License v3; see [LICENSE](LICENSE). Upstream attribution and notices are retained. Map and game assets belong to Gaijin Entertainment. This project is not affiliated with or endorsed by Gaijin.
