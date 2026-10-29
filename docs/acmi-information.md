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

## Ground initialization snapshots

A stationary UID-only position prefix before that UID's first unambiguous ECS construction is omitted rather than exported as a temporary separate vehicle. Such initialization snapshots at convoy spawn points otherwise create artificial objects that disappear when construction metadata arrives, which Tacview can report as deaths even without an explicit `Destroyed` event. This rule uses identity boundaries and unchanged recorded coordinates, not a fixed opening-time window or a geographic blacklist.

Moving prefixes, identities without later construction evidence, contradictory construction metadata, and prefixes with matching victim reports remain available for normal lifetime resolution. Confirmed vehicle generations, stationary real units and actual destruction events retain their recorded trajectories and lifetimes. Removing initialization objects can renumber subsequent ground/facility ACMI IDs; parent and radar references are generated again from retained identities.

## Static ground objects

When ground export is enabled, a structure/fortification without a synchronized track may use a successfully decoded ECS construction `transform` translation and its actual packet time. Only explicit static-category evidence is accepted. UID/model conflicts, recycled identities, nonfinite or changing translations, missing timestamps, and `dummy`/`dummy_immortal` mission placeholders are excluded. Existing synchronized ground tracks take precedence.

Accepted objects use `Type=Ground+Static+Building`, keep model/team evidence, add `WT_GroundPositionSource=ECSConstructionTransform` and `WT_GroundStatic=1`, and are removed on an explicitly matched ECS destruction. No vehicle is frozen merely because its trajectory is missing. Object counts depend on the replay; existing synchronized ground tracks are retained.

## Air-to-air missile models

The default embedded catalogue scans all 706 `rocketguns/*.blkx` definitions at pinned Datamine commit `f7952e4291e9fd7b6fe38e84020dddef925c3457`. `cmd/acmi/data/aam_models.json` supplies 683 aliases and 119 distinct display names (114 localized names, 5 untranslated internal identifiers), including AIM-120A/B/C-5/D, R-77/RVV-AE/R-77-1, Sparrow, Sidewinder, Phoenix, R-27, PL-series and other guided AAM definitions. Models need not appear in a bundled sample replay. Reviewed legacy display spellings take precedence for the same internal identity.

An alias maps an explicit weapon identity to a name; it does not identify an arbitrary unknown object. Unique same-owner report/end-time matches and consistent replay-local raw construction-token/template/weapon-role groups supply the binding. Nonzero short tokens below 129 also participate without inventing a public numeric weapon ID. Zero/oversized tokens never propagate identity. Conflicting reports remain unresolved. Recorded actual-loadout snapshots may supply a unique compatible model. Guidance-filtered possible stores may supply an inferred family as described below; they never establish a confirmed variant.

Rebuild with `python tools/agm/build_catalog.py --cache <asset-cache> --fetch --role AAM --out cmd/acmi/data/aam_models.json`. Bullet names use exact English localization, or an exact launcher localization for a single guided bullet identity. Without a translation the raw internal identifier is retained and marked in catalogue provenance. Ambiguous shared bullet aliases are omitted while exact launcher aliases remain usable. SAM naming follows its separate launcher catalogue below; unknown future assets require rebuilding the catalogues.

### Guidance and family fallback

Recorded AAM seeker layouts also feed guidance classification: the supported IR layout can establish `IR` without a model, while a radar block still needs independent template/model evidence to distinguish ARH from SARH. Conflicting or undecoded layouts remain unknown. This does not invent a lock or change recorded seeker states.

After exact-model identification, default `-missile-models embedded` can merge compatible variants of a single family: AIM-120A/B/C/D becomes `AIM-120`, AIM-9 variants become `AIM-9`, and R-73 variants become `R-73`. Complete actual owner ammunition is preferred (`ReplayOwnerLoadoutFamily`). If it cannot supply the identity, the exact launch-time parent model may use its pinned declared custom-slot options (`CarrierOptionsGuidanceFamily`), filtered by missile role, template and decoded seeker type. The catalogue covers 490 aircraft/helicopter profiles scanned from 1,652 definitions; only declared `WeaponSlots`/`commonWeapons` missile references are used. Combination mounts in `weapons/containers` are expanded recursively to their exact missile definitions, with Git blob hashes for every dependency; cycles, missing references and depth limits remain unresolved. The AAM catalogue also recognizes the established omitted/false `radarSeeker.active` convention as SARH (without applying it to AGM passive seekers). This allows Sparrow mounts to resolve to `AIM-7` and compatible MiG-29 SARH options to `R-27` while retaining unresolved variants. Missing slot declarations, unresolved references for the relevant role, unknown parent/model or multiple remaining families prevent this fallback.

