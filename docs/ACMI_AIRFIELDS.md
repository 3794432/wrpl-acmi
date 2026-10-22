# Airport positioning and runways

[中文](ACMI_AIRFIELDS.zh-CN.md) · [Map coverage](ACMI_AIRFIELDS_COVERAGE.md)

`-airfields=true` selects runway geometry by the exact replay map, header level and mission. Navigation markers are appended after physical replay objects, preserving their IDs, tracks and references. Ground-unit and airport export work independently. Radar labels remain `AFF Radar` and `AFM Radar`, including snow variants; full models remain in `WT_GroundModel`.

The catalogue records coverage for all 69 terrain-map IDs. The coverage report distinguishes actual source-backed runway definitions from unresolved maps; a catalogue entry alone does not imply a runway exists. Missing information is not filled with generic geometry.

Sources are [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) at `f7952e4291e9fd7b6fe38e84020dddef925c3457`. Root missions assign maps; imported helper levels may be unrelated placeholders. Mission object-group matrices transform explicit prefab `airfield.start`, `end` and `width`. Direct mission `addAirfield` definitions are also collected. Dimensions are not inferred from filenames: the prefab named `dynaf_pg_1line_3000_universal` defines a 3200 m × 120 m strip.

Unconditional source placements can be exported directly. Conditional/rank-dependent layouts require a unique match against the same team's explicit main radar and two flight-radar identities, within 2 m horizontally. Recycled identities, duplicate roles and conflicting positions are rejected. Repeated identical geometry is deduplicated; ambiguous geometry is omitted. Conditional trigger-created strips without evidence of their active state remain unselected. Unknown layouts retain facility-only anchors where replay evidence exists.

Each runway uses native `Ground+Static+Aerodrome` and source dimensions, with two `Navaid+Static+Waypoint` endpoints linked by `Parent` and `Next`. Exact named mission endpoint-area pairs also supply a runway axis; if their width is undefined, `Width` is omitted and `WidthUnknown` is recorded. Headings describe the geometric axis, not the active landing direction. The flight-radar pair is not the runway endpoints. Every marker uses the same `terrainPosition` transform as replay tracks and terrain, including the existing Geographic/Atlas selection.

Matching radar ground heights take priority. Otherwise, a catalogue built with terrain data uses sampled signed-metre game elevation. Both are labelled flat-field approximations. With neither available, nominal transformed endpoint height is explicitly marked `NominalTransformedEndpointY;NotGameGround`. `WT_Airfield*` properties retain source paths/commit, mission group, prefab, radar UIDs, position and elevation provenance. Radar destruction does not remove the static navigation markers. These markers are additional ACMI objects, outside the physical tracks CSV.

Normal Go builds embed the checked-in `cmd/acmi/data/airfields_all.json` without network access. Rebuild using standard-library Python:

```powershell
python tools/airfields/build_catalog.py
python tools/airfields/build_catalog.py --source-root C:/War-Thunder-Datamine
python tools/airfields/build_catalog.py --source-root C:/War-Thunder-Datamine --terrain-root ./dist/wrpl-acmi/terrain
```

`--source-root` requires the pinned mission and object-group files. `--cache-dir` retains bounded download caches. Optional `--terrain-root` accepts converter-generated `maps/<id>/metadata.json` and signed `<id>.data`; raster dimensions, bounds, row direction and SHA-256 are checked before sparse height sampling. `build_reviewed_falklands.py` rebuilds the smaller reviewed fallback fixture.

ACMI remains version 2.1. This feature does not change terrain installation. Automated source/geometry checks and controlled exports are separate from Tacview GUI verification.
