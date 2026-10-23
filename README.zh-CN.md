# wrpl-acmi

[English](README.md) · [使用指南](docs/acmi-export.zh-CN.md) · [构建与发布](docs/standalone-build.zh-CN.md) · [文档目录](docs/README.md)

wrpl-acmi 将《战争雷霆》的回放转换为 Tacview 可读取的 ACMI 文件，用于查看飞行轨迹、作战事件及相关对象。导出器支持客户端的单个 `.wrpl` 文件，也支持同一场服务器回放的分片目录，默认输出 ACMI 2.1。

项目基于 [wrpl-inspector v3](https://github.com/maxsupermanhd/wrpl-inspector)，保留了 Go 回放解析库和 dear-imgui 图形检查器，并提供无图形界面的导出器 `cmd/acmi`。开发分支为 [dev](https://github.com/3794432/wrpl-acmi/tree/dev)；Go 模块路径仍为 `github.com/maxsupermanhd/wrpl-inspector/v3`。

## 快速开始

从[最新发布页](https://github.com/3794432/wrpl-acmi/releases/latest)下载：[wrpl-acmi.exe](https://github.com/3794432/wrpl-acmi/releases/latest/download/wrpl-acmi.exe) 用于导出回放；[wrpl-terrain-installer.exe](https://github.com/3794432/wrpl-acmi/releases/latest/download/wrpl-terrain-installer.exe) 为可选的独立地形安装器，包含 69 张地图。未安装地形也能正常导出。

如果已有发布包，可直接在 PowerShell 中运行导出器。以下示例假定程序位于 `dist\wrpl-acmi`：

```powershell
.\dist\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\dist\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

`wrpl-acmi.exe` 可以单独复制到其他目录运行。默认查找表，包括 ECS 组件名称表 `ecshashes`，已编译在程序中。导出时不需要相邻的 `data/` 目录、本机游戏、已安装地形、Python、Go、Git 或 Tacview；Tacview 用于打开生成的 ACMI 文件。

服务器回放应先下载并解压，将同一场的分片放在独立目录中，再将目录作为输入。程序会根据分片头排序，不接受单个服务器分片。具体规则见[服务器回放分片导出](docs/ACMI_SERVER_REPLAYS.md)。

也可以将一个客户端回放文件或一个分片目录拖到 EXE 上。未指定 `-out` 时，程序在当前工作目录生成 `<输入名>.acmi`；服务器回放缺少尾片时，默认生成 `<目录名>.partial.acmi`，并标注回放不完整。包内的 `run.bat` 是可选入口，运行时会先切换到发布包目录。

命令行还支持单个位置参数。使用这种写法时，所有选项必须位于输入路径之前，且不能再指定 `-in`：

```powershell
.\wrpl-acmi.exe -out "C:\Replays\replay.acmi" "C:\Replays\replay.wrpl"
```

完整参数、输出规则及常见问题见[导出指南](docs/acmi-export.zh-CN.md)。

## 导出内容与数据含义

导出结果同时包含回放记录、由记录推导的估算值，以及静态配置资料。阅读时应结合对象的来源字段区分这三类信息。

| 内容 | 数据来源与主要限制 |
| --- | --- |
| 飞机与地面单位 | 使用回放中的位置。飞机保留飞行模型姿态；移动地面单位增加轨迹推导朝向，确认销毁时先写入 `Destroyed` 再移除。地面型号、阵营及销毁时刻需有明确证据。符合条件的静态建筑可使用带时间戳的 ECS 构造位置。 |
| 导弹与炸弹 | 优先使用实际同步位置。导弹运动参数由位置序列估算；只有构造记录的炸弹仅显示投放位置，并标记 `Release position only`。 |
| 干扰物 | 有可靠弹药证据时区分 Flare 与 Chaff；无法确认时按 Chaff 显示并注明回退原因。缺少同步位置时，短时估算轨迹会单独标记。 |
| 雷达目标指定 | 写入 Tacview 的 `LockedTarget` 属性及可计算的距离、角度。目标指定可能包含跟踪信息，不能据此确认 STT 锁定或雷达工作模式。 |
| 机场与跑道 | 按地图、任务和可核对的雷达记录选择源配置。目录覆盖 69 张地图、457 条任务记录，其中 67 张有可用跑道源定义。 |
| 防空参考范围 | 内嵌表包含 255 个型号。范围来自静态型号配置或明确标注的显示设置，不代表实时交战能力。 |
| 元数据与事件 | 包括标题、作者、时间、来源备注、阵营分组及回放事件消息；奖励和高亮书签需显式开启。飞机遥测估算默认开启，可用 `-estimate-telemetry=false` 关闭。 |

默认 `-events=kill,chat,join,damage,radar`，输出有明确归属和武器的击杀、聊天、玩家名单变化、Critical/Severe 伤害报告及雷达目标指定变化。击杀归属缺失或冲突时跳过；飞机击杀时间必须匹配确认的销毁时刻。可用 `-events=none` 关闭，或选择逗号分隔的类别子集。奖励默认关闭，添加 `award` 或使用 `-events=all` 可开启。高亮书签默认关闭（`-bookmarks=false`），保留普通 Chaff/Flare 日志，避免重复高亮行；需要时设置 `-bookmarks=true`。详见[事件消息说明](docs/acmi-information.zh-CN.md#事件消息时间线)。

轨迹清单、导弹飞行参数、干扰物构造和雷达指定变化的 CSV 报告均为可选输出，只有提供对应路径时才会生成。

飞机速度和载荷等字段采用无风、标准大气等假设；其中 IAS 为 EAS 近似，三轴 G 为比力估算。静态制导类型不能说明导引头在某一时刻的状态，跑道源定义也不能证明该跑道在当前对局中已启用。各项依据和限制见下方专题文档。

## 从源码构建

构建 Windows 发布包需要 PowerShell 和 Go 1.25.6 或更高版本。在 PowerShell 中获取源码并运行构建脚本：

```powershell
git clone --branch dev --single-branch https://github.com/3794432/wrpl-acmi.git
Set-Location .\wrpl-acmi
.\build.ps1
```

默认输出目录为 `dist\wrpl-acmi`，其中包含两个可独立使用的程序：

| 程序 | 用途 |
| --- | --- |
| `wrpl-acmi.exe` | 导出回放，默认数据已内嵌。 |
| `wrpl-terrain-installer.exe` | 将转换后的游戏地形安装到 Tacview。普通构建不包含地形数据，安装时需另行提供转换目录。 |

如果已有完整的地形转换目录，可将其打包到安装器中：

```powershell
.\build.ps1 -TerrainSource ".\converted-terrain"
```

从本机游戏提取和转换地形，还需要 64 位 Python 3.12、Git、转换器依赖及固定版本的 Datamine 配置。环境准备和完整命令见[地形工具说明](tools/terrain/README.zh-CN.md#从零构建)；构建参数、断点续作和发布产物见[构建与发布指南](docs/standalone-build.zh-CN.md)。历史发布包可能落后于 dev，需要当前实现时应从该分支构建。

## 在 Tacview 中使用地形

地形安装是可选步骤。未安装游戏地形时，ACMI 仍会包含导出的回放对象，但 Tacview 的背景地形可能与游戏地图不符。

含地形数据的安装器可直接运行：

```powershell
.\dist\wrpl-acmi\wrpl-terrain-installer.exe
```

默认安装位置为 `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`。安装器需要系统 Windows PowerShell；完整 69 张地图的默认分辨率安装建议预留至少 8 GB 空间。安装后应完全退出并重启 Tacview，再从原始 WRPL 重新导出，以确保轨迹与地形使用同一版坐标配置。

导出和安装默认使用 geographic 布局：69 张地图中，14 张采用经审核的 WGS84 估算位置，55 张使用 Atlas 回退坐标。Atlas 坐标用于技术展示，不代表真实地理位置。显式选择 Atlas 时，安装器的 `-atlas` 必须与导出器的 `-terrain-layout atlas` 配套使用；`-alt` 仅调整垂直偏移。

地形高度来自本机游戏的 `levels/*.bin`，Datamine 提供边界、水位等配置，地理定位则来自 WT-MAP-ANALYZE 的估算资料。这些输入的用途、版本和许可说明见[输入来源与许可](tools/terrain/README.zh-CN.md#输入来源与许可)。

## 文档导航

| 阅读目的 | 文档 |
| --- | --- |
| 使用与构建 | [导出指南](docs/acmi-export.zh-CN.md)、[构建与发布](docs/standalone-build.zh-CN.md)、[服务器回放](docs/ACMI_SERVER_REPLAYS.md) |
| 理解对象与事件 | [飞机遥测与元数据](docs/acmi-information.zh-CN.md)、[地面单位](docs/ACMI_GROUND_UNITS.md)、[炸弹](docs/ACMI_BOMBS.md)、[干扰物](docs/ACMI_COUNTERMEASURES.zh-CN.md)、[雷达目标指定](docs/ACMI_RADAR_LOCKS.md) |
| 查询静态资料 | [机场与跑道](docs/ACMI_AIRFIELDS.zh-CN.md)、[逐地图覆盖](docs/ACMI_AIRFIELDS_COVERAGE.zh-CN.md)、[防空范围](docs/ACMI_AIR_DEFENCE_RANGES.zh-CN.md)、[导弹型号](docs/ACMI_MODELS.md)、[制导信息](docs/ACMI_GUIDANCE.md) |
| 配置地形 | [地形工具](tools/terrain/README.zh-CN.md)、[地理坐标](docs/TERRAIN_GEOGRAPHIC.md)、[Atlas 布局](docs/terrain-atlas.md) |
| 了解上游与研究依据 | [检查器与文档目录](docs/README.md)、[型号证据](docs/evidence/missile-models.md)、[制导证据](docs/evidence/missile-guidance.md) |

## 验证范围与许可

已有样例曾验证 69 架飞机和 140 个地面单位的型号、阵营关联，但该结果不代表所有游戏版本和回放均有同等覆盖率。仓库的[源码构建工作流](.github/workflows/source-build.yml)配置了导出器、解析库及合成地形检查；真实地形转换依赖本机游戏文件，Tacview 图形界面的加载和渲染也需要另行验证。

本项目及 `wrpl` 库按 GNU Affero General Public License v3 发布，详见 [LICENSE](LICENSE)。上游版权与许可声明予以保留。地图和游戏资源归 Gaijin Entertainment 所有；本项目与 Gaijin 无隶属或背书关系。