Family results use native `Name/CallSign` and `WT_MissileModel`, retain candidate variants, and carry confidence `Inferred`, `WT_MissileFamily`, `WT_MissileVariantUnresolved=1` and, for carrier-option inference, `WT_MissileFamilyCarrier`. `WT_MissileInternalName` stays empty: no specific variant was decoded. Existing exact models and manually supplied display names take priority. Carrier-derived families do not propagate to other missiles or promote static guidance into recorded seeker telemetry. `-missile-models off` disables family naming; supported recorded guidance classification remains independent. Mixed AIM-9/R-73 possibilities remain generic `AAM (IR)` rather than choosing a nationality or counting launches.

To rebuild after the AAM/AGM alias tables, run `python tools/agm/build_carrier_catalog.py --cache <asset-cache> --fetch --out cmd/acmi/data/missile_carriers.json`. It verifies each aircraft definition against the same pinned Git tree, can rebuild offline without `--fetch`, and embeds only compact names/references/provenance. Runtime conversion needs no game installation, assets or network.

## Air-to-ground missile models

With the default `-missile-models embedded`, AGM names use a pinned Datamine catalogue with 680 aliases and 177 distinct display names (166 localized names, 11 untranslated internal identifiers) (`cmd/acmi/data/agm_models.json`) built from explicit guided weapon fields and English `units_weaponry.csv` names. Supported families include Maverick, Hellfire, Kh-25/29/31/38/58/59, LD-10, YJ-91, HOT, Spike and Brimstone. The catalogue supplies aliases, not a global mapping from numeric replay weapon IDs to models.

A weapon report can name a uniquely matched missile with the same recorded parent and a destruction time within 250 ms. Consistent report evidence may name other missiles in the same replay raw-token/template/AGM-role group. Exact owner ammunition snapshots can also supply a unique compatible model or propagate strong identity within the same owner, handle generation and weapon group. AAM and AGM aliases remain separate even when numeric IDs overlap. Mixed, unknown or contradictory stores retain candidates; launch order, speed and launcher slot number do not choose a variant. The SAM latest-variant assumption does not apply to AGMs.

Names are written to native `Name`/`CallSign` and `WT_MissileModel`; `WT_MissileInternalName`, model source and confidence retain provenance. Naming does not change positions, lifetimes or recorded seeker states, and an optical asset does not by itself establish IR versus TV seeker telemetry. Unknowns remain `AGM (unknown)`. `-missile-models off` disables catalogue-assisted AGM naming; a custom model table supplies its own aliases.

To rebuild, use `python tools/agm/build_catalog.py --cache <asset-cache> --fetch --out cmd/acmi/data/agm_models.json`. The cache requires `tree.json`, a Git recursive tree at pinned Datamine commit `f7952e4291e9fd7b6fe38e84020dddef925c3457`. Downloads and cached inputs are verified against Git blob hashes. Omit `--fetch` for an offline rebuild. The script scans all rocketgun definitions at that revision and selects explicit guided non-AAM/non-SAM weapons, without a filename-family allowlist. Use `--role AGM` explicitly or omit it for the same result. Unknown future assets require rebuilding. The application embeds the generated aliases and needs neither a cache nor network access at runtime.

## Bomb models

Bomb naming is enabled by default with `-bomb-models embedded`. The catalogue in `cmd/acmi/data/bomb_models.json` maps explicit Datamine `bombGun=true` definitions to the same pinned revision's English weapon names. It covers conventional, guided and nuclear bomb assets, including bomb constructions whose weapon name starts with AGM (such as JSOW). Their recorded Bomb component keeps them native `Type=Weapon+Bomb`; a name prefix never reclassifies the object.

Weapon reports use the same recorded parent and a 250 ms destruction window. A unique match uses source `KillReportMatched` and confidence `Matched`. Consistent evidence for the same raw construction token and template within this replay can complete other bomb names with source `ReplayWeaponTokenFromKillReport` and confidence `Inferred`. This token is not a global model ID. Simultaneous same-type salvos establish a type, not which individual bomb caused a hit. Different overlapping tokens, contradictory reports or distinct internal variants sharing one display name cannot establish a unique variant. A unique, fully understood actual owner loadout can also name a bomb; a static list of everything an aircraft could carry does not.

Native `Name`/`CallSign` show the model. `WT_BombModel`, `WT_BombInternalName`, `WT_BombModelSource`, `WT_BombModelConfidence`, `WT_BombLeadingULEB` and optional candidate/conflict properties retain the evidence. Missing construction identity, parent evidence or usable reports leaves the generic `Bomb` name. Synthetic replay-end times never substitute for recorded destruction in report matching. Naming itself does not change recorded positions, object IDs or lifetimes; separate motion estimation is described below. Manual `-names` overrides take priority. `-bomb-models off` disables this feature independently of `-missile-models`.

