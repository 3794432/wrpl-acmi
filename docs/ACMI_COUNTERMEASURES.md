# Countermeasure types and trajectories

[简体中文](ACMI_COUNTERMEASURES.zh-CN.md) · [Export guide](acmi-export.md)

Countermeasure releases use Tacview's native `Misc+Decoy+Flare` or `Misc+Decoy+Chaff` types. A release that cannot be reliably identified is exported as **Chaff**, with `WT_CountermeasureTypeConfidence=fallback` and `WT_CountermeasureTypeSource=UnknownAsChaff:<reason>`. This is an export policy, not a claim that the replay identified chaff. No velocity, unknown flag, registry-id or emitter-index heuristic selects a type.

## Type identification

The exporter validates aircraft storage field `0x33`, then retains the launcher, ammunition name, and count for the exact owner EID before release. It matches those names against an embedded catalogue. An empty ammunition name uses the default `bullet` block only when that block is explicitly defined.

A type can be identified only when all active countermeasure ammunition in the known loadout has the same type. Mixed loadouts remain unresolved, and zero-count records are not active ammunition evidence. The low 16 bits of `WeaponRef` identify a launcher, not a Flare/Chaff enum. Ammunition snapshots are kept separate for each entity generation.

## Catalogue source

The catalogue covers four reviewed split-launcher configurations, including large, MAW and BOL variants. Its source is [War-Thunder-Datamine at f7952e4](https://github.com/gszabi99/War-Thunder-Datamine/tree/f7952e4291e9fd7b6fe38e84020dddef925c3457/aces.vromfs.bin_u/gamedata/weapons/rocketguns): the outer `bulletType=flr` means Flare and `bulletType=chff` means Chaff. Unknown launchers or ammunition use the Chaff fallback. Coverage remains dependent on the game version.

## Trajectories and diagnostic fields

The optional `-countermeasure-log output.csv` report includes `kind`, `type_source`, `type_confidence`, `launcher`, and `bullet` after the original 17 columns. ACMI records the type, source, and confidence in `WT_CountermeasureType*`; ordinary messages use the selected Flare/Chaff name. Highlighted bookmarks default to off (`-bookmarks=false`), avoiding duplicate highlighted release rows; use `-bookmarks=true` to opt in.

Type classification is independent of trajectory estimation. A name ending in `(estimated)` refers to the existing short release-trajectory estimate; `WT_Estimated=1` and `WT_TrajectorySource=EstimatedFromRelease` remain. Recorded WeaponSync positions always win. Object IDs, parent links, lifetimes and the trajectory model are unchanged.

## Rebuilding the catalogue

Run either command from the repository root to rebuild the embedded catalogue from pinned, hash-verified source files:

```powershell
python tools/countermeasures/build_catalog.py
python tools/countermeasures/build_catalog.py --source-root C:/War-Thunder-Datamine
```

The second command works offline and requires the exact pinned files. Running the exporter does not require an asset download. Use `-countermeasures=false` to disable countermeasure export. These types are supported in ACMI 2.1.
