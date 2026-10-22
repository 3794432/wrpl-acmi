# 跑道逐地图覆盖

[English](ACMI_AIRFIELDS_COVERAGE.md)

目录含 69 张地图，67 张取得可用源定义，共 457 个任务配置。

定义数包含不同任务、等级和条件分支，不代表同时出现的机场数。条件布局还需要回放证据确认；来源缺失时不补造跑道。

| 地图 | 任务数 | 可用源定义数 | 状态 |
|---|---:|---:|---|
| `air_afghan` | 8 | 126 | 源定义已配置 |
| `air_africa_desert` | 4 | 20 | 源定义已配置 |
| `air_archipelago` | 2 | 24 | 源定义已配置 |
| `air_denmark` | 3 | 71 | 源定义已配置 |
| `air_equatorial_island` | 2 | 12 | 源定义已配置 |
| `air_falklands` | 2 | 10 | 源定义已配置 |
| `air_grand_canyon` | 3 | 106 | 源定义已配置 |
| `air_israel` | 2 | 16 | 源定义已配置 |
| `air_kamchatka` | 3 | 30 | 源定义已配置 |
| `air_ladoga` | 8 | 33 | 源定义已配置 |
| `air_mysterious_valley` | 3 | 24 | 源定义已配置 |
| `air_normandy` | 3 | 12 | 源定义已配置 |
| `air_pyrenees` | 2 | 16 | 源定义已配置 |
| `air_race_phiphi_islands` | 8 | 0 | 见下方说明 |
| `air_skyscraper_city` | 3 | 16 | 源定义已配置 |
| `air_smolensk` | 6 | 129 | 源定义已配置 |
| `air_south_eastern_city` | 5 | 36 | 源定义已配置 |
| `air_southeastern_cliffs` | 2 | 16 | 源定义已配置 |
| `air_vietnam` | 5 | 121 | 源定义已配置 |
| `arcade_africa_canyon` | 4 | 6 | 源定义已配置 |
| `arcade_africa_seashore` | 1 | 2 | 源定义已配置 |
| `arcade_alps` | 4 | 4 | 源定义已配置 |
| `arcade_asia_4roads` | 4 | 6 | 源定义已配置 |
| `arcade_canyon_snow` | 1 | 2 | 源定义已配置 |
| `arcade_ireland` | 7 | 4 | 源定义已配置 |
| `arcade_mediterranean` | 1 | 2 | 源定义已配置 |
| `arcade_norway_fjords` | 1 | 2 | 源定义已配置 |
| `arcade_norway_green` | 1 | 2 | 源定义已配置 |
| `arcade_norway_plain` | 1 | 2 | 源定义已配置 |
| `arcade_phiphi_crater` | 1 | 2 | 源定义已配置 |
| `arcade_phiphi_crater_rocks` | 2 | 2 | 源定义已配置 |
| `arcade_rice_terraces` | 5 | 10 | 源定义已配置 |
| `arcade_snow_rocks` | 3 | 2 | 源定义已配置 |
| `arcade_tabletop_mountain` | 3 | 2 | 源定义已配置 |
| `arcade_zhang_park` | 1 | 2 | 源定义已配置 |
| `avg_egypt_sinai` | 10 | 108 | 源定义已配置 |
| `berlin` | 18 | 28 | 源定义已配置 |
| `britain` | 17 | 16 | 源定义已配置 |
| `bulge` | 11 | 102 | 源定义已配置 |
| `caribbean_islands` | 0 | 0 | 见下方说明 |
| `dover_strait` | 5 | 88 | 源定义已配置 |
| `guadalcanal` | 20 | 10 | 源定义已配置 |
| `guam` | 5 | 10 | 源定义已配置 |
| `honolulu` | 11 | 15 | 源定义已配置 |
| `hurtgen` | 3 | 17 | 源定义已配置 |
| `iwo_jima` | 6 | 16 | 源定义已配置 |
| `khalkhin_gol` | 9 | 107 | 源定义已配置 |
| `korea` | 10 | 18 | 源定义已配置 |
| `korsun` | 13 | 118 | 源定义已配置 |
| `krymsk` | 19 | 36 | 源定义已配置 |
| `kursk` | 20 | 119 | 源定义已配置 |
| `malta` | 11 | 9 | 源定义已配置 |
| `midway` | 3 | 4 | 源定义已配置 |
| `moscow` | 13 | 65 | 源定义已配置 |
| `mozdok` | 19 | 26 | 源定义已配置 |
| `mozdok_winter` | 1 | 10 | 源定义已配置 |
| `norway` | 9 | 15 | 源定义已配置 |
| `peleliu` | 8 | 9 | 源定义已配置 |
| `port_moresby` | 11 | 74 | 源定义已配置 |
| `ruhr` | 17 | 145 | 源定义已配置 |
| `saipan` | 6 | 11 | 源定义已配置 |
| `sector_montmedy` | 2 | 96 | 源定义已配置 |
| `sicily` | 13 | 103 | 源定义已配置 |
| `spain` | 12 | 136 | 源定义已配置 |
| `stalingrad` | 19 | 157 | 源定义已配置 |
| `stalingrad_w` | 10 | 109 | 源定义已配置 |
| `tunisia` | 3 | 108 | 源定义已配置 |
| `wake_island` | 3 | 1 | 源定义已配置 |
| `zhengzhou` | 6 | 203 | 源定义已配置 |

## 未配置原因

- `air_race_phiphi_islands`: 已核对竞速任务：为空中出发，未定义陆地跑道。
- `caribbean_islands`: 当前有界来源扫描尚未找到可用陆地跑道定义；不据此断言地图没有跑道。

每个机场的实际选择和高度来源见 ACMI 的 `WT_Airfield*` 字段。
