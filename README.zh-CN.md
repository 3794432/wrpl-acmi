# wrpl-acmi

[English](README.md) · [单文件发布构建](docs/standalone-build.zh-CN.md) · [dev 分支](https://github.com/3794432/wrpl-acmi/tree/dev) · [上游项目](https://github.com/maxsupermanhd/wrpl-inspector)

把《战争雷霆》回放导出为 Tacview ACMI。wrpl-acmi 继承 upstream wrpl-inspector v3 的 Go 回放库与 dear-imgui 检查器，并加入无界面导出器 `cmd/acmi`。它读取客户端 `.wrpl` 或同一场服务器回放的分片目录，生成 Tacview ACMI 2.1。仓库发布在 `3794432/wrpl-acmi` 的 `dev` 分支；Go module 路径仍为 `github.com/maxsupermanhd/wrpl-inspector/v3`。

## 导出内容

- 按地图和任务选择机场跑道，默认 `-airfields=true`。目录包含 69 张地图、457 条任务记录，其中 67 张地图有可用源定义；条件布局需要匹配的回放雷达身份。PhiPhi 的 8 条竞速任务为空中出发且没有陆地跑道来源；Caribbean 没有可靠的任务或跑道来源。雷达名称保留游戏缩写 `AFF Radar` 和 `AFM Radar`（机场飞行雷达与主雷达），完整型号保存在 `WT_GroundModel`。详见[机场定位](docs/ACMI_AIRFIELDS.zh-CN.md)和[逐地图覆盖](docs/ACMI_AIRFIELDS_COVERAGE.zh-CN.md)。
- 文件元数据（`Title`、`Author`、`RecordingTime`、类别与来源备注）、事件时间轴书签、阵营分组和机体相对的雷达指定角度。飞机 `HDG`、`TAS`、`Mach`、`IAS`、`AOA` 与三轴 G 估算默认开启；设置 `-estimate-telemetry=false` 可关闭，详见[字段与估算边界](docs/acmi-information.zh-CN.md)。
- 飞机与地面单位使用回放中的位置；飞机带飞行模型姿态，地面单位带精确轨迹、型号、队伍和可确认的销毁记录。没有同步轨迹的静态建筑或工事可使用带时间戳的真实 ECS 构造位置，并通过 `WT_GroundStatic` 来源字段区分。
 - 导弹使用实际同步位置，并附带由位置序列估计的速度、加速度、转向和 G 值；型号候选、静态制导类型与 INS 秒数保留在 ACMI 字段和 `INS x.xx s` 标签中。
- 炸弹有 WeaponSync 时导出真实位置序列；只有构造记录时明确标为 `Release position only`，不补造飞行轨迹。
- 干扰物按可靠弹药证据区分 Flare/Chaff；无法判断的统一按 Chaff 显示并注明默认归类，不推测弹种。有真实位置时直接导出，否则保留明确标记的短时估算轨迹，详见[干扰物类型](docs/ACMI_COUNTERMEASURES.zh-CN.md)。
- 雷达目标指定写成 Tacview 原生 `LockedTarget` 属性；在回放位置支持时附带几何斜距和机体相对的方位角、俯仰角。重新导出并开启 Display Radar Locks 可查看。这不证明 STT 硬锁、雷达开机状态或武器制导模式。
- 轨迹清单、导弹飞行证据、干扰物构造和雷达锁定变化四类 CSV 默认不输出；只有分别通过 `-list`、`-missile-flight-log`、`-countermeasure-log`、`-radar-lock-log` 显式提供输出路径时才写出对应文件。
- 防空范围表覆盖 255 个型号。经来源确认无雷达的纯炮/机枪配置不画威胁圈（共 142 个）；ZA-35、ZSU-23-4、ZSU-37-2 以及红外或混合制导 SAM 保留。ZSU-37-2 使用用户确认的 2,000 米显示半径；来源字段同时记录固定 Datamine 中的 `attackMaxDist` 原值为 4,000 米。范围是静态型号参考，不代表回放中的雷达开关或实时包线。
- ACMI 2.1 仍是兼容性默认值；`-acmi-version 2.2` 为显式选用。

在项目核对过的回放中，69 架飞机和 140 个地面单位的型号与队伍均已关联；可确认的销毁记录按证据保留。其他版本、地图和服务器分片的覆盖情况仍需逐场核对。

## 快速开始

```
.\dist\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\dist\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

`wrpl-acmi.exe` 是单文件程序，可以复制到任意目录运行；正常导出所需的查找表（包括 `ecshashes`）均编译在 EXE 内。导出不需要 `data/` 目录、已安装地形、本机《战争雷霆》、Python、Go、Git 或 Tacview；Tacview 只用于查看结果。包内的 `run.bat` 仍可作为可选包装，它会先切换到包目录再调用 EXE。

Go 的 `flag` 解析在首个位置参数处停止，因此参数要放在文件前；也可以只给一个位置参数：

```
.\wrpl-acmi.exe -out "C:\Replays\replay.acmi" "C:\Replays\replay.wrpl"
```

不要同时使用 `-in` 和位置参数。服务器回放要把同一场的分片放在一个目录中读取；单个分片直接作为输入会被拒绝。

拖放 `.wrpl` 或分片目录到 EXE 时，未指定 `-out` 仍在当前工作目录写出 `<输入基本名>.acmi`；单文件构建不改变这条规则。分片目录的尾片缺失时输出 `<目录名>.partial.acmi`，并在 ACMI 中标记不完整；中间分片缺失、重复、混入其他场次或压缩损坏会报错。

`-ecshashes` 默认为 `embedded`，无需外部数据文件即可识别 ECS 组件名称及相关回放元数据。可用显式 JSON 路径覆盖内嵌表，或设置 `-ecshashes ""` 关闭。两个可执行文件、地形打包与安装参数见[单文件发布说明](docs/standalone-build.zh-CN.md)。

## 从当前 dev 构建

导出器要求 Go 1.25.6 或更高版本（`go.mod` 当前声明 Go 1.25.6）。`build.ps1` 总是同时构建 `wrpl-acmi.exe` 和 `wrpl-terrain-installer.exe`。没有地形输入时，地形安装器只有安装逻辑，不能称为完整离线地形安装器。从零转换地形还需要 Git、64 位 Python 3.12，以及本机《战争雷霆》客户端文件。

在全新的 PowerShell 会话中，以下命令获取当前 `dev` 分支、准备转换环境、转换地图、构建两个 EXE，并把地形 payload 内嵌到安装器：

```powershell
git clone --branch dev --single-branch https://github.com/3794432/wrpl-acmi.git
Set-Location .\wrpl-acmi
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install --upgrade pip
.\.venv\Scripts\python.exe -m pip install -r .\tools\terrain\converter\requirements.txt
git clone --depth 1 --filter=blob:none --no-checkout https://github.com/gszabi99/War-Thunder-Datamine.git .\War-Thunder-Datamine
git -C .\War-Thunder-Datamine fetch --depth 1 origin f7952e4291e9fd7b6fe38e84020dddef925c3457
git -C .\War-Thunder-Datamine sparse-checkout init --cone
git -C .\War-Thunder-Datamine sparse-checkout set aces.vromfs.bin_u/levels
git -C .\War-Thunder-Datamine checkout --detach FETCH_HEAD
.\build.ps1 -WithTerrain -GameRoot "C:\Path\To\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -Python ".\.venv\Scripts\python.exe"
```

稀疏检出只选取 `aces.vromfs.bin_u/levels`，不需要完整克隆 Datamine。转换器会校验 `AtlasReferences.json` 中列出的 69 个目标配置。`tools/terrain/converter/requirements.txt` 固定 NumPy 2.3.5、Pillow 12.3.0、pyooz 0.0.8 与 zstandard 0.25.0。

`-Python` 请使用实际的 64 位 Python 3.12 解释器；默认值为 `python`。`-OutputRoot` 默认为 `dist\wrpl-acmi`。省略 `-WithTerrain`、`-GameRoot` 和 `-DatamineRoot` 时，仍会生成含安装逻辑但无地形 payload 的安装器。构建过程不会自动向 Tacview 安装地形。地形转换中断后，可用同样参数加上 `-ResumeTerrain` 继续；再次使用 `-WithTerrain` 时，若已有非空 `terrain` 目录且没有该开关，构建会拒绝覆盖。

如果 69 张地图已经转换完成，可在构建机上没有 Python、Git、游戏或 Tacview 的情况下直接打包：

```powershell
.\build.ps1 -TerrainSource ".\converted-terrain"
```

`-TerrainSource` 与 `-WithTerrain` 不能同时使用。转换器未指定 `-out` 时仍默认写到当前工作目录；打包已有目录不会重写地图，也不改变拖放输出的命名规则。

运行包包含两个 EXE，以及 `run.bat`、`README.md`、`README.zh-CN.md`、`LICENSE`、复制进包的 `docs/`、`data/` 和 `tools/terrain`、`tools/airfields`、`tools/countermeasures` 目录。这些辅助目录供项目与工具使用，`wrpl-acmi.exe` 不依赖它们。`-WithTerrain` 成功后，`terrain/` 保存 69 张地图的转换源；打包时会把这些地形数据附加到安装器，而不是提交到 Git。`build-manifest.json` 记录产物哈希及安装器是否包含数据。可执行文件以关闭 CGO 的方式构建。历史 release 可能落后于当前 `dev`；需要最新行为时请从当前分支构建。

[源码构建工作流](https://github.com/3794432/wrpl-acmi/blob/dev/.github/workflows/source-build.yml)配置了导出器与库检查、合成地形解码、机场地形采样、安装器检查、地形格式转换和包构建。真实地形转换依赖本机游戏文件，因此不在 CI 中执行。本页不声称已有 CI 运行成功；CI 也不验证 Tacview GUI 加载或渲染。

## 地图与地形

默认 `-terrain-layout geographic`。权威地图目录恰好包含 69 张地图：14 张使用审核过的 WGS84 估算位置（5 张高置信、9 张中置信），55 张使用 Atlas 回退。转换地形、导出器配置与安装器元数据必须共用这整套 69 张地图的参考集。Sinai、Grand Canyon、Israel 的已知错误定位不会被采用。

地形高程 XML 与飞机、导弹、炸弹、地面单位和干扰物共用同一份 GeoReferences 变换。若已有 ACMI 要与当前地形匹配，应从原始 WRPL 直接重新导出。`-terrain-layout atlas` 是当前的技术布局选项；`-alt` 仍可作为以米为单位的垂直偏移。

使用与导出器一同构建的独立安装器安装地形：

```powershell
.\dist\wrpl-acmi\wrpl-terrain-installer.exe
```

默认目标是 `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`。可用 `-source <converted-terrain>` 安装外部转换目录，用 `-validate-only` 仅验证 source 而不写目标；高度编码由 `-profile auto`（默认）、`1.9.4` 或 `1.9.5` 选择，自动检测找不到 Tacview 时回退到 1.9.4。安装器的 `-atlas` 只能与导出器 `wrpl-acmi.exe -terrain-layout atlas` 配套；默认仍是 geographic。安装器依赖系统 Windows PowerShell，并为临时展开和安装预留数 GB 磁盘空间。地图 `.data` 不纳入 Git。详见[单文件发布说明](docs/standalone-build.zh-CN.md)和[地形工具说明](tools/terrain/README.zh-CN.md)。

高程和地形几何来自本机已安装游戏的 `levels/*.bin`。[War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) 提供地图边界、水位等关卡配置；只有 Datamine 并不能得到编译后的高度。转换流程固定在 Datamine 提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457`。经纬度定位是另一项来自 WT-MAP-ANALYZE 的 MIT 许可估算。完整工具链、各项输入的作用、目录结构和许可见[地形工具说明](tools/terrain/README.zh-CN.md#输入来源与许可)。

## 文档

- [导出指南与命令参数](docs/acmi-export.zh-CN.md)
- [单文件发布与地形安装器](docs/standalone-build.zh-CN.md)
- [服务器分片](docs/ACMI_SERVER_REPLAYS.md) · [地面单位](docs/ACMI_GROUND_UNITS.md) · [机场](docs/ACMI_AIRFIELDS.zh-CN.md) · [机场逐图覆盖](docs/ACMI_AIRFIELDS_COVERAGE.zh-CN.md)
- [元数据、书签与飞机估算](docs/acmi-information.zh-CN.md) · [干扰物类型](docs/ACMI_COUNTERMEASURES.zh-CN.md) · [炸弹](docs/ACMI_BOMBS.md) · [雷达锁定](docs/ACMI_RADAR_LOCKS.md)
- [防空静态范围](docs/ACMI_AIR_DEFENCE_RANGES.zh-CN.md) · [导弹型号](docs/ACMI_MODELS.md) · [制导证据](docs/ACMI_GUIDANCE.md)
- [地理地形](docs/TERRAIN_GEOGRAPHIC.md) · [Atlas 布局](docs/terrain-atlas.md) · [上游检查器说明](docs/README.md)

## 解释边界

导弹速度、加速度与 G 值来自同步位置的时间差分。飞机遥测由记录的位置和姿态在明确的无风、标准大气假设下估算；IAS 是 EAS 近似，三轴 G 是比力估算。无同步的干扰物轨迹是显式估算，无法判断弹种时按 Chaff 显示且不声称已识别；只有构造记录的炸弹只是投放位置标记。静态制导类型和防空范围不表示某一帧已经锁定、雷达已经开机或处于可击杀状态。跑道标记保留源几何和覆盖边界，不证明某一局中跑道实际处于活动状态。

目录声明为估算的位置仍保留相应不确定性；Tacview GUI 加载与实际渲染不在本文说明的验证范围内。

本项目及 `wrpl` 库按 GNU Affero General Public License v3 发布，详见 [LICENSE](LICENSE)。上游项目与版权声明予以保留。地图和游戏资源归 Gaijin Entertainment 所有；地理定位来自 WT-MAP-ANALYZE 固定 commit 的估算，存在不确定性。本项目与 Gaijin 无隶属或背书关系。
