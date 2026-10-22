# wrpl-acmi ACMI 导出指南

[English](acmi-export.md) · [项目说明](../README.zh-CN.md)

本页对应当前 `dev` 的 `cmd/acmi`。地形定位见[地理地形](TERRAIN_GEOGRAPHIC.md)和 [Atlas 布局](terrain-atlas.md)。

## 输入与输出

- 客户端回放：单个 `.wrpl` 文件。
- 服务器回放：同一场的分片目录。目录按分片头排序，文件名不需要手工改序；不要混入其他场次。服务器回放的单片文件不能直接作为输入。
- 尾片缺失：默认 `-allow-incomplete=true`，导出已下载部分并写入不完整标记；设置 `-allow-incomplete=false` 可恢复严格拒绝。
- 中间分片缺失、分片 0 缺失、重复分片、场次混杂或压缩数据损坏会报错。输入文件不会被改写。

目录读取逐片打开和解压，不把所有原始分片同时装进内存；最终内存仍取决于整场轨迹规模。

```powershell
.\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
.\wrpl-acmi.exe -out "C:\Replays\replay.acmi" "C:\Replays\replay.wrpl"
```

参数要放在位置参数之前，或统一使用 `-in`。不能同时提供 `-in` 和位置参数，也不能给多个位置参数。

未指定 `-out` 时，输出写到当前工作目录：普通文件使用 `<输入基本名>.acmi`；完整分片目录使用 `<目录名>.acmi`；尾片缺失的分片目录使用 `<目录名>.partial.acmi`。把单个文件或单个目录拖到 EXE 也遵循这条规则；一次只拖一个输入。runtime 包中的 `run.bat` 会先切换到包目录。

`-ecshashes` 默认读取 `data/ecshashes.json`，也会尝试 EXE 同目录下的相对路径。该表缺失时导出器会降级，实体类名、型号、队伍、飞行员和导弹构造信息可能缺失。

## 命令参数

| 参数 | 含义 |
| --- | --- |
| `-in` | 输入 `.wrpl` 或服务器分片目录 |
| `-out` | 输出 `.acmi` 路径 |
| `-allow-incomplete` | 是否允许尾片缺失；默认 `true` |
| `-terrain-layout` | `geographic`（默认）或 `atlas`（当前技术布局） |
| `-map-id` | 无法自动识别地图时显式指定地图 ID |
| `-alt` | 垂直高度偏移，单位米；不改变水平摆放 |
| `-ecshashes` | ECS 组件哈希表，默认 `data/ecshashes.json`；空值表示关闭 |
| `-missile-models` | `embedded`（默认）、`off` 或 schema-1 型号表 JSON 路径 |
| `-missile-guidance` | `embedded`（默认）、`off` 或 schema-1 制导表 JSON 路径 |
| `-countermeasures` | 是否导出飞机干扰物；默认 `true` |
| `-radar-locks` | 是否写雷达目标指定；默认 `true` |
| `-ground-units` | 是否导出记录的地面位置同步；默认 `true` |
| `-ground-models` | 是否按显式回放证据识别地面型号与派生范围；默认 `true` |
| `-airfields` | 是否导出源配置的机场标记和跑道；默认 `true` |
| `-estimate-telemetry` | 是否由位置和姿态估算飞机航向、速度、Mach、IAS、AOA 与三轴 G；默认 `true` |
| `-bookmarks` | 是否新增事件时间轴书签；默认 `true` |
| `-author` | 文件作者或操作员，默认 `wrpl-acmi` |
| `-acmi-version` | `2.1`（默认）或显式选用 `2.2` |
| `-list` | 轨迹与当前名称 CSV 的可选输出路径 |
| `-missile-flight-log` | 导弹飞行参数与原始运动证据 CSV 的可选输出路径 |
| `-countermeasure-log` | 干扰物构造 CSV 的可选输出路径 |
| `-radar-lock-log` | 雷达目标指定状态变化 CSV 的可选输出路径 |
| `-names` | 可选 JSON，覆盖 `air:`、`ground:`、`missile:`、`bomb:` 等显示名 |

四类 CSV 默认都不输出；只有对应参数获得非空路径时才写出文件。`-estimate-telemetry` 同样默认开启，设置 `-estimate-telemetry=false` 可关闭飞机估算字段。

## 导出内容

### 飞机、元数据与书签

