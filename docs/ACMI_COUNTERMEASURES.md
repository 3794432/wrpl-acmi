# Countermeasure identity

[简体中文](ACMI_COUNTERMEASURES.zh-CN.md)

Countermeasure releases use Tacview's native `Misc+Decoy+Flare` or `Misc+Decoy+Chaff` types. A release that cannot be reliably identified is exported as **Chaff**, with `WT_CountermeasureTypeConfidence=fallback` and `WT_CountermeasureTypeSource=UnknownAsChaff:<reason>`. This is an export policy, not a claim that the replay identified chaff. No velocity, unknown flag, registry-id or emitter-index heuristic selects a type.

The exporter reads bounded aircraft storage field `0x33`, retains the launcher, ammunition name and count from the exact owner EID before release, and matches those names against an embedded catalogue. An empty ammunition name selects the explicit default `bullet` block. Only a single active countermeasure type across the known loadout is direct type evidence; a mixed loadout remains unresolved and therefore uses the Chaff fallback. Zero-count records do not provide active-ammunition evidence. The low 16 bits of `WeaponRef` identify a launcher, not a global Flare/Chaff enum. Different entity generations do not share ammunition snapshots.

The catalogue covers four reviewed split-launcher configurations, including large, MAW and BOL variants. Its source is [War-Thunder-Datamine at f7952e4](https://github.com/gszabi99/War-Thunder-Datamine/tree/f7952e4291e9fd7b6fe38e84020dddef925c3457/aces.vromfs.bin_u/gamedata/weapons/rocketguns): the outer `bulletType=flr` means Flare and `bulletType=chff` means Chaff. Unknown launchers or ammunition also fall back to Chaff. This catalogue is not a promise of coverage for every game version.

`-countermeasure-log output.csv` appends `kind`, `type_source`, `type_confidence`, `launcher` and `bullet` after the existing 17 columns. ACMI exposes the type, source and confidence through `WT_CountermeasureType*`. Messages and bookmarks use the selected Flare/Chaff name.

Type classification is independent of trajectory estimation. A name ending in `(estimated)` refers to the existing short release-trajectory estimate; `WT_Estimated=1` and `WT_TrajectorySource=EstimatedFromRelease` remain. Recorded WeaponSync positions always win. Object IDs, parent links, lifetimes and the trajectory model are unchanged.

Rebuild the embedded catalogue from pinned, hash-verified source files:

```powershell
python tools/countermeasures/build_catalog.py
python tools/countermeasures/build_catalog.py --source-root C:/War-Thunder-Datamine
```

The second command is offline and requires the exact pinned files. No asset download is needed to run the exporter. `-countermeasures=false` disables countermeasure export as before. The types use ACMI 2.1 and do not require 2.2.
