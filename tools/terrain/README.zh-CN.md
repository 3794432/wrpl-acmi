# 地形提取、转换与安装

[English](README.md) · [项目说明](../../README.zh-CN.md) · [构建与发布](../../docs/standalone-build.zh-CN.md)

地形工具将本机《战争雷霆》的编译后关卡转换为 Tacview 高程数据，再通过独立安装器写入 Tacview 的自定义地形目录。完整转换集包含 69 张地图；游戏地形资产不提交到 Git。

如果已经取得含地形数据的安装器，可直接阅读[安装转换后的地形](#安装转换后的地形)。只有需要自行提取或打包地形时，才需要准备下述构建环境。

## 环境要求

| 操作 | 所需环境 |
| --- | --- |
| 从零转换并构建 | Windows PowerShell、Go 1.25.6 或更高版本、64 位 Python 3.12、转换器依赖、Git、本机游戏文件及固定版本 Datamine。 |
| 打包已有转换目录 | Windows PowerShell、Go 及项目源码；不需要 Python、Git、游戏或 Tacview。 |
| 安装地形 | 系统 Windows PowerShell 及内嵌或外部地形数据。查看结果需要 Tacview，安装过程不需要游戏、Python、Go 或 Git。 |
| 导出回放 | 独立的 `wrpl-acmi.exe`；无需先转换或安装地形。 |

默认分辨率的完整地图集建议预留至少 8 GB 磁盘空间，用于安装时的临时展开、原始栅格及兼容高程副本。

## 从零构建

以下命令从 dev 分支获取源码，准备转换环境，再构建含地形数据的安装器。运行前，请确认 `python` 对应 64 位 Python 3.12，并将 `-GameRoot` 改为本机游戏目录。

### 获取源码并准备 Python

```powershell
git clone --branch dev --single-branch https://github.com/3794432/wrpl-acmi.git
Set-Location .\wrpl-acmi
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install --upgrade pip
.\.venv\Scripts\python.exe -m pip install -r .\tools\terrain\converter\requirements.txt
```

依赖版本由 `requirements.txt` 固定：NumPy 2.3.5、Pillow 12.3.0、pyooz 0.0.8 和 zstandard 0.25.0。

### 获取固定版本的关卡配置

```powershell
git clone --depth 1 --filter=blob:none --no-checkout https://github.com/gszabi99/War-Thunder-Datamine.git .\War-Thunder-Datamine
git -C .\War-Thunder-Datamine fetch --depth 1 origin f7952e4291e9fd7b6fe38e84020dddef925c3457
git -C .\War-Thunder-Datamine sparse-checkout init --cone
git -C .\War-Thunder-Datamine sparse-checkout set aces.vromfs.bin_u/levels
git -C .\War-Thunder-Datamine checkout --detach FETCH_HEAD
```

稀疏检出仅获取转换所需的关卡目录。转换器会核对 `AtlasReferences.json` 所列 69 张地图的目标 `.blkx` 文件，无需完整检出 Datamine。

### 转换并生成发布包

```powershell
.\build.ps1 -WithTerrain -GameRoot "C:\Path\To\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -Python ".\.venv\Scripts\python.exe"
```

该命令调用 `tools/terrain/build-terrain.ps1`，将转换结果写入 `dist/wrpl-acmi/terrain`，再附加到 `wrpl-terrain-installer.exe`。同时生成独立导出器 `wrpl-acmi.exe`。构建过程不会向 Tacview 安装任何内容。

`-Python` 默认为 `python`；使用虚拟环境时应显式指定解释器。`-OutputRoot` 可更改发布包目录。转换中断后，在相同命令后添加 `-ResumeTerrain` 继续；该选项只能与 `-WithTerrain` 同用。未指定时，脚本会拒绝覆盖已有的非空地形输出目录。

## 分别执行转换与打包

只需要生成地形目录时，可在准备好依赖后运行：

```powershell
.\tools\terrain\build-terrain.ps1 -GameRoot "C:\Path\To\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -OutputRoot ".\converted-terrain" -Python ".\.venv\Scripts\python.exe"
```

| 选项 | 说明 |
| --- | --- |
| `-ValidateOnly` | 检查全部输入，不写转换结果。 |
| `-Resume` | 在原输出目录继续中断的转换。 |
| `-Resolution` | 每张地图的输出分辨率，默认 4096 像素。 |
| `-TimeoutSeconds` | 每张地图的转换超时，默认 1200 秒。 |

已有完整转换目录时，可单独打包：

```powershell
.\build.ps1 -TerrainSource ".\converted-terrain"
```

`-TerrainSource` 与 `-WithTerrain` 互斥。普通的 `build.ps1` 调用仍会生成安装器，但不含地形数据，安装时必须另行提供 `-source`。

打包不会重新转换或移动源地图，也不会改变底层转换器的输出约定：直接调用转换器且未指定 `-out` 时，结果写入当前工作目录。

## 产物结构与数据格式

以下列出地形相关的主要产物；发布包还包含项目说明、许可及其他辅助工具。

```text
dist/wrpl-acmi/
  wrpl-acmi.exe
  wrpl-terrain-installer.exe
  build-manifest.json
  run.bat
  data/
  docs/
  tools/terrain/
  terrain/
    AtlasReferences.json
    GeoReferences.json
    maps/<id>/<id>.data
    maps/<id>/metadata.json
    maps/<id>/CustomHeightmapList.xml
```

使用 `-TerrainSource` 打包或安装已有转换目录时，来源根目录必须同时包含 `AtlasReferences.json` 和 `GeoReferences.json`。

`terrain/` 是 `-WithTerrain` 构建时的暂存目录。完整来源包含 69 个地图目录；`.data` 使用以米为单位的 16 位有符号小端高程，按从北到南、从西到东排列。`metadata.json` 记录尺寸、采样边界和 SHA-256，源 XML 使用安装器要求的零点模板。

`smalltests` 中的合成数据仅供测试。含数据的独立安装器不依赖相邻的 `data/`、`terrain/` 或 `tools/` 目录。发布包清单及完整性标记见[构建与发布](../../docs/standalone-build.zh-CN.md)。

## 输入来源与许可

| 输入 | 用途 | 来源与许可说明 |
| --- | --- | --- |
| 客户端编译后地形 | 提供实际高度与几何，包括 `levels/<id>.bin`。 | 用户本机游戏；资产归 Gaijin Entertainment 所有，不随源码仓库分发。 |
| Datamine 关卡配置 | 提供地图边界、水位及相关设置。 | [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) 提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457`。既有来源记录未在该提交发现许可文件；此处作为本地转换输入，不随仓库分发。 |
| 地理参考 | 提供经审核的 WGS84 估算位置。 | [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE) 提交 `c26c0069558079df6597a7bc147cb91391509f89`，MIT 许可。 |
| Python 依赖 | 解码压缩数据并处理栅格。 | NumPy 和 zstandard 为 BSD-3-Clause；pyooz 为 GPL-3.0-or-later；Pillow 为 HPND。 |

项目代码按 AGPL-3.0 发布，各上游来源保留自己的许可。地理参考的声明见 [UPSTREAM-NOTICE.md](georeference/UPSTREAM-NOTICE.md) 和 [UPSTREAM-MIT.txt](georeference/UPSTREAM-MIT.txt)；转换器依赖声明见 [THIRD-PARTY-NOTICES.md](converter/THIRD-PARTY-NOTICES.md)。

Datamine 仅提供配置，不能单独生成游戏高程。将 `.bin` 或 `.blkx` 改名为 `.data` 也不能替代转换。pyooz 所附的 GPL-3.0-or-later ooz 解码器用于读取 Oodle 压缩流，正常安装 pip 依赖后不需要专有 Oodle DLL 或密钥。

## 安装转换后的地形

含地形数据的安装器可直接运行；不含数据或希望更换来源时，使用第二种写法：

```powershell
.\dist\wrpl-acmi\wrpl-terrain-installer.exe
.\dist\wrpl-acmi\wrpl-terrain-installer.exe -source "C:\Terrain\converted-terrain"
```

默认目标为 `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`，可用 `-destination <目录>` 更改。安装器会在写入目标前校验来源元数据与 SHA-256；`-validate-only` 只验证来源，不写目标。

`-profile auto` 默认检测 Tacview 版本，找不到时采用 1.9.4 配置。也可显式指定 `-profile 1.9.4` 或 `-profile 1.9.5`。再次运行时可以复用缓存。

### 坐标布局

默认 geographic 布局包含 14 张使用已审核地理估算位置的地图，以及 55 张使用 Atlas 回退的地图。转换结果、导出器内嵌配置和安装器元数据应使用同一套 69 张地图参考数据。

需要纯 Atlas 技术布局时，安装器使用 `-atlas`，导出器使用 `-terrain-layout atlas`。仅改变一侧会导致错位。`-alt` 只能增加以米为单位的垂直偏移。原理见[地理坐标](../../docs/TERRAIN_GEOGRAPHIC.md)和 [Atlas 布局](../../docs/terrain-atlas.md)，逐图审核结果见[覆盖清单](georeference/coverage.json)。

安装完成后应完全退出并重启 Tacview，并从原始 WRPL 重新导出需要匹配当前地形的 ACMI。

### 调整已安装的高度编码

独立安装器通过 `-profile` 选择编码。需要检查或切换既有安装时，也可使用发布包中的格式工具：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\dist\wrpl-acmi\tools\terrain\Set-Tacview-Terrain-Format.ps1 -Profile 1.9.4
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\dist\wrpl-acmi\tools\terrain\Set-Tacview-Terrain-Format.ps1 -Profile 1.9.5
```

工具的 `-TerrainRoot` 默认为上述安装目标；自定义位置需显式传入。两种编码保持相同的地图位置和 ACMI 坐标：

| 配置 | 高程处理 |
| --- | --- |
| 1.9.4 | 生成根目录高程索引及无符号 16 位副本，配合 -32768 米偏移，保留原始有符号栅格。 |
| 1.9.5 | 恢复逐地图的原生有符号高程定义。 |

格式工具的 `-ValidateOnly` 检查已安装配置，与独立安装器仅验证来源的 `-validate-only` 用途不同。切换后需重启 Tacview。

## 导出与检查

地形准备完成后，可按[导出指南](../../docs/acmi-export.zh-CN.md)生成 ACMI。导出器的默认查找表已内嵌，不依赖地形是否安装。

仓库提供以下合成夹具检查，可从源码根目录运行；它们不读取真实游戏资产：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\test-install-all-terrains.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\geographic\New-Fixture.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\geographic\test-install-geographic-terrains.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\test-terrain-194-encoding.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\test-terrain-format.ps1
```

测试覆盖生成文件、格式转换和往返校验。列出命令不表示当前环境已执行通过，也不能替代两个 Tacview 版本的图形界面验证。
