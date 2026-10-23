# Runway coverage by map

[中文](ACMI_AIRFIELDS_COVERAGE.zh-CN.md) · [Airfield selection](ACMI_AIRFIELDS.md)

The catalogue contains 69 maps and 457 mission entries. Usable runway definitions are available for 67 maps. This page describes source coverage; actual export also depends on the replay's map, mission, and evidence for conditional layouts.

The mission count is the number of catalogue entries for that map. Definition counts include alternatives across missions, ranks, and conditions, so they are not counts of airports active in one match. Missing or ambiguous geometry is omitted.

## Coverage table

| Map | Missions | Usable source definitions | Status |
|---|---:|---:|---|
| `air_afghan` | 8 | 126 | Source configured |
| `air_africa_desert` | 4 | 20 | Source configured |
| `air_archipelago` | 2 | 24 | Source configured |
| `air_denmark` | 3 | 71 | Source configured |
| `air_equatorial_island` | 2 | 12 | Source configured |
| `air_falklands` | 2 | 10 | Source configured |
| `air_grand_canyon` | 3 | 106 | Source configured |
| `air_israel` | 2 | 16 | Source configured |
| `air_kamchatka` | 3 | 30 | Source configured |
| `air_ladoga` | 8 | 33 | Source configured |
| `air_mysterious_valley` | 3 | 24 | Source configured |
| `air_normandy` | 3 | 12 | Source configured |
| `air_pyrenees` | 2 | 16 | Source configured |
| `air_race_phiphi_islands` | 8 | 0 | See notes below |
| `air_skyscraper_city` | 3 | 16 | Source configured |
| `air_smolensk` | 6 | 129 | Source configured |
| `air_south_eastern_city` | 5 | 36 | Source configured |
| `air_southeastern_cliffs` | 2 | 16 | Source configured |
| `air_vietnam` | 5 | 121 | Source configured |
| `arcade_africa_canyon` | 4 | 6 | Source configured |
| `arcade_africa_seashore` | 1 | 2 | Source configured |
| `arcade_alps` | 4 | 4 | Source configured |
| `arcade_asia_4roads` | 4 | 6 | Source configured |
| `arcade_canyon_snow` | 1 | 2 | Source configured |
| `arcade_ireland` | 7 | 4 | Source configured |
| `arcade_mediterranean` | 1 | 2 | Source configured |
| `arcade_norway_fjords` | 1 | 2 | Source configured |
| `arcade_norway_green` | 1 | 2 | Source configured |
| `arcade_norway_plain` | 1 | 2 | Source configured |
| `arcade_phiphi_crater` | 1 | 2 | Source configured |
| `arcade_phiphi_crater_rocks` | 2 | 2 | Source configured |
| `arcade_rice_terraces` | 5 | 10 | Source configured |
| `arcade_snow_rocks` | 3 | 2 | Source configured |
| `arcade_tabletop_mountain` | 3 | 2 | Source configured |
| `arcade_zhang_park` | 1 | 2 | Source configured |
| `avg_egypt_sinai` | 10 | 108 | Source configured |
| `berlin` | 18 | 28 | Source configured |
| `britain` | 17 | 16 | Source configured |
| `bulge` | 11 | 102 | Source configured |
| `caribbean_islands` | 0 | 0 | See notes below |
| `dover_strait` | 5 | 88 | Source configured |
| `guadalcanal` | 20 | 10 | Source configured |
| `guam` | 5 | 10 | Source configured |
| `honolulu` | 11 | 15 | Source configured |
| `hurtgen` | 3 | 17 | Source configured |
| `iwo_jima` | 6 | 16 | Source configured |
| `khalkhin_gol` | 9 | 107 | Source configured |
| `korea` | 10 | 18 | Source configured |
| `korsun` | 13 | 118 | Source configured |
| `krymsk` | 19 | 36 | Source configured |
| `kursk` | 20 | 119 | Source configured |
| `malta` | 11 | 9 | Source configured |
| `midway` | 3 | 4 | Source configured |
| `moscow` | 13 | 65 | Source configured |
| `mozdok` | 19 | 26 | Source configured |
| `mozdok_winter` | 1 | 10 | Source configured |
| `norway` | 9 | 15 | Source configured |
| `peleliu` | 8 | 9 | Source configured |
| `port_moresby` | 11 | 74 | Source configured |
| `ruhr` | 17 | 145 | Source configured |
| `saipan` | 6 | 11 | Source configured |
| `sector_montmedy` | 2 | 96 | Source configured |
| `sicily` | 13 | 103 | Source configured |
| `spain` | 12 | 136 | Source configured |
| `stalingrad` | 19 | 157 | Source configured |
| `stalingrad_w` | 10 | 109 | Source configured |
| `tunisia` | 3 | 108 | Source configured |
| `wake_island` | 3 | 1 | Source configured |
| `zhengzhou` | 6 | 203 | Source configured |

## Maps without usable runway sources

- `air_race_phiphi_islands`: all eight checked race missions define air starts and no land-runway geometry.
- `caribbean_islands`: the bounded CTA ship and event-name search found no mission whose root level is this map. The terrain level exists, but the checked mission sources establish neither a land runway nor carrier-only operation. This does not establish absence across all game versions.

Actual selection and elevation provenance are recorded in the ACMI `WT_Airfield*` fields.
