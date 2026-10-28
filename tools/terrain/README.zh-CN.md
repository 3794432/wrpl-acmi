# 地形提取、转换与安装

[English](README.md) · [项目说明](../../README.zh-CN.md) · [构建与发布](../../docs/standalone-build.zh-CN.md)

独立安装器读取用户本机《战争雷霆》资产，转换 71 张已知地图，再安装到 Tacview。EXE 内含便携 CPython 和四个固定版本依赖，不含预生成的 `.data` 地形。游戏资产不提交到 Git，也不包含在新版安装器中。

正常使用请阅读[安装转换后的地形](#安装转换后的地形)。下述手动 Python/Datamine 流程供开发者使用，与安装器自动读取本机游戏的流程分开。

## 环境要求

| 操作 | 所需环境 |
| --- | --- |
| 构建程序 | Windows PowerShell、Go 1.25.6 或更高版本及源码。默认在构建时下载经哈希固定的开源运行时和 wheel，不下载游戏数据。 |
| 自动转换并安装本机地形 | Windows、系统 Windows PowerShell、独立安装器及本机 `levels/*.bin` 和 `aces.vromfs.bin`。运行时无需用户安装 Python、Go、Git，无需联网或下载游戏数据。 |
| 开发者手动转换 | 64 位 Python 3.12、转换器依赖、本机编译后关卡和匹配的 Datamine 配置。以下使用 Git 获取固定提交。 |
| 导出回放 | 独立 `wrpl-acmi.exe`；转换或安装地形为可选步骤。 |

## 从零构建

### 构建独立程序

在源码根目录运行：

```powershell
.\build.ps1
```

该命令生成两个 EXE，其中地形安装器为自包含程序。`tools/terrain/runtime-lock.json` 固定确切的运行时版本、四个依赖及制品哈希。`-PythonRuntime <目录>` 选择已准备的运行时，`-RuntimeCache <目录>` 指定构建缓存。构建安装器无需游戏或 Datamine。

旧构建参数 `-WithTerrain` 和 `-TerrainSource` 会被拒绝并提示迁移。公开安装器不再打包转换后的游戏数据。手动使用已有转换目录时，请传入安装器运行参数 `-source`。

### 准备开发者手动转换环境

这些步骤仅用于 `tools/terrain/build-terrain.ps1`；终端用户无需准备此环境或下载 Datamine。

```powershell
git clone --branch dev --single-branch https://github.com/3794432/wrpl-acmi.git
Set-Location .\wrpl-acmi
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r .\tools\terrain\converter\requirements.txt

git clone --depth 1 --filter=blob:none --no-checkout https://github.com/gszabi99/War-Thunder-Datamine.git .\War-Thunder-Datamine
git -C .\War-Thunder-Datamine fetch --depth 1 origin f7952e4291e9fd7b6fe38e84020dddef925c3457
git -C .\War-Thunder-Datamine sparse-checkout init --cone
git -C .\War-Thunder-Datamine sparse-checkout set aces.vromfs.bin_u/levels
git -C .\War-Thunder-Datamine checkout --detach FETCH_HEAD
```

确认 `python` 为 64 位 Python 3.12。`requirements.txt` 固定 NumPy 2.3.5、Pillow 12.3.0、pyooz 0.0.8 和 zstandard 0.25.0。稀疏检出提供 `AtlasReferences.json` 所列 71 张地图的目标配置；手动配置必须与本机游戏资产匹配。

### 手动转换

```powershell
.\tools\terrain\build-terrain.ps1 -GameRoot "C:\Games\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -OutputRoot ".\converted-terrain" -Python ".\.venv\Scripts\python.exe"
```

该命令生成转换来源目录，不构建安装器，也不向 Tacview 安装。

| 手动脚本参数 | 说明 |
| --- | --- |
| `-ValidateOnly` | 检查输入，不写转换结果。 |
| `-Resume` | 在同一输出目录续作；未指定时拒绝非空输出目录。 |
| `-Resolution` | 输出分辨率，默认 4096。 |
| `-TimeoutSeconds` | 每张地图超时，默认 1200 秒。 |

输出路径限制、转换命令和校验约定见 [BUILD-CONTRACT.md](converter/BUILD-CONTRACT.md)。该手动脚本的参数与独立 EXE 的小写参数不同。

## 产物结构与数据格式

发布包包含 `wrpl-acmi.exe`、`wrpl-terrain-installer.exe`、文档、许可声明和辅助文件。安装器携带运行时及转换工具，不携带已转换地图，也不依赖相邻的 `data/`、`terrain/` 或 `tools/` 目录。

转换生成本地缓存，默认位于 `%LOCALAPPDATA%\wrpl-acmi\terrain`，包含可续作的地图结果和日志。完整转换来源（包括手动 `-source` 来源）具有以下地形结构：

```text
<converted-source>/
  AtlasReferences.json
  GeoReferences.json
  maps/<id>/<id>.data
  maps/<id>/metadata.json
  maps/<id>/CustomHeightmapList.xml
```

目录包含 71 张地图。栅格使用以米为单位的有符号 16 位小端高程，按从北到南、从西到东排列。元数据记录尺寸、边界和 SHA-256；来源 XML 使用零点模板，由安装器应用 geographic 或 Atlas 定位。手动脚本还生成构建约定中列出的目录和校验报告。`smalltests` 合成地形仅用于测试。

## 输入来源与许可

| 输入 | 用途 | 来源与许可说明 |
| --- | --- | --- |
| 客户端编译后地形 | 提供实际高度与几何，包括 `levels/<id>.bin`。 | 用户本机游戏；资产归 Gaijin Entertainment 所有，不随源码仓库分发。 |
| 本机游戏配置 | 为自动转换提供边界、水位等设置。 | 读取用户本机的 `aces.vromfs.bin`，运行时不下载。 |
| Datamine 关卡配置 | 提供地图边界、水位及相关设置。 | [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) 提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457`。既有来源记录未在该提交发现许可文件；此处作为本地转换输入，不随仓库分发。 |
| 地理参考 | 提供经审核的 WGS84 估算位置。 | [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE) 提交 `c26c0069558079df6597a7bc147cb91391509f89`，MIT 许可。 |
| Python 依赖 | 解码压缩数据并处理栅格。 | NumPy 和 zstandard 为 BSD-3-Clause；pyooz 为 GPL-3.0-or-later；Pillow 为 HPND。 |

项目代码按 AGPL-3.0 发布，各上游来源保留自己的许可。地理参考的声明见 [UPSTREAM-NOTICE.md](georeference/UPSTREAM-NOTICE.md) 和 [UPSTREAM-MIT.txt](georeference/UPSTREAM-MIT.txt)；转换器依赖声明见 [THIRD-PARTY-NOTICES.md](converter/THIRD-PARTY-NOTICES.md)。

Datamine 仅用于上述开发者手动流程，自动安装器读取本机游戏配置。运行时及 wheel 的固定版本以 `runtime-lock.json` 为准。CPython 和依赖声明随运行时解压到 `LICENSES/` 目录，准备或分发运行时时须保留。减少游戏数据再分发，并不代表已获得提取、转换、使用或再分发游戏资产的许可。

Datamine 仅提供配置，不能单独生成游戏高程。将 `.bin` 或 `.blkx` 改名为 `.data` 也不能替代转换。pyooz 所附的 GPL-3.0-or-later ooz 解码器用于读取 Oodle 压缩流，正常安装 pip 依赖后不需要专有 Oodle DLL 或密钥。

## 安装转换后的地形

将独立 EXE 放在包含 `levels/` 和 `aces.vromfs.bin` 的游戏根目录，然后运行：

```powershell
.\wrpl-terrain-installer.exe
```

默认游戏根目录为 EXE 所在目录，不是当前工作目录。默认保持游戏资产只读。内嵌运行时离线执行转换，无需用户安装 Python、Go、Git，也不下载游戏数据。EXE 位于其他位置时，可显式选择游戏目录：

```powershell
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -resolution 4096
```

默认目标为 `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`，可用 `-destination <目录>` 更改。分辨率默认 4096。`-cache <目录>` 更改默认的 `%LOCALAPPDATA%\wrpl-acmi\terrain` 缓存，其中保留可续作地图和日志；再次运行时检查源文件哈希再决定是否复用。排查转换或安装失败时请保留程序报告的日志。

`-profile auto` 默认检测 Tacview 版本，找不到时采用 1.9.4 配置。也可显式指定 `-profile 1.9.4` 或 `-profile 1.9.5`。

```powershell
# 检查输入，不改变目标目录，也不转换全部地图
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -validate-only

# 仅转换到缓存，不安装
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -cache "D:\TerrainCache" -convert-only

# 手动指定已有转换来源，不自动读取游戏
.\wrpl-terrain-installer.exe -source "C:\Terrain\converted-terrain"
```

`-validate-only` 检查输入，不修改目标目录；不会转换全部地图，也不证明每张地图均可转换。`-convert-only` 生成转换缓存但不安装。`-source` 需要含两个参考 JSON 文件的完整转换目录，跳过自动读取本机游戏。安装前会校验转换元数据及 SHA-256；`-source` 模式下校验的是指定来源。校验通过不代表 Tacview 界面已正确渲染。

### 坐标布局

默认 geographic 布局包含 14 张使用已审核地理估算位置的地图，以及 57 张使用 Atlas 回退的地图。转换结果、导出器内嵌配置和安装器元数据应使用同一套 71 张地图参考数据。

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

## 比赛空战地图

71 张地图目录包含日本港口（`avn_japan`）和红色沙漠（`avg_red_desert`）。即使内部文件名带陆战或海战前缀，也会按显式清单转换。两张地图在默认 geographic 布局下均采用 Atlas 回退；已有地图坐标不变。红色沙漠使用完整地图地形加局部 HM2 叠加，不能将局部高度图拉伸到全图。本次本机游戏安装器变更不新增跑道几何，这两张地图的跑道源几何尚未审核。更新两个 EXE，运行本机游戏安装器，再从原始回放重新导出。

地图转换缓存及日志在安装后继续保留，不自动删除。运行结束时显示缓存路径，并提示不再需要时手动清理；清理后再次安装需要重新转换。
