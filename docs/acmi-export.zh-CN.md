# ACMI 导出指南

[English](acmi-export.md) · [项目说明](../README.zh-CN.md) · [构建与发布](standalone-build.zh-CN.md)

本指南介绍当前 dev 分支导出器的输入要求、常用操作和命令参数。首次使用可先完成一次导出，再按需要查阅对象类型、估算数据和地形配置的专题说明。

## 准备回放并导出

客户端回放使用单个 `.wrpl` 文件；服务器回放使用同一场对局的分片目录。服务器分片按文件头排序，无需修改文件名，但不能混入其他场次，也不能将单个分片直接作为输入。网页链接和 ZIP 文件需先下载、解压。

```powershell
.\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

导出器不会改写输入回放。默认查找数据已内嵌在 EXE 中，因此导出不需要相邻的 `data/` 目录、本机游戏、已安装地形、Python、Go、Git 或 Tacview。Tacview 用于查看生成的 ACMI。

也可以省略 `-in`，只提供一个输入路径。由于 Go 的参数解析会在第一个位置参数处停止，所有选项必须写在路径之前：

```powershell
.\wrpl-acmi.exe -out "C:\Replays\replay.acmi" "C:\Replays\replay.wrpl"
```

两种输入写法不能混用，也不能同时提供多个位置参数。

### 输出位置与命名

未指定 `-out` 时，文件写入当前工作目录。将一个文件或一个目录拖到 EXE 上，也遵循相同规则。

| 输入 | 默认输出名 |
| --- | --- |
| 客户端 `replay.wrpl` | `replay.acmi` |
| 完整服务器分片目录 `server-session` | `server-session.acmi` |
| 缺少尾片的服务器分片目录 `server-session` | `server-session.partial.acmi` |

发布包中的 `run.bat` 会先切换到包目录，再调用导出器。需要明确控制输出位置时，建议使用 `-out` 指定完整路径。

### 不完整的服务器回放

默认 `-allow-incomplete=true` 允许导出缺少尾片的回放，并在 ACMI 中注明 Incomplete。若只接受完整回放，可设置 `-allow-incomplete=false`。

该选项不放宽其他完整性检查：分片 0 或中间分片缺失、分片重复、混入其他场次、版本等头部信息不一致，或压缩数据损坏，都会导致导出停止。目录按片读取和解压，但轨迹仍需保存在内存中，实际内存占用取决于对局规模。详见[服务器回放](ACMI_SERVER_REPLAYS.md)。

## 常用设置

飞机遥测估算默认开启。若只希望查看记录的位置、姿态及其他导出内容，可关闭飞机估算字段：

```powershell
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi" -estimate-telemetry=false
```

诊断报告默认不生成。按需指定 CSV 路径，例如：

```powershell
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi" -list "tracks.csv" -radar-lock-log "locks.csv"
```

默认输出 ACMI 2.1；需要使用 2.2 时，可显式指定 `-acmi-version 2.2`。更改文件版本不代表已经验证了特定 Tacview 版本的界面显示效果。

## 命令参数

### 输入、输出与文件信息

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-in` | 无 | 客户端回放文件或服务器分片目录。 |
| `-out` | 按输入命名 | ACMI 输出路径。 |
| `-allow-incomplete` | `true` | 是否允许服务器回放缺少尾片。 |
| `-author` | `wrpl-acmi` | 写入文件的作者或操作员。 |
| `-acmi-version` | `2.1` | 可选 `2.1` 或 `2.2`。 |
| `-names` | 无 | 显示名称覆盖 JSON，支持 `air:`、`ground:`、`missile:`、`bomb:` 等键。 |

