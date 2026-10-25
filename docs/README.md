# Documentation / 文档目录

[Project README](../README.md) · [中文项目说明](../README.zh-CN.md)

Start with the export guide to convert a replay, or the build guide to produce a Windows package. The references below explain individual object types, data sources, and limitations. Some specialist pages are currently available only in Chinese.

首次使用请阅读导出指南；需要自行构建程序时，请阅读构建与发布指南。专题文档用于查询对象含义、数据来源和限制，研究报告则保留特定样例的推断依据。

## Use and build / 使用与构建

| Topic / 主题 | English | 中文 |
| --- | --- | --- |
| Export options / 导出操作与参数 | [Export guide](acmi-export.md) | [导出指南](acmi-export.zh-CN.md) |
| Windows package / 构建与发布 | [Build guide](standalone-build.md) | [构建与发布](standalone-build.zh-CN.md) |
| Server replay input / 服务器分片 | — | [服务器回放](ACMI_SERVER_REPLAYS.md) |
| Terrain tools / 地形提取与安装 | [Terrain guide](../tools/terrain/README.md) | [地形工具](../tools/terrain/README.zh-CN.md) |

## Data references / 数据与对象

| Topic / 主题 | English | 中文 |
| --- | --- | --- |
| Metadata, bookmarks, estimates / 元数据、书签与估算 | [Aircraft information](acmi-information.md) | [飞机遥测与元数据](acmi-information.zh-CN.md) |
| Ground units / 地面单位 | — | [地面单位](ACMI_GROUND_UNITS.md) |
| Airfields / 机场与跑道 | [Airfields](ACMI_AIRFIELDS.md) | [机场与跑道](ACMI_AIRFIELDS.zh-CN.md) |
| Runway coverage / 跑道覆盖 | [Map coverage](ACMI_AIRFIELDS_COVERAGE.md) | [逐地图覆盖](ACMI_AIRFIELDS_COVERAGE.zh-CN.md) |
| Countermeasures / 干扰物 | [Countermeasures](ACMI_COUNTERMEASURES.md) | [干扰物类型](ACMI_COUNTERMEASURES.zh-CN.md) |
| Bombs / 炸弹 | — | [炸弹导出](ACMI_BOMBS.md) |
| Radar designations / 雷达目标指定 | — | [雷达目标指定](ACMI_RADAR_LOCKS.md) |
| Sensor states / 雷达与导引头状态 | [Sensor states](ACMI_SENSOR_STATES.md) | [雷达与导引头状态](ACMI_SENSOR_STATES.zh-CN.md) |
| Air-defence ranges / 防空范围 | [Static ranges](ACMI_AIR_DEFENCE_RANGES.md) | [防空静态范围](ACMI_AIR_DEFENCE_RANGES.zh-CN.md) |
| Missile models / 导弹型号 | — | [型号识别](ACMI_MODELS.md) |
| Missile guidance / 导弹制导 | — | [制导信息](ACMI_GUIDANCE.md) |

## Terrain and evidence / 地形与研究依据

| Topic / 主题 | Reference / 文档 |
| --- | --- |
| Geographic placement / 地理坐标 | [地形与回放对象的地理坐标](TERRAIN_GEOGRAPHIC.md)（中文） |
| Atlas coordinates / 技术坐标 | [Atlas 布局](terrain-atlas.md)（中文） |
| Model evidence / 型号证据 | [型号数据表与研究依据](evidence/missile-models.md)（中文） |
| Guidance evidence / 制导证据 | [制导数据表与研究依据](evidence/missile-guidance.md)（中文） |
| Converter contract / 转换器契约 | [Build contract](../tools/terrain/converter/BUILD-CONTRACT.md) (English) |

Evidence reports describe historical samples and may refer to research files outside this repository. Those paths are provenance records, not installation or build requirements.

研究报告中的历史文件路径用于说明依据来源，不是安装步骤；样例中的识别率和测试结果也不应推广为所有回放的保证。

## Upstream inspector

The graphical inspector and replay library are inherited from wrpl-inspector. They complement the command-line exporter by exposing packets and parser results for investigation.

![Inspector preview](assets/preview.gif)

### Components and build

| Component | Role |
| --- | --- |
| `inspector` | GUI library with pluggable tabs for viewing replay packets and parser results. |
| `wrpl` | Standalone replay parsing library with pluggable parsers. |

To build the sample inspector application, run `go build` in `cmd/inspector`. Its graphical dependencies are separate from the headless exporter's CGO-disabled build.

### Capabilities

The inherited inspector supports static header parsing, settings and results BLK sections when present, packet streams, and multiple open replay files. It can download server replays by session ID and combine segmented replays.

Supported packet views include chat, much of the ECS system, awards, kills, ground movement, aircraft state, camera angles, critical and fatal damage, and player information. A kill log and map view are also available. Protocol coverage may be incomplete or inaccurate.

Potential development areas include comparing packets across replays and synchronizing packet views with video.

### Contributing and attribution

Parser improvements and GUI tabs can be proposed through issues or pull requests. The upstream notes list Discord `@flexcoral` (343418440423309314) and this [community invite](https://discord.com/invite/DFsMKWJJPN) as contact routes. Some historical work took place in a private repository and is not part of this checkout.

The upstream author credits the StatShark community for the initial motivation, alongside:

- [Sgambe33's WT-Plotter](https://github.com/Sgambe33/WT-Plotter), for motivation and an initial reference.
- [wt_blk](https://github.com/Warthunder-Open-Source-Foundation/wt_blk), for BLK parsing.
- [llama-for3ver's wt_replay_decoder](https://github.com/llama-for3ver/wt_replay_decoder), for packet-stream parsing.
- [LivingTheDagor's WrplReplayParser](https://github.com/LivingTheDagor/WrplReplayParser), whose author developed inspector capabilities jointly with the upstream author, including ECS and aircraft-state work.

### License

The UI and `wrpl` library use GNU Affero General Public License v3; see the repository [LICENSE](../LICENSE).

The upstream notes also describe `HackNerdFontMono-Regular.ttf` as a setup aid outside the wrpl-inspector distribution, licensed under Bitstream-Vera and MIT. This historical note does not imply that the font is bundled in every checkout.

wrpl-inspector and wrpl are not affiliated with or endorsed by Gaijin Entertainment. War Thunder is a registered trademark of Gaijin Entertainment. All rights reserved.
