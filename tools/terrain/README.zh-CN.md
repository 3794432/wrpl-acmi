# 地形提取、转换与安装

[English](README.md) · [项目说明](../../README.zh-CN.md)

源码仓库包含转换器和安装器代码。转换器读取本机《战争雷霆》的编译后地形和固定版本 Datamine 的关卡配置，生成 69 张地图的转换目录；`build.ps1` 把该目录作为 payload 附加到单文件安装器。游戏地形资产不纳入 Git。

## 环境要求

- Windows PowerShell，以及构建 EXE 所需的 Go 1.25.6 或更高版本（`go.mod` 当前声明 Go 1.25.6）。
- 从零转换地形还需要 64 位 Python 3.12、转换器的 pip 依赖、Git、包含编译后 `levels/*.bin` 的本机《战争雷霆》，以及提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457` 的 Datamine 克隆。
- 使用 `-TerrainSource` 打包已有转换目录时，构建机不需要 Python、Git、游戏或 Tacview。
- 安装时需要 Tacview 和系统 Windows PowerShell（`powershell.exe`）。完整离线安装器要为临时展开和安装预留数 GB 空间，但不需要游戏、转换脚本、Python、Go 或 Git。

## 从零构建

以下 PowerShell 命令获取当前 `dev` 分支、创建 Python 环境、安装转换器依赖、固定 Datamine 版本、转换全部 69 张地图，并构建两个 EXE，把地形 payload 内嵌到安装器：

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

`build.ps1` 接受 `-OutputRoot`（默认 `dist\wrpl-acmi`）、`-TerrainSource`、`-WithTerrain`、`-ResumeTerrain`、`-GameRoot`、`-DatamineRoot` 和 `-Python`（默认 `python`）。每次构建都生成 `wrpl-acmi.exe` 与 `wrpl-terrain-installer.exe`。指定 `-WithTerrain` 时，脚本调用 `tools/terrain/build-terrain.ps1`，把转换地图暂存到 `dist/wrpl-acmi/terrain`，再附加到安装器。如果虚拟环境解释器不是 `PATH` 中的默认 `python`，请通过 `-Python` 明确指定。

如果目录中已有 69 张地图的转换结果：

```powershell
.\build.ps1 -TerrainSource ".\converted-terrain"
```

`-TerrainSource` 与 `-WithTerrain` 不能同时使用。普通的 `build.ps1` 调用会生成只有安装逻辑、没有地形 payload 的安装器，不能视为完整离线地形安装器。构建过程不会向 Tacview 安装任何内容；打包也不改变转换器的输出规则，未指定转换器 `-out` 时仍写到当前工作目录。

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
  wrpl-terrain-installer.exe        # 使用 -TerrainSource 或 -WithTerrain 时内含 payload
  build-manifest.json
  run.bat
  data/                            # 辅助副本；导出器默认数据已内嵌
  docs/
  tools/terrain/                 # 转换器、安装器、元数据与许可声明
  terrain/                       # -WithTerrain 构建时的暂存源
    GeoReferences.json
    maps/<id>/<id>.data
    maps/<id>/metadata.json
    maps/<id>/CustomHeightmapList.xml
```

完整地形来源包含 69 个地图目录。`.data` 为米制、16 位有符号小端高程，按从北到南、从西到东排列；`metadata.json` 记录尺寸、采样边界及 SHA-256。源 XML 使用安装器要求的零点模板。`smalltests` 中的合成数据只用于安装测试。独立 EXE 内含附加 payload，不依赖相邻的 `data/`、`terrain/` 或 `tools/` 目录。发布细节见[单文件发布构建](../../docs/standalone-build.zh-CN.md)。

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

发布时优先使用包根目录中的独立安装器：

```powershell
.\dist\wrpl-acmi\wrpl-terrain-installer.exe
```

安装器没有内嵌 payload，或需要安装另一个转换目录时，传入 `-source`：

```powershell
.\dist\wrpl-acmi\wrpl-terrain-installer.exe -source "C:\Terrain\converted-terrain"
```

含 payload 的安装器可省略 `-source`；只有逻辑的构建则必须提供。默认目标是 `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`，可用 `-destination <目录>` 指定其他位置。安装器在写入目标前校验来源元数据和 SHA-256；`-validate-only` 只验证 source，不写目标。

`-profile` 选择高度编码：默认 `auto` 检测已安装的 Tacview 版本，找不到 Tacview 时回退 `1.9.4`；也可显式使用 `1.9.4` 或 `1.9.5`。重新运行安装器可复用 cache，但临时展开数据加目标地图可能占用数 GB 可用空间。

权威目录恰好包含 69 张地图，其中 14 张使用审核后的地理估算位置，55 张使用 Atlas 回退。正常的 geographic 导出与安装需要这整套参考数据，转换地形、导出器配置和安装器元数据必须保持同步。

需要显式技术坐标时，将安装器的 `-atlas` 与导出器的 `-terrain-layout atlas` 配对使用；两边默认都是 geographic，坐标布局必须来自同一次转换设置。导出器的 `-alt` 仍是以米为单位的垂直偏移。完整说明见 [地理地形](../../docs/TERRAIN_GEOGRAPHIC.md)、[Atlas 布局](../../docs/terrain-atlas.md) 和 [覆盖清单](georeference/coverage.json)。

安装完成后完全退出并重启 Tacview，再用当前导出器重新导出 WRPL。若已有 ACMI 要与当前地形匹配，请以对应的原始 WRPL 直接重新导出。

### 地形高度编码

独立安装器按 `-profile auto`、`1.9.4` 或 `1.9.5` 写入匹配的高度编码。已有安装需要检查或手动切换时，仍可使用源码树中的格式工具：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\dist\wrpl-acmi\tools\terrain\Set-Tacview-Terrain-Format.ps1 -Profile 1.9.4
# 恢复原生有符号高程布局：
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\dist\wrpl-acmi\tools\terrain\Set-Tacview-Terrain-Format.ps1 -Profile 1.9.5
```

`-TerrainRoot` 默认为 `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`；自定义安装位置时需显式传入。1.9.4 配置创建根目录高程索引与无符号 16 位高程副本，配合 -32768 米高度偏移，原始有符号栅格保持不变。1.9.5 配置恢复逐地图的原生有符号高程定义。两种配置使用相同的地图位置与 ACMI 坐标。格式工具的 `-ValidateOnly` 检查已安装配置；安装器的 `-validate-only` 只检查 source，不写目标。

切换已安装格式后需重启 Tacview。合成格式与往返转换检查覆盖生成文件，不代表两个 Tacview 版本已完成 GUI 渲染验证。

## 导出

导出器是自包含 EXE，可以从包目录直接运行，也可以复制到其他位置：

```powershell
.\dist\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\dist\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

导出器读取客户端 `.wrpl` 或同一场服务器回放的分片目录；正常查找数据（包括 `ecshashes`）已编译在内，因此导出不需要 `run.bat`、`data/` 目录或已安装地形，Tacview 只用于查看 ACMI。默认使用 `-terrain-layout geographic`（14 张地理估算、55 张 Atlas 回退）；与安装器 `-atlas` 配套时可显式使用 `-terrain-layout atlas`。需要垂直高度偏移时仍可使用 `-alt`。

## 安装器检查

下列源码树检查使用合成夹具，不读取真实游戏资产：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\test-install-all-terrains.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\geographic\New-Fixture.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\geographic\test-install-geographic-terrains.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\test-terrain-194-encoding.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\test-terrain-format.ps1
```

这些检查的存在不表示当前工作树已运行并通过全部测试或构建。