Rebuild with `python tools/bombs/build_catalog.py --cache <asset-cache> --fetch --out cmd/acmi/data/bomb_models.json`. Use the same fixed-commit `tree.json` and cache described for AGMs; the neighbouring `tools/agm` script provides the shared downloader. Every input is checked against its Git blob hash. Omit `--fetch` for an offline rebuild. The executable embeds the generated aliases and needs no asset cache or network connection for conversion.

## Estimated bomb motion

`-estimate-bomb-trajectories=true` is the default. It affects only construction-backed bombs without any recorded WeaponSync positions. A valid, explicitly associated aircraft sortie must supply two finite positions near release (within 1.5 seconds, no gap over 1.5 seconds). Missing, stale, ambiguous or excessive-speed evidence leaves the stationary release marker. Recorded bomb tracks always take priority.

The model inherits the parent's local ground velocity and applies constant downward gravity of 9.80665 m/s². It has no drag, wind, release impulse, parachute or guidance model. Samples are generated every 0.2 seconds, for at most 180 seconds, bounded by the original lifetime, available recording and sea level. Sea level is a visual safety cutoff, not a terrain collision or measured impact; maps do not have to be installed for conversion. Nonpositive release altitude is left unestimated.

The label becomes `Estimated ballistic trajectory`. `WT_BombPositionSource=EstimatedBallistic`, `WT_BombTrajectoryEstimated=1`, `WT_BombEstimateModel`, `WT_BombInitialVelocity*`, `WT_BombEstimateEndReason` and the original `WT_BombRecordedEndTimeMS` retain the model and evidence. `WT_BombTrajectoryAvailable=0` continues to mean that no real synchronized trajectory exists. The displayed estimate may end earlier than the original destruction record. File comments identify these assumptions. Set `-estimate-bomb-trajectories=false` to retain the original `Release position only` presentation.

## Surface-to-air missiles

SAM flight export is enabled by default. Rocket constructions in ground weapon groups 19–21 are accepted only when the exact owner EID, construction time and model identify a launcher in the embedded SAM catalogue. Group 19 also carries MLRS and ballistic rockets, so its trigger value alone never establishes a SAM. The catalogue currently covers 65 launcher configurations and 55 distinct ammunition names, including S-300, SAMP/T, Buk, Pantsir, Tor, Tunguska, Roland, Crotale, SPYDER, HQ-11 and Type 03 variants.

Each missile requires actual WeaponSync positions matched to its complete entity handle and recorded lifetime. Construction-only releases produce no estimated flight. Tacview receives native `Type=Weapon+Missile`, `T`, coalition and, where a matching live ground track exists, `Parent`. Missile flight continues independently after the launcher is destroyed. Disabling `-ground-units` and `-airfields` does not disable SAM tracks or the launcher's recorded coalition.

The fallback label `SAM (<launcher>)` identifies the platform, not a guessed missile variant. `WT_WeaponRole=SAM`, `WT_Launcher*`, `WT_SAMAmmoCandidates` and `WT_SAMAmmoSource` retain identity and static ammunition evidence. A mixed configuration keeps all candidates; its longest range, firing order and position are not used to select ammunition. These static candidates do not establish the missile's current seeker mode. Unknown or conflicting launcher evidence stays unclassified.

When an audited complete launcher configuration contains exactly one ammunition identity, its exact `bulletName` is mapped through the same Datamine revision's English `units_weaponry.csv` row. The model is written to native `Name`/`CallSign` and `WT_MissileModel`; `WT_MissileInternalName` retains the asset identity. `WT_MissileModelSource=LauncherConfigurationSingleAmmo`, confidence `Inferred`, and `WT_SAMModelEvidence=StaticLauncherConfiguration` distinguish this from a directly recorded weapon selection. Existing kill-report identities and manual display names take priority. `-missile-models off` disables this configuration naming stage.

The audit includes unit modifications, weapon presets and non-default missile bullet sets. With `-sam-model-fallback off`, incomplete audits, multiple ammunition identities, missing translations and contradictory template evidence retain the launcher fallback and model candidates. Naming does not infer an active seeker mode or change flight coordinates.

The default `-sam-model-fallback latest` applies an explicit display assumption to unresolved SAMs: prefer known radar ammunition, then the latest reviewed variant within the same family. The editable embedded preference order is `cmd/acmi/data/sam_variant_preferences.json`; latest refers to that reviewed table, not future game updates. Examples are 2S6 → 9M311-1M, SPYDER → I-Derby ER, and the known HQ-11 candidate → FM-3000. Non-radar families still keep their actual guidance class. Only ammunition already listed for the launcher can be selected; unknown family orders stay unresolved. Earlier identified models remain unchanged.

Assumed objects retain all model candidates and use `WT_MissileModelSource=UserLatestSAMVariant`, confidence `Assumed`, `WT_SAMModelEvidence=UserPreferenceFallback` and `WT_SAMModelAssumed=1`. File comments record the active policy. This is an operator preference, not decoded evidence of actual ammunition; recorded seeker modes, positions and coalition are unchanged. `-missile-models off` also disables this naming stage.