飞机使用记录的位置和飞行模型姿态。估算开启时，飞机更新写入原生 `HDG`、`TAS`、`Mach`、`IAS`、`VerticalGForce`、`LongitudinalGForce`、`LateralGForce` 和 `AOA`。航向来自记录姿态；TAS 在无风假设下使用轨迹速度；Mach 使用分层标准大气；IAS 是密度比 EAS 近似 `TAS * sqrt(rho / rho0)`；AOA 来自无风速度相对机体轴的方向；三轴 G 使用比力估算。缺失或被拒绝的样本会显式清空对应字段。

文件头写入地图、模式与回放时间组成的 `Title`、`-author` 值、UTC `RecordingTime`、`Category=War Thunder`、`DataRecorder=wrpl-acmi`，以及构建版本、布局、地理参考、估算假设、干扰物规则和机场边界等来源备注。`ReferenceTime` 仍表示回放时间基准。

对象附带明确阵营派生的分组。已确认的飞机击毁、导弹发射或首次观测、炸弹投放或首次观测、干扰物释放和雷达目标指定变化可生成时间轴书签；同一时刻的重复书签会去重。目标指定不证明 STT 硬锁或雷达开机。详见[元数据、书签与飞机估算](acmi-information.zh-CN.md)。

### 地面单位、静态对象与机场

地面单位使用同步位置轨迹。只有明确的 UID、型号和阵营证据才用于关联名称与阵营；缺少证据时保留通用身份，只有事件证据匹配才应用销毁。`-ground-units=false` 关闭批量同步地面轨迹；`-ground-models=false` 关闭型号派生的显示和威胁范围，独立的阵营证据仍可使用。

