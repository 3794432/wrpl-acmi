# Static SPAA / SPAAM threat ranges

[简体中文](ACMI_AIR_DEFENCE_RANGES.zh-CN.md)

The exporter embeds `cmd/acmi/ground_threats.json`; it does not rely on Tacview's object database to supply ranges for these units. Each entry associates a game model basename with an explicit ground UID from the replay. Matching is case-insensitive and never guesses a model from a similar name, country, or position.

The data is pinned to War Thunder Datamine `f7952e4291e9fd7b6fe38e84020dddef925c3457` (`2.59.0.74`). The table contains 255 models, drawn from the `type_spaa` entries in `unittags.blkx` and fixed/AI entries under `gamedata/units/air_defence`. Radar, fire-control, or searchlight-only units may remain in the source catalogue but do not produce a weapon circle.

## Range selection

- For guns, use the real weapon configuration's `attackMaxDist`. If absent, use `AttackShellMaxRadius` and then `AttackMaxRadius` on the same weapon mount. If all are absent, retain `aimMaxDist` as a low-confidence aiming reference, mark it `StaticAimLimitReference`, and do not call it the engagement limit.
- For missiles, read `bullet.rocket` and prefer `rangeMax`. If absent, use the guidance-control distance, then `AttackMaxRadius` on the same mount, then the launcher's `attackMaxDist`. Do not take a maximum across fields with different meanings, and do not substitute body-flight range, radar search range, or `guaranteedRange` for the engagement limit.
- Preserve the largest sourced static ability among configurations of one weapon class, with at most one primary range per class. The table does not determine the loadout, radar power state, terrain masking, moving target, or guidance result in a replay.
- For a multi-mount gun system, prefer the main anti-air gun calibre and then a sourced range on a candidate of that calibre. Smoke, decoys, dummy weapons, and unguided rockets are not anti-air guns. A SAM vehicle's low-calibre self-defence machine gun does not get a separate anti-air circle.
- Hide a gun circle only when the source confirms the absence of search or tracking radar and the profile has no valid missile range. The current 255-model table has 142 profiles meeting that policy; model names, tracks and coalitions remain. Optical or infrared sensors are not radar, and unknown radar equipment is not treated as confirmed absence.
- Radar-equipped guns and SAM or gun-missile systems with a valid missile range keep their weapon range. A radarless infrared or optical guided missile system is not removed merely because it lacks radar. This classification describes static equipment, not radar activity in a replay.

## ZSU-37-2 display radius

`ussr_zsu_37_2` uses the user-approved 2,000 m display radius. It emits `EngagementRange=2000` and `EngagementMode=1`. `WT_EngagementRangeBasis` explicitly identifies this as a user override rather than the original weapon `attackMaxDist`.

The provenance is retained: `WT_EngagementRangeSource` records both `User-configured ZSU-37-2 display radius: 2000 m` and the pinned Datamine value `37mm_2a11_user_cannon.blkx#/attackMaxDist=4000 m`. The ACMI therefore distinguishes the 2,000 m display choice from the original 4,000 m upstream source value. The displayed circle must not be read as a Datamine-derived range.

## ACMI fields

The primary range is written as `EngagementRange` (radius in metres) with `EngagementMode=1`. `WT_EngagementRangeSource`, `WT_EngagementRangeMode`, `WT_EngagementRangeKind`, and `WT_EngagementRangeBasis` distinguish missile or gun data, parameter provenance, and aiming references. The Mode enables a static reference circle; it does not mean that a radar was observed as active.

For a mixed system, the largest range is always the primary circle. A second weapon class is retained in the historical-compatibility `EngagementRange2` / `EngagementMode2` fields and their matching provenance fields. Current Tacview documentation explicitly covers the first circle; the second-circle fields appear in older official documentation. This update did not verify simultaneous two-circle rendering in the GUI, so it guarantees export of the primary range and preservation of the secondary range data.

This model table does not apply to ordinary aircraft, non-air-defence vehicles, missile tracks, radar designations, or countermeasures. `-ground-models=false` disables model-derived presentation and threat ranges while coalition evidence remains independent. `-ground-units=false` disables the bulk ground-track export path.

## Validation boundary

Go tests cover mixed systems, missing or invalid values, radarless suppression, case handling, and isolation between ground and air identities. The controlled real-replay check used only `#2026.09.21 16.33.02.wrpl`, converted under a 2 GiB Windows Job Object limit. Before/after checks protected object ID, position, time, lifetime, names, coalitions, and all non-ground data; only existing ground range fields and evidence-backed air-defence classification were allowed to change.

The catalogue is a fixed-version static reference and cannot guarantee exact agreement with every replay version. These data, outputs and tests do not constitute Tacview GUI or visual-rendering validation.