### 坐标与查找表

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-terrain-layout` | `geographic` | 使用地理定位及 Atlas 回退；也可选 `atlas`。 |
| `-map-id` | 自动识别 | 显式指定规范地图名、`WarThunder.Geo1.<name>` 或 `WarThunder.Atlas1.<name>`。 |
| `-alt` | `0` | 垂直高度偏移，单位为米，不改变水平位置。 |
| `-ecshashes` | `embedded` | ECS 组件名称表；可指定 JSON 路径覆盖，或传入空字符串关闭。 |
| `-missile-models` | `embedded` | 导弹型号补全表；可选 `off` 或 schema 1 JSON 路径。 |
| `-missile-guidance` | `embedded` | 静态制导信息表；可选 `off` 或 schema 1 JSON 路径。 |

### 导出内容开关

下列布尔选项中，`-bookmarks` 默认关闭，其余默认开启。关闭选项时使用 `-参数名=false`；需要高亮书签时使用 `-bookmarks=true`。

| 参数 | 说明 |
| --- | --- |
| `-countermeasures` | 导出飞机干扰物。 |
| `-radar-locks` | 导出雷达目标指定。 |
| `-radar-modes` | 导出支持的载机雷达模式及原生指向和标签，与目标指定独立。 |
| `-missile-seeker` | 导出支持的发射后导引头状态及原生锁定字段和标签。 |
| `-ground-units` | 启用批量地面位置同步导出路径。 |
| `-ground-models` | 根据明确证据识别地面型号，并添加型号派生信息及参考范围。 |
| `-airfields` | 导出源配置中的机场标记和跑道。 |
| `-estimate-telemetry` | 根据飞机位置、姿态估算航向、速度、Mach、IAS、AOA 和三轴 G。 |
| `-bookmarks` | 为支持的事件生成高亮时间轴书签，默认关闭。 |

`-events` 默认选择 `kill,chat,join,damage,radar`，分别对应击杀、聊天、玩家名单变化、伤害及雷达指定消息。可用 `none` 关闭，或指定逗号分隔的子集。奖励类别 `award` 默认关闭；`all` 包含全部类别。此开关与高亮书签独立，详见[事件消息时间线](acmi-information.zh-CN.md#事件消息时间线)。

### CSV 诊断报告

| 参数 | 内容 |
| --- | --- |
| `-list` | 轨迹清单及当前显示名称。 |
| `-missile-flight-log` | 导弹飞行参数和原始运动证据。 |
| `-countermeasure-log` | 干扰物构造记录。 |
| `-radar-lock-log` | 雷达目标指定状态变化。 |

每个参数独立接受输出路径；只有路径非空时才生成对应文件。

支持的布局、精确模式名称、型号关联及实测范围见[传感器状态与原生兼容性](ACMI_SENSOR_STATES.zh-CN.md)。

## 如何理解导出结果

### 飞机、文件信息与事件

飞机使用回放记录的位置和飞行模型姿态。估算功能额外写入 `HDG`、`TAS`、`Mach`、`IAS`、`AOA` 及三轴 G。这些值依据无风、标准大气等假设计算，其中 IAS 为 EAS 近似，三轴 G 为比力估算。无效或缺失的估算值会清空，避免沿用过期状态。

文件头记录标题、作者、UTC 导出时间、回放时间基准及来源备注。已确认的飞机击毁、武器发射或首次观测、干扰物释放和雷达指定变化可生成事件书签；同一时刻的重复书签会去重。字段含义和算法限制见[元数据与飞机遥测](acmi-information.zh-CN.md)。

### 地面单位与机场

地面单位优先使用同步轨迹。型号、阵营及销毁时刻只有在身份和事件证据明确时才会关联；缺少证据时保留通用身份。`-ground-models=false` 关闭型号及派生范围显示，但不关闭独立的阵营识别。

没有同步轨迹的建筑或工事，只有在静态类别、构造位置和时间戳均可靠时才会导出。身份冲突、UID 回收、位置变化、非有限坐标及任务占位物会被排除。相关规则见[地面单位](ACMI_GROUND_UNITS.md)。

机场目录覆盖 69 张地图、457 条任务记录，其中 67 张有可用源定义。无条件配置可直接选择；条件布局需要与回放中的机场雷达身份及位置匹配。源定义数量包含任务、等级和条件分支，不是同时存在的机场数量。跑道航向表示几何轴线，不代表当前降落方向。

PhiPhi 的 8 条竞速任务为空中出发，未提供陆地跑道；Caribbean 在已检查的来源范围内没有可靠跑道定义。具体规则和覆盖表见[机场与跑道](ACMI_AIRFIELDS.zh-CN.md)、[逐地图覆盖](ACMI_AIRFIELDS_COVERAGE.zh-CN.md)。

### 导弹、炸弹与干扰物

导弹轨迹来自同步位置，速度、加速度、转向率和 G 值由位置序列计算。型号候选、静态制导类型与 INS 时长另行保留；运动估算和型号资料的来源不能混为一谈。详见[导弹型号](ACMI_MODELS.md)和[制导信息](ACMI_GUIDANCE.md)。

炸弹存在 WeaponSync 时导出实际位置序列；只有构造记录时，仅标注 `Release position only`，不推算飞行轨迹或命中时刻。详见[炸弹导出](ACMI_BOMBS.md)。

干扰物只有在发射者弹药证据足以确定单一类型时，才被识别为 Flare 或 Chaff。混装或未知情况按 Chaff 显示，并注明 `fallback` 及 `UnknownAsChaff` 来源。有同步位置时使用实际轨迹，否则使用明确标记的短时估算轨迹。详见[干扰物类型](ACMI_COUNTERMEASURES.zh-CN.md)。

### 雷达指定与防空范围

雷达目标指定写入 Tacview 原生锁定属性；有效位置和姿态还可提供斜距、机体相对方位角及俯仰角。使用的位置不得晚于事件时刻，且最多允许一秒的时间差。解锁、目标销毁或几何无效时会清空相应字段。目标指定不能证明 STT 锁定、雷达开机或武器制导状态。详见[雷达目标指定](ACMI_RADAR_LOCKS.md)。

静态防空表包含 255 个型号。其中 142 个经来源确认无雷达、且没有有效导弹范围的纯炮或机枪配置不显示威胁圈；有雷达的炮车及具备有效导弹范围的系统保留参考范围。ZSU-37-2 使用明确标注的 2,000 米显示设置，并在来源字段保留上游 4,000 米原值。详见[防空静态范围](ACMI_AIR_DEFENCE_RANGES.zh-CN.md)。

## 地形与坐标配置

默认 geographic 布局在 69 张地图中采用 14 张已审核的 WGS84 估算位置，其余 55 张使用 Atlas 回退。回放对象与地形高程 XML 必须使用同一份坐标参考配置。已有 ACMI 需要匹配更新后的地形时，应从原始 WRPL 重新导出。

选择 `-terrain-layout atlas` 时，独立安装器应同时使用 `-atlas`。安装游戏地形只影响背景显示，不是导出对象的前提；源码仓库不包含地图高程资产。原理与安装方法见[地理坐标](TERRAIN_GEOGRAPHIC.md)、[Atlas 布局](terrain-atlas.md)及[地形工具](../tools/terrain/README.zh-CN.md)。

## 开发构建与验证

完整 Windows 发布包由根目录的 `build.ps1` 生成，详见[构建与发布指南](standalone-build.zh-CN.md)。仅开发无界面导出器时，可在安装 Go 1.25.6 或更高版本后，从仓库根目录运行：

```powershell
$env:CGO_ENABLED = "0"
go build -o .\wrpl-acmi.exe .\cmd\acmi
go test .\cmd\acmi .\wrpl\...
```

仓库工作流包含解析、合成地形及打包检查。真实地形转换和 Tacview 界面验证需要相应的本地环境，不能由上述测试结果替代。

## 常见问题

**找不到输出文件。** 未指定 `-out` 时，请检查当前工作目录；通过 `run.bat` 启动时，通常应检查发布包目录。

**输出名称带有 partial。** 服务器回放缺少结束部分。文件可用于查看已记录的片段，但不能据此判断整场最终结果。

**轨迹与地形错位。** 检查导出器和安装器是否使用同一布局及同一版参考配置。安装后重启 Tacview，并重新导出 WRPL。`-alt` 只能调整高度，不能修复水平错位。

**某个对象没有型号，或某项遥测为空。** 型号识别需要明确证据，估算还需要连续、有效的样本。空值表示当前信息不足或样本未通过校验，不应补作确定结论。