没有同步轨迹的建筑或工事可在存在明确静态类别证据时，使用成功解码且带时间戳的 ECS 构造平移。位置或身份冲突、UID 回收、位置变化、非有限值和 `dummy` 任务占位物都会排除。接受的对象使用 `Ground+Static+Building`，保留型号和阵营证据，并写入 `WT_GroundPositionSource=ECSConstructionTransform`、`WT_GroundStatic=1`。已有同步轨迹优先。详见[地面单位](ACMI_GROUND_UNITS.md)和[静态地面对象](acmi-information.zh-CN.md#静态地面对象)。

机场目录包含 69 张地图、457 条任务记录，其中 67 张地图有可用源定义。定义数包含任务、等级和条件分支，不代表同时出现的机场数。条件布局必须用回放中的主雷达和飞行雷达身份按实现中的几何容差匹配；无条件源放置可直接导出，歧义或缺失几何会省略，不用通用跑道填补。

`air_race_phiphi_islands`（PhiPhi）的 8 条竞速任务都定义为空中出发，没有陆地跑道来源；`caribbean_islands`（Caribbean）在有界来源扫描中没有可靠的任务或陆地跑道来源。这不声明任意游戏版本中该地图永远不存在跑道。机场标记通过 `WT_Airfield*` 保留来源提交与路径、预制资产、任务组、雷达身份、位置和高度来源；跑道航向表示几何轴线，不是当前降落方向。机场雷达保留游戏缩写 `AFF Radar` 和 `AFM Radar`（飞行雷达与主雷达），完整型号保存在 `WT_GroundModel`。详见[机场定位与跑道](ACMI_AIRFIELDS.zh-CN.md)和[逐地图覆盖](ACMI_AIRFIELDS_COVERAGE.zh-CN.md)。

### 导弹、炸弹与干扰物

导弹轨迹保留同步位置；速度、加速度、转向率和 G 值来自位置序列时间差分。型号候选、静态制导类型与 INS 秒数保留在 ACMI 字段和 `INS x.xx s` 标签中，这些是回放位置的估计，不是解码出的逐帧物理量。详见[导弹型号](ACMI_MODELS.md)和[制导证据](ACMI_GUIDANCE.md)。

炸弹有 `WeaponSync` 记录时导出真实位置；只有构造记录时，在投放位置写出 `Release position only` 标记，不补造轨迹或命中时间。详见[炸弹](ACMI_BOMBS.md)。

只有完整且明确的发射者弹药证据能识别单一活动弹种时，干扰物才使用原生 Flare/Chaff 类型。无法判断或混装时统一按 Chaff 显示，并写入 `WT_CountermeasureTypeConfidence=fallback` 和 `UnknownAsChaff` 来源标记；这是明确的显示规则，不表示回放已识别为箔条。真实同步位置优先，否则生成并标记短时估算轨迹。详见[干扰物类型](ACMI_COUNTERMEASURES.zh-CN.md)。

### 雷达指定与防空范围

雷达目标指定使用原生 `LockedTarget`、`LockedTarget2` 至 `LockedTarget9`、`LockedTargetMode` 和 `LockedTargetRange`。斜距使用当前或至多一秒前的有效位置；可用时，`LockedTargetAzimuth` 和 `LockedTargetElevation` 是机体相对角度，向右和向上为正。解锁、目标销毁或几何无效会清空相应字段。这些记录不证明 STT 硬锁、雷达模式或武器制导。详见[雷达指定](ACMI_RADAR_LOCKS.md)。

内嵌静态威胁表包含 255 个防空型号。有来源证明无雷达且仅有火炮/机枪的型号不输出威胁圈，共 142 个。有雷达的炮车、ZA-35、ZSU-23-4、ZSU-37-2 以及红外或混合制导 SAM 保留。ZSU-37-2 输出用户确认的 2,000 米显示半径，`WT_EngagementRangeSource` 同时记录固定 Datamine `attackMaxDist` 原值为 4,000 米。范围是型号静态参考，不代表实时雷达状态或交战包线。详见[防空静态范围](ACMI_AIR_DEFENCE_RANGES.zh-CN.md)。

## 地图与地形

默认 `-terrain-layout geographic`。权威目录恰好包含 69 张地图：14 张使用审核后的 WGS84 估算位置（5 张高置信、9 张中置信），55 张使用 Atlas 回退。地形高程 XML 与飞机、导弹、炸弹、地面单位和干扰物共用同一份 GeoReferences 变换。已有 ACMI 要与当前地形匹配时，应从对应原始 WRPL 重新导出；`-alt` 只增加垂直偏移。

`-terrain-layout atlas` 是当前技术布局，与安装器的 `-AtlasOnly` 配对。地图 `.data` 不纳入 Git，源码仓库不附带地形资产。`-WithTerrain` 构建会把 69 张转换地图放进 `dist/wrpl-acmi/terrain`；使用 `dist/wrpl-acmi/tools/terrain/Install-All-Terrains.cmd` 安装。详见[地理地形](TERRAIN_GEOGRAPHIC.md)、[Atlas 布局](terrain-atlas.md)和[地形工具说明](../tools/terrain/README.zh-CN.md)。

## 构建与检查

导出器要求 Go 1.25.6 或更高版本（`go.mod` 当前声明 1.25.6）。完整运行包从仓库根目录按[项目说明](../README.zh-CN.md)构建；带地形构建还需要 64 位 Python 3.12、Git 和本机游戏文件：

```powershell
.\build.ps1 -WithTerrain -GameRoot "C:\Path\To\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -Python ".\.venv\Scripts\python.exe"
```

只检查和构建无界面导出器：

```powershell
$env:CGO_ENABLED = "0"
go build -o .\wrpl-acmi.exe .\cmd\acmi
go test .\cmd\acmi .\wrpl\...
```

包构建会把 `README.md`、`README.zh-CN.md`、`LICENSE`、`docs/`、运行数据 `data/`，以及 `tools/terrain`、`tools/airfields`、`tools/countermeasures` 工具目录复制到 `dist/wrpl-acmi`；`-WithTerrain` 再增加转换后的 `terrain/`。当前树的本地源码构建已成功完成。历史发布物可能落后于 `dev`；需要最新行为时从当前分支构建。

源码构建工作流覆盖导出器与库测试、合成地形解码、机场地形采样、安装器检查、地形格式转换和纯导出器包构建。包级 smoke 检查单独跟踪。真实地形转换依赖本机游戏文件，不在 CI 中执行；CI 不声明 Tacview GUI 加载或渲染兼容性已经完成。

## 证据边界

导弹和飞机的运动数据来自记录位置与姿态，未包含完整气动力、推力、质量、燃料、天气或传感器状态。飞机 IAS 是 EAS 近似，不是座舱读数。缺失弹药证据时不能确认干扰物类型，无法判断的记录按 Chaff 显示；无位置记录的干扰物轨迹是显式估算；只有构造记录的炸弹是投放位置标记。静态制导类型与防空范围不证明某一时刻已锁定、雷达已开机或处于可击杀状态。

机场覆盖取决于固定来源目录和条件布局的回放证据；目录条目不保证跑道处于活动状态，未解决来源不用猜测填补。本文档已按实现、测试覆盖和生成的目录核对，没有把 Tacview GUI 加载与实际渲染当作已验证项。
