# wrpl-acmi ACMI 导出指南

本页对应当前 `dev` 的 `cmd/acmi`。地形定位见 [地理地形](TERRAIN_GEOGRAPHIC.md)，技术布局见 [Atlas 布局](terrain-atlas.md)。

`cmd/acmi` 只转换回放，不提取或安装地形。仓库中的地形转换器读取本机游戏 `levels/*.bin` 的实际高度与几何，
再从固定提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457` 的 War-Thunder-Datamine 取得地图边界、水位等配置；
只有 Datamine 不能生成地形包。完整的从零构建、安装和导出命令见 [项目说明](../README.zh-CN.md)，
输入作用与许可见[地形工具说明](../tools/terrain/README.zh-CN.md#输入来源与许可)。

## 输入

- 客户端回放：单个 `.wrpl` 文件。
- 服务器回放：同一场的分片目录。目录按分片头排序，文件名不需要手工改序；不要混入其他场次。服务器回放的单片文件不能直接作为输入。
- 尾片缺失：默认 `-allow-incomplete=true`，导出已下载部分并写入不完整标记；设置 `-allow-incomplete=false` 可恢复严格拒绝。
- 中间分片缺失、分片 0 缺失、重复分片、场次混杂或压缩数据损坏会报错。输入文件不会被改写。

目录读取逐片打开和解压，不把所有原始分片同时装进内存；最终内存仍取决于整场轨迹规模。

## 命令与输出

```
.\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
.\wrpl-acmi.exe -out "C:\Replays\replay.acmi" "C:\Replays\replay.wrpl"
```

参数要放在位置参数之前，或统一使用 `-in`。不能同时提供 `-in` 和位置参数，也不能给多个位置参数。

未指定 `-out` 时，输出写到当前工作目录：普通文件使用 `<输入基本名>.acmi`；完整分片目录使用 `<目录名>.acmi`；尾片缺失的分片目录使用 `<目录名>.partial.acmi`。把单个文件或单个目录拖到 EXE 也遵循这条规则；一次只拖一个输入。runtime 包中的 `run.bat` 会先切换到包目录。

默认读取 `data/ecshashes.json`，也会尝试 EXE 同目录下的相对路径。该表缺失时导出器会降级，实体类名、型号、队伍、飞行员和导弹构造信息可能缺失。

## 主要参数

| 参数 | 含义 |
| --- | --- |
| `-in` | 输入 `.wrpl` 或服务器分片目录 |
| `-out` | 输出 `.acmi` 路径 |
| `-allow-incomplete` | 是否允许尾片缺失；默认 `true` |
| `-terrain-layout` | `geographic`（默认）或 `atlas`（当前技术布局） |
| `-map-id` | 无法自动识别地图时显式指定地图 ID |
| `-alt` | 垂直高度偏移，单位米 |
| `-missile-models` | `embedded`、`off` 或型号表 JSON 路径 |
| `-missile-guidance` | `embedded`、`off` 或制导表 JSON 路径 |
| `-countermeasures` | 是否导出干扰物；默认 `true` |
| `-radar-locks` | 是否写雷达目标指定；默认 `true` |
| `-ground-units` | 是否导出地面位置同步；默认 `true` |
| `-ground-models` | 是否按显式回放证据识别地面型号与派生范围；默认 `true` |
| `-list` | 可选，写出轨迹与当前名称的 CSV 清单 |
| `-names` | 可选，用 JSON 覆盖 `air:`、`ground:`、`missile:` 等显示名 |

水平定位由 `-terrain-layout` 选择；`-alt` 只调整垂直高度，不会改变地图的水平摆放。

## 导出内容

飞机使用回放位置和飞行模型姿态；地面单位使用精确位置轨迹，并按显式 UID、型号和队伍证据关联。可确认的击毁事件会结束对应对象，缺失证据时不猜测销毁时刻。

导弹轨迹保留实际位置。速度、加速度、转向率和 G 值来自位置序列的时间差分，不是游戏内部逐帧物理量。首帧可带型号候选和静态制导类型，INS 秒数显示为 `INS x.xx s`。

炸弹有真实同步时导出轨迹；只有构造记录时导出带 `Release position only` 的投放位置标记。干扰物优先使用真实同步位置，否则生成明确标记的估算短轨迹。雷达目标指定写入原生 `LockedTarget` 系列属性。

各部分的证据和开关说明：

- [服务器分片](ACMI_SERVER_REPLAYS.md)
- [地面单位](ACMI_GROUND_UNITS.md)
- [炸弹](ACMI_BOMBS.md)
- [雷达锁定](ACMI_RADAR_LOCKS.md)
- [防空静态范围](ACMI_AIR_DEFENCE_RANGES.md)
- [导弹型号](ACMI_MODELS.md) · [制导证据](ACMI_GUIDANCE.md)

## 地图与地形

默认 `-terrain-layout geographic`。权威地图目录恰好包含 69 张地图：14 张使用审核后的 WGS84 估算位置，55 张使用 Atlas 回退；高程 XML 与所有轨迹共用同一份 GeoReferences 变换。正常 geographic 导出依赖这整套 69 张地图的参考数据，必须与已安装地形和转换元数据使用同一版本。已知定位错误的 Sinai、Grand Canyon、Israel 不采用上游坐标。

若已有 ACMI 要与当前地形匹配，应从对应的原始 WRPL 直接重新导出。`-terrain-layout atlas` 与安装器的 `-AtlasOnly` 配对，属于当前的技术布局；`-alt` 可用于增加垂直高度偏移。详见 [地理地形](TERRAIN_GEOGRAPHIC.md) 与 [Atlas 布局](terrain-atlas.md)。

地图 `.data` 不纳入 Git，软件仓库不附带地形资产。`-WithTerrain` 构建会把 69 张地图放进 `dist/wrpl-acmi/terrain`；安装工具入口为 `dist/wrpl-acmi/tools/terrain/Install-All-Terrains.cmd`，目录与选项见 [地形工具说明](../tools/terrain/README.zh-CN.md)。

## 构建与检查

完整运行包（可带转换地形）从仓库根目录构建；详细依赖和全新克隆命令见[项目说明](../README.zh-CN.md)：

```powershell
.\build.ps1 -WithTerrain -GameRoot "C:\Path\To\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -Python ".\.venv\Scripts\python.exe"
```

只检查无界面导出器时，需要 Go 1.25.6 或更高版本（`go.mod` 当前声明 1.25.6），可用关闭 CGO 的单程序构建：

```
$env:CGO_ENABLED = "0"
go build -o .\wrpl-acmi.exe ./cmd/acmi
go test ./cmd/acmi ./wrpl/...
```

POSIX shell 等价命令为 `CGO_ENABLED=0 go build -o wrpl-acmi ./cmd/acmi`。历史发布物可能落后于当前 `dev`；需要最新行为时从当前分支构建。

## 证据边界

导弹运动学是同步位置的时间差分估计，未计入完整气动力、推力、质量、燃料或机体轴姿态。静态型号和制导类型不代表某一帧已锁定或进入末制导。无位置记录的干扰物与仅有构造记录的炸弹均已明确标注为估算或位置标记。防空范围是固定资料版本的静态参考，不是回放中的雷达状态或实时可击杀包线。

Tacview 中的实际渲染效果未作为本次验收项。
