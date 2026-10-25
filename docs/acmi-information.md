# ACMI metadata, bookmarks, and estimated aircraft telemetry

[中文](acmi-information.zh-CN.md) · [Export guide](acmi-export.md)

This page explains file metadata, event bookmarks, and estimated aircraft fields, followed by the source rules for static objects and radar geometry. Recorded positions and attitude are distinct from the telemetry calculated from them.

## File information

Files include a map/mode/replay-time `Title`, `Author` (default `wrpl-acmi`, override with `-author`), UTC export `RecordingTime`, `Category=War Thunder`, `DataRecorder=wrpl-acmi`, and `Comments`. Comments record the exporter build, requested terrain layout, georeference commit, and estimation policy. `ReferenceTime` remains the replay time origin; it is independent of file creation time. Existing georeference properties describe the effective geographic/Atlas layout.

## Event bookmarks

Bookmarks are disabled by default (`-bookmarks=false`), retaining ordinary messages without duplicate highlighted rows. When enabled with `-bookmarks=true`, they identify confirmed aircraft destruction, missile launch or first observation, bomb release or first observation, countermeasure release, and radar designation changes. Duplicate bookmarks at the same timestamp are removed, while original messages and destruction events remain.

Standard ACMI does not provide a per-event background color; Tacview controls bookmark highlighting. No separate color is assigned to Critical damage. A radar designation bookmark does not establish STT lock or radar power state.

## Object groups and names

An object's `Group` is derived from its explicit coalition. The two identified airport radar types use `Importance=0.75` as a display priority, not an operational measurement. `Name`, `CallSign`, and `Pilot` retain their respective roles. Under the [official ACMI specification](https://www.tacview.net/documentation/acmi/en/), `ShortName`, `LongName`, and `FullName` belong in Tacview's object database, and `Registration` is a tail or registration number. The exporter therefore leaves those fields free of player names or unverified manufacturer information.

## Aircraft telemetry estimates

```powershell
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi" -estimate-telemetry=false
```

Aircraft telemetry estimates are enabled by default. The example above disables them; omit `-estimate-telemetry=false` to keep the default. When enabled, aircraft position updates also contain native `HDG`, `TAS`, `Mach`, `IAS`, `VerticalGForce`, `LongitudinalGForce`, `LateralGForce`, and `AOA` properties. No missile label or existing missile flight field is changed.

- Heading comes from recorded attitude. TAS assumes zero wind, equating path speed with airspeed. Mach uses a layered standard atmosphere; game altitude approximates geopotential height. IAS uses `TAS * sqrt(density / sea-level density)`, an **EAS approximation**, not exact compressible IAS/CAS. Weather and wind are not reconstructed.
- Body-axis G is specific force: `(coordinate acceleration - gravity) / g`, projected onto forward, right, and up axes using recorded heading, pitch, and bank. Level unaccelerated flight is +1 normal G, freefall is 0 G, and inverted unaccelerated flight is -1 normal G. These differ from the existing missile coordinate-acceleration magnitude.
- AOA comes from the direction of the zero-wind velocity relative to the body axes. It is unavailable for zero/negative forward velocity. It is not a decoded aerodynamic sensor.
- A bounded local quadratic fit reduces packet jitter. Estimates never bridge gaps over two seconds or rejected intervals. Nonfinite data, duplicate/reversed chronology, speeds over 1200 m/s, and acceleration over 100 coordinate G are rejected. Atmosphere estimates stop above 84.852 km. Missing fields are explicitly cleared with empty assignments.
- Aircraft positions, attitude, object ids, and destruction times are retained. Comments explicitly identify estimates as position/attitude derivatives, not recorded game telemetry. Estimates are unsuitable as proof of actual cockpit readings, airframe loads, or aerodynamic limits.

## Optional CSV diagnostics

Track listings (`-list`), missile flight evidence (`-missile-flight-log`), countermeasure constructions (`-countermeasure-log`), and radar lock state changes (`-radar-lock-log`) are not written by default. Supply an explicit CSV path for each report you want; the corresponding file is written only when its path is nonempty.

## Static ground objects

When ground export is enabled, a structure/fortification without a synchronized track may use a successfully decoded ECS construction `transform` translation and its actual packet time. Only explicit static-category evidence is accepted. UID/model conflicts, recycled identities, nonfinite or changing translations, missing timestamps, and `dummy`/`dummy_immortal` mission placeholders are excluded. Existing synchronized ground tracks take precedence.

Accepted objects use `Type=Ground+Static+Building`, keep model/team evidence, add `WT_GroundPositionSource=ECSConstructionTransform` and `WT_GroundStatic=1`, and are removed on an explicitly matched ECS destruction. No vehicle is frozen merely because its trajectory is missing. Object counts depend on the replay; existing synchronized ground tracks are retained.

## Radar geometry and file version

`LockedTargetAzimuth` (positive right) and `LockedTargetElevation` (positive up) are body-relative angles in degrees. They share the existing slant-range checks: positions at or before the event, no more than one second old, and valid object lifetimes. Invalid geometry, unlocks, and target destruction clear angles. A fresh target switch immediately publishes the new geometry. Coincident targets have valid zero range but no angles.

The default header is `FileVersion=2.1`; use `-acmi-version 2.2` to select 2.2 explicitly. These fields do not require a higher default version. Earlier checks covered generated text and tests, but did not establish Tacview 1.9.4 or 1.9.5 GUI loading, rendering, or terrain-loader compatibility.

## Message timeline

`-events=kill,chat,join,damage,radar` is the default. Use `-events=none` to disable these messages or a comma-separated subset, such as `-events=kill,radar`. Awards are disabled by default; explicitly add `award` to the selection or use `-events=all` to include them. This is independent of `-bookmarks` and the four opt-in CSV files. Event times use the original packet timestamp and the existing ACMI time origin.

- Kill messages use explicit killer/victim identities and the killer's `PlayerWeapon`. Aircraft kills must match the confirmed F058 destruction time exactly. Missing, ambiguous, or conflicting attribution is omitted; `DestroyedWeapon` is not substituted as the killer's weapon. These messages do not move destruction times or assign kills by proximity.
- Chat, player roster changes, critical/severe damage and awards use recorded packet fields. Messages without reliable object attribution remain objectless. Initial player rosters are not treated as join events.
- Damage messages show the victim's vehicle and player followed by `critical damaged by` or `severe damaged by` and the attacker's vehicle and player. Unresolved identities remain unknown; slot numbers, raw UIDs and fire flags are omitted from the displayed message.
- Radar messages describe target designation and clearing, using `Target <name>; Range <metres> m` when distance is available. Range remains the calculated geometric slant range. They use the same prepared state changes as native radar properties, including clears between aircraft position syncs. They do not prove STT lock. `-radar-locks=false` disables native lock properties; omit `radar` from `-events` to disable radar messages too.
- Malformed or oversized event payloads are skipped. Event collectors have bounded retention; repeated identical messages at the same timestamp are deduplicated. A message never recreates an unborn or already deleted object.

## Moving ground direction and destruction

Moving ground units use neighboring recorded positions to derive geographic course. Available headings use the six-component `T` transform and `WT_HeadingSource=PathDerivative`; they are not measured hull or turret attitude. Static units, single-point tracks and unreliable movement intervals retain position-only transforms. No direction is inferred across large recording gaps. An explicit matched ground destruction emits `Event=Destroyed` before object deletion; reaching the last position sample alone does not count as destruction.
