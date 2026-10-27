# Airport positioning and runways

[中文](ACMI_AIRFIELDS.zh-CN.md) · [Map coverage](ACMI_AIRFIELDS_COVERAGE.md) · [Export guide](acmi-export.md)

`-airfields=true` selects runway geometry by the exact replay map, header level and mission. Navigation markers are appended after physical replay objects, preserving their IDs, tracks and references. Ground-unit and airport export work independently. Radar labels remain `AFF Radar` and `AFM Radar`, including snow variants; full models remain in `WT_GroundModel`.

## Sources and coverage

The catalogue records coverage for all 71 terrain-map IDs. The coverage report distinguishes available runway definitions from unresolved sources. A map entry does not guarantee that a runway can be exported; missing source geometry is left unresolved.

Sources are [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) at `f7952e4291e9fd7b6fe38e84020dddef925c3457`. Root missions assign maps; imported helper levels may be unrelated placeholders. Mission object-group matrices transform explicit prefab `airfield.start`, `end` and `width`. Direct mission `addAirfield` definitions are also collected. Dimensions are not inferred from filenames: the prefab named `dynaf_pg_1line_3000_universal` defines a 3200 m × 120 m strip.

## Layout selection

Unconditional placements can be exported directly. A conditional or rank-dependent layout requires a unique match to one main radar and two flight radars on the same team, with explicit identities and positions within 2 m horizontally of the source coordinates. Recycled identities, duplicate roles, and conflicting positions are rejected.

Identical geometry is exported once; ambiguous geometry is omitted. Trigger-created runways remain unselected when their active state cannot be established. Even when the layout is unresolved, facility anchors may still be retained if supported by replay evidence.

## Geometry and elevation

Each runway uses `Ground+Static+Aerodrome`, source dimensions, and two `Navaid+Static+Waypoint` endpoints linked by `Parent` and `Next`. Named endpoint areas in a root mission can also provide the runway axis. When width is undefined, `Width` is omitted and `WidthUnknown` is recorded.

Heading describes the geometric axis, not the current landing direction. Flight-radar positions are not runway endpoints. Every marker uses the same `terrainPosition` transform and geographic or Atlas layout as tracks and terrain.

Matching radar ground heights take priority. Otherwise, a catalogue built with terrain data uses sampled signed-metre game elevation. Both are labelled flat-field approximations. With neither available, nominal transformed endpoint height is explicitly marked `NominalTransformedEndpointY;NotGameGround`. `WT_Airfield*` properties retain source paths/commit, mission group, prefab, radar UIDs, position and elevation provenance. Radar destruction does not remove the static navigation markers. These markers are additional ACMI objects, outside the physical tracks CSV.

## Rebuilding the catalogue

Normal Go builds embed the checked-in `cmd/acmi/data/airfields_all.json` without network access. To rebuild the catalogue, run one of these commands from the repository root using standard-library Python:

```powershell
python tools/airfields/build_catalog.py
python tools/airfields/build_catalog.py --source-root C:/War-Thunder-Datamine
python tools/airfields/build_catalog.py --source-root C:/War-Thunder-Datamine --terrain-root ./dist/wrpl-acmi/terrain
```

`--source-root` requires the pinned mission and object-group files; the levels-only sparse checkout used for terrain conversion is insufficient. `--cache-dir` retains a size-limited download cache. Optional `--terrain-root` accepts converter-generated `maps/<id>/metadata.json` and signed `<id>.data`. The builder checks raster dimensions, bounds, row direction, and SHA-256 before sampling a small number of elevation points. `build_reviewed_falklands.py` rebuilds the smaller reviewed Falklands fallback configuration.

ACMI remains version 2.1. This feature does not change terrain installation. Automated source/geometry checks and controlled exports are separate from Tacview GUI verification.