`tools/sam/build_catalog.py --catalog <full-air-defence-catalogue.json> --out cmd/acmi/data/sam_launchers.json` rebuilds the compact table offline from the full schema-1 weapon catalogue pinned to Datamine `f7952e4291e9fd7b6fe38e84020dddef925c3457`. The full input includes per-unit `weapons` evidence; the compact range-only `ground_threats.json` is not a substitute. Runtime export uses the embedded result without external files or network access.

For model naming, first run `tools/sam/build_missile_names.py` with the pinned asset cache and full catalogue as described in that script's help, retaining its JSON audit. Pass that file as `build_catalog.py --evidence <audit.json>` to include all reviewed variants and completeness evidence. Use `build_missile_names.py --names` to generate `cmd/acmi/data/sam_missile_names.json`. A base catalogue built without the audit still supports flight export, but does not establish that its ammunition candidates are complete.

## Radar geometry and file version

`LockedTargetAzimuth` (positive right) and `LockedTargetElevation` (positive up) are body-relative angles in degrees. They share the existing slant-range checks: positions at or before the event, no more than one second old, and valid object lifetimes. Invalid geometry, unlocks, and target destruction clear angles. A fresh target switch immediately publishes the new geometry. Coincident targets have valid zero range but no angles.

The default header is `FileVersion=2.1`; use `-acmi-version 2.2` to select 2.2 explicitly. These fields do not require a higher default version. Earlier checks covered generated text and tests, but did not establish Tacview 1.9.4 or 1.9.5 GUI loading, rendering, or terrain-loader compatibility.

## Message timeline

With `kill` enabled, ground victims use an annotated native `Event=Destroyed` instead of a duplicate kill `Message`: `<target> destroyed by <attacker vehicle> (<player>) with <weapon>`. This applies to all weapon types. The victim must match an exact recorded identity and death time. Report weapon aliases use reviewed display names; unknown names retain the original report text. `WT_KillSource=F058`, `WT_KillWeapon*`, `WT_KillerVehicle`, `WT_KillerPlayer` and available UID/PID fields retain the attribution evidence at destruction.

An attacker's explicit PID is resolved through the player roster at report time, independently of whether its aircraft still exists. PID is never treated as a vehicle UID, and no deleted attacker object is revived. Missing PID is distinct from slot zero; AI ground objects cannot inherit an unrelated roster player. Missing names stay empty, weapon-only attribution is marked unknown, and ambiguous or conflicting victim/report identities retain the generic destruction event. No blast-radius or nearest-player inference is used. Disabling the `kill` category also disables these annotations without removing ordinary destruction events.

`-events=kill,chat,join,damage,radar` is the default. Use `-events=none` to disable these messages or a comma-separated subset, such as `-events=kill,radar`. Awards are disabled by default; explicitly add `award` to the selection or use `-events=all` to include them. This is independent of `-bookmarks` and the four opt-in CSV files. Event times use the original packet timestamp and the existing ACMI time origin.

- Kill messages use explicit killer/victim identities and the killer's `PlayerWeapon`. Aircraft kills must match the confirmed F058 destruction time exactly. Missing, ambiguous, or conflicting attribution is omitted; `DestroyedWeapon` is not substituted as the killer's weapon. These messages do not move destruction times or assign kills by proximity.
- Chat, player roster changes, critical/severe damage and awards use recorded packet fields. Messages without reliable object attribution remain objectless. Initial player rosters are not treated as join events.
- Damage messages show the victim's vehicle and player followed by `critical damaged by` or `severe damaged by` and the attacker's vehicle and player. Unresolved identities remain unknown; slot numbers, raw UIDs and fire flags are omitted from the displayed message.
- Radar messages describe target designation and clearing, using `Target <name>; Range <metres> m` when distance is available. Range remains the calculated geometric slant range. They use the same prepared state changes as native radar properties, including clears between aircraft position syncs. They do not prove STT lock. `-radar-locks=false` disables native lock properties; omit `radar` from `-events` to disable radar messages too.
- Malformed or oversized event payloads are skipped. Event collectors have bounded retention; repeated identical messages at the same timestamp are deduplicated. A message never recreates an unborn or already deleted object.

## Moving ground direction and destruction

Moving ground units use neighboring recorded positions to derive geographic course. Available headings use the six-component `T` transform and `WT_HeadingSource=PathDerivative`; they are not measured hull or turret attitude. Static units, single-point tracks and unreliable movement intervals retain position-only transforms. No direction is inferred across large recording gaps. An explicit matched ground destruction emits `Event=Destroyed` before object deletion; reaching the last position sample alone does not count as destruction.
