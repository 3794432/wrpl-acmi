# wrpl-acmi

[English](README.en.md) · [dev 分支](https://github.com/3794432/wrpl-acmi/tree/dev) · [上游项目](https://github.com/maxsupermanhd/wrpl-inspector)

把《战争雷霆》回放导出为 Tacview ACMI。wrpl-acmi 继承 upstream wrpl-inspector v3 的 Go 回放库与 dear-imgui 检查器，并加入无界面导出器 `cmd/acmi`。它读取客户端 `.wrpl` 或同一场服务器回放的分片目录，生成 Tacview ACMI 2.1。仓库发布在 `3794432/wrpl-acmi` 的 `dev` 分支；Go module 路径仍为 `github.com/maxsupermanhd/wrpl-inspector/v3`。

## 导出内容

- 飞机与地面单位使用回放中的位置；飞机带飞行模型姿态，地面单位带精确轨迹、型号、队伍和可确认的销毁记录。
 - 导弹使用实际同步位置，并附带由位置序列估计的速度、加速度、转向和 G 值；型号候选、静态制导类型与 INS 秒数保留在 ACMI 字段和 `INS x.xx s` 标签中。
- 炸弹有 WeaponSync 时导出真实位置序列；只有构造记录时明确标为 `Release position only`，不补造飞行轨迹。
- 干扰措施有真实位置时直接导出；否则从释放点生成明确标记的短时估算轨迹。
- 雷达目标指定写成 Tacview 原生 `LockedTarget` 属性；重新导出并开启 Display Radar Locks 可查看。
- 防空范围表覆盖 255 个型号。经固定版本资料确认无雷达的纯炮/机枪配置不画威胁圈（审计清单中的 142 个在列）；ZA-35、ZSU-23-4、ZSU-37-2 以及红外或混合制导 SAM 保留。范围是静态型号参考，不代表回放中的雷达开关或实时包线。

在项目核对过的回放中，69 架飞机和 140 个地面单位的型号与队伍均已关联；可确认的销毁记录按证据保留。其他版本、地图和服务器分片的覆盖情况仍需逐场核对。

## 快速开始

```
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi"
.\wrpl-acmi.exe -in ".\server-replays\session-id"
```

Go 的 `flag` 解析在首个位置参数处停止，因此参数要放在文件前；也可以只给一个位置参数：

```
.\wrpl-acmi.exe -out "replay.acmi" "replay.wrpl"
```

不要同时使用 `-in` 和位置参数。服务器回放要把同一场的分片放在一个目录中读取；单个分片直接作为输入会被拒绝。

拖放 `.wrpl` 或分片目录到 EXE 时，未指定 `-out` 会在当前工作目录写出 `<输入基本名>.acmi`。分片目录的尾片缺失时输出 `<目录名>.partial.acmi`，并在 ACMI 中标记不完整；中间分片缺失、重复、混入其他场次或压缩损坏会报错。包含 `run.bat` 的 runtime 包会先把工作目录切到包目录。

默认读取 `data/ecshashes.json`。缺少该表时程序会降级，型号、队伍、飞行员关联和导弹构造信息可能缺失。

## 从当前 dev 构建

`go.mod` 要求 Go 1.25.6。无界面导出器不需要 GUI 依赖，关闭 CGO 构建：

```
$env:CGO_ENABLED = "0"
go build -o .\wrpl-acmi.exe ./cmd/acmi
go test ./cmd/acmi ./wrpl/...
```

POSIX shell 中等价于 `CGO_ENABLED=0 go build -o wrpl-acmi ./cmd/acmi`。历史 release 可能落后于当前 `dev`；需要最新行为时请从当前分支构建。

## 地图与地形

默认 `-terrain-layout geographic`。地图目录共 69 张，其中 14 张使用审核过的 WGS84 估算位置（5 张高置信、9 张中置信），55 张自动回退 Atlas 技术坐标。Sinai、Grand Canyon、Israel 的已知错误定位不会被采用。

地形高程 XML 与飞机、导弹、炸弹、地面单位和干扰物共用同一份 GeoReferences 变换。当前版本不保证旧 ACMI 的坐标兼容；已有 ACMI 应从 WRPL 重新导出。`-terrain-layout atlas` 是显式技术摆放模式，`-terrain-layout legacy` 才允许手工 `-lat`/`-lon`/`-center-x`/`-center-z`。

地形安装命令（转换产物需带 GeoReferences.json，可从 tools/terrain 复制）：

```
.\tools\terrain\Install-All-Terrains.cmd -SourceRoot "<地形转换产物目录>" -AtlasReferences .\tools\terrain\AtlasReferences.json
```

Atlas 配套安装使用同一工具的 `-AtlasOnly`。地图 `.data` 不纳入 Git，仓库不附带地形资产；安装和目录要求见 [地形工具说明](tools/terrain/README.md)。

## 文档

- [当前导出指南](docs/acmi-export.md)
- [服务器分片](docs/ACMI_SERVER_REPLAYS.md) · [地面单位](docs/ACMI_GROUND_UNITS.md) · [炸弹](docs/ACMI_BOMBS.md) · [雷达锁定](docs/ACMI_RADAR_LOCKS.md)
- [防空静态范围](docs/ACMI_AIR_DEFENCE_RANGES.md) · [导弹型号](docs/ACMI_MODELS.md) · [制导证据](docs/ACMI_GUIDANCE.md)
- [地理地形](docs/TERRAIN_GEOGRAPHIC.md) · [Atlas 与 legacy](docs/terrain-atlas.md) · [上游检查器说明](docs/README.md)

## 解释边界

导弹速度、加速度与 G 值来自同步位置的时间差分；无同步的干扰物轨迹是显式估算；只有构造记录的炸弹只是投放位置标记。静态制导类型和防空范围不表示某一帧已经锁定、雷达已经开机或处于可击杀状态。Tacview 中的实际渲染效果未作为本次验收项。

本项目及 `wrpl` 库按 GNU Affero General Public License v3 发布，详见 [LICENSE](LICENSE)。上游项目与版权声明予以保留。地图和游戏资源归 Gaijin Entertainment 所有；地理定位来自 WT-MAP-ANALYZE 固定 commit 的估算，存在不确定性。本项目与 Gaijin 无隶属或背书关系。
