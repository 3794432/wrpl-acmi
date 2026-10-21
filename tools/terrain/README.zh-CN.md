# 地形提取、转换与安装

[English](README.md) · [项目说明](../../README.zh-CN.md)

源码仓库包含转换器和安装器。转换器读取本机《战争雷霆》的编译后地形和固定版本 Datamine 的关卡配置，生成 69 张地图的安装包；安装器校验后将其复制到 Tacview。游戏地形资产不纳入 Git。

## 环境要求

- Windows 与 PowerShell。
- 构建 `wrpl-acmi` 需要 Go 1.25.6 或更高版本（`go.mod` 当前声明 Go 1.25.6）。
- 64 位 Python 3.12 及转换器所需的 pip 依赖。
- Git，以及包含地形高度和几何数据的本机《战争雷霆》客户端 `levels/*.bin`。
- 兼容提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457` 的 Datamine 克隆。
- 只有安装地形时才需要 Tacview。

## 从零构建

以下 PowerShell 命令获取当前 `dev` 分支、创建 Python 环境、安装转换器依赖、固定 Datamine 版本，并将回放导出器与全部 69 张转换地图构建到 `dist/wrpl-acmi`：

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

稀疏检出只选取 `aces.vromfs.bin_u/levels`；转换器需要固定版本中、由 `AtlasReferences.json` 列出的 69 个目标 `.blkx`，不需要完整 Datamine 克隆。`requirements.txt` 固定 NumPy 2.3.5、Pillow 12.3.0、pyooz 0.0.8 与 zstandard 0.25.0。

`build.ps1` 接受 `-OutputRoot`（默认 `dist\wrpl-acmi`）、`-WithTerrain`、`-ResumeTerrain`、`-GameRoot`、`-DatamineRoot` 和 `-Python`（默认 `python`）。指定 `-WithTerrain` 时，脚本会调用 `tools/terrain/build-terrain.ps1`，并把转换后的地图放入 `dist/wrpl-acmi/terrain`。构建过程不会向 Tacview 安装任何内容。如果虚拟环境解释器不是 `PATH` 中的默认 `python`，请通过 `-Python` 明确指定。

转换中断后，用同样参数加上 `-ResumeTerrain` 继续；该开关必须与 `-WithTerrain` 同用。没有它时，重复执行地形构建会拒绝已有的非空输出目录。

安装依赖后，也可以只构建地形包，直接调用转换器包装脚本：

```powershell
.\tools\terrain\build-terrain.ps1 -GameRoot "C:\Path\To\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -OutputRoot ".\converted-terrain" -Python ".\.venv\Scripts\python.exe"
```

`-ValidateOnly` 只检查全部输入，不写输出。中断后使用同一输出目录并加上 `-Resume`。`-Resolution` 默认每张地图 4096 像素，`-TimeoutSeconds` 默认每张地图 1200 秒。

构建产物结构如下：

```text
dist/wrpl-acmi/
  wrpl-acmi.exe
  run.bat
  data/
  tools/terrain/                 # 转换器、安装器、元数据与许可声明
  terrain/                       # -WithTerrain 成功转换后存在
    GeoReferences.json
    maps/<id>/<id>.data
    maps/<id>/metadata.json
    maps/<id>/CustomHeightmapList.xml
```

完整安装包包含 69 个地图目录。`.data` 为米制、16 位有符号小端高程，按从北到南、从西到东排列；`metadata.json` 记录尺寸、采样边界及 SHA-256。源 XML 使用安装器要求的零点模板。`smalltests` 中的合成数据只用于安装测试。

## 输入来源与许可

| 输入 | 作用 | 来源与许可 |
| --- | --- | --- |
| 客户端编译后地形 | 实际高度与几何，包括 `levels/<id>.bin` | 用户本机的《战争雷霆》安装。游戏资产归 Gaijin Entertainment 所有，本仓库不分发。 |
| Datamine 关卡配置 | 地图边界、水位及相关 `.blkx` 设置 | 提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457` 的 [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine)；该提交未发现许可文件，按上游仓库数据使用，仅作为本地转换输入，不随本仓库分发。 |
| 地理参考 | `GeoReferences.json` 中审核后的 WGS84 估算位置 | 提交 `c26c0069558079df6597a7bc147cb91391509f89` 的 [ShioUmiusi/WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE)，MIT 许可。声明见 `georeference/UPSTREAM-NOTICE.md` 与 `georeference/UPSTREAM-MIT.txt`。 |
| Python 模块 | 解码与栅格处理 | NumPy 2.3.5 为 BSD-3-Clause；pyooz 0.0.8 为 GPL-3.0-or-later；zstandard 0.25.0 为 BSD-3-Clause；Pillow 12.3.0 为 HPND。 |

本项目代码按 AGPL-3.0 发布；各依赖和上游来源保留自己的许可。Datamine 只提供配置，实际地形高度必须来自本机游戏编译后的关卡文件。仅把 `.bin` 或 `.blkx` 改名为 `.data` 不能完成转换。

pyooz 附带的 GPL-3.0-or-later `ooz` 解码器负责处理 Oodle 压缩的地形流。全新的 pip 环境不需要专有 Oodle DLL 或密钥。

## 安装转换后的地形

在 `-WithTerrain` 构建成功后，从仓库根目录执行：

```powershell
.\dist\wrpl-acmi\tools\terrain\Install-All-Terrains.cmd -SourceRoot .\dist\wrpl-acmi\terrain -AtlasReferences .\dist\wrpl-acmi\tools\terrain\AtlasReferences.json
```

安装器校验所有地图的元数据与哈希，重写 XML 摆放并复用栅格数据。默认目标为 Tacview 的 `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`；可用 `-Destination` 指定其他位置。权威目录恰好包含 69 张地图，其中 14 张使用审核后的地理估算位置，55 张使用 Atlas 回退。正常的 geographic 导出与安装需要这整套参考数据，转换地形、导出器配置和安装器元数据必须保持同步。

需要显式技术坐标时，将安装器的 `-AtlasOnly` 与导出器的 `-terrain-layout atlas` 配对使用。导出器的 `-alt` 仍是以米为单位的垂直偏移。完整说明见 [地理地形](../../docs/TERRAIN_GEOGRAPHIC.md)、[Atlas 布局](../../docs/terrain-atlas.md) 和 [覆盖清单](georeference/coverage.json)。

安装完成后完全退出并重启 Tacview，再用当前导出器重新导出 WRPL。若已有 ACMI 要与当前地形匹配，请以对应的原始 WRPL 直接重新导出。

## 导出

从仓库根目录执行：

```powershell
.\dist\wrpl-acmi\run.bat -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\dist\wrpl-acmi\run.bat -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

`run.bat` 会把工作目录保留在包内，从而找到 `data/ecshashes.json` 和随包默认文件。导出器读取客户端 `.wrpl` 或同一场服务器回放的分片目录，写出 Tacview ACMI 2.1；它本身不提取或安装地形。默认使用 `-terrain-layout geographic`（14 张地理估算、55 张 Atlas 回退）；与安装器 `-AtlasOnly` 配套时可显式使用 `-terrain-layout atlas`。需要垂直高度偏移时仍可使用 `-alt`。

## 安装器检查

下列测试使用合成夹具，不读取真实游戏资产：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\test-install-all-terrains.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\geographic\New-Fixture.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\geographic\test-install-geographic-terrains.ps1
```
