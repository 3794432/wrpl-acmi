# Windows 单文件构建与发布

[English](standalone-build.md) · [项目说明](../README.zh-CN.md) · [地形工具](../tools/terrain/README.zh-CN.md)

根目录的 `build.ps1` 生成两个可独立使用的 EXE。地形安装器内含转换运行时，不含预生成的 `.data` 或其他游戏数据载荷。

| 程序 | 用途 | 运行要求 |
| --- | --- | --- |
| `wrpl-acmi.exe` | 将客户端回放或服务器分片目录转换为 ACMI，默认查找表已内嵌。 | Windows；导出不需要游戏、已安装地形、Python、Go、Git 或 Tacview。Tacview 用于查看结果。 |
| `wrpl-terrain-installer.exe` | 转换用户本机游戏资产并向 Tacview 安装地形。 | Windows、系统 Windows PowerShell、本机游戏资产，或手动指定的已有转换目录。内含便携 CPython 和四个固定版本依赖；运行时无需用户安装 Python、Go、Git，无需联网或下载游戏数据。 |

## 构建程序

使用 Windows PowerShell 和 Go 1.25.6 或更高版本，在仓库根目录运行：

```powershell
.\build.ps1
```

默认输出到 `dist\wrpl-acmi`。普通构建即可生成自包含安装器；构建机不需要游戏、Datamine 或地形转换结果。构建不会向 Tacview 安装地形。

脚本在**构建时**下载经哈希固定的开源 CPython 运行时和 wheel。确切版本、依赖及制品哈希以 `tools/terrain/runtime-lock.json` 为准。下载内容是转换环境，不是游戏数据；运行时使用内嵌环境离线执行。

| 构建参数 | 用途 |
| --- | --- |
| `-OutputRoot` | 发布包目录，默认为 `dist\wrpl-acmi`。 |
| `-PythonRuntime <目录>` | 选择已准备的便携运行时目录，必须符合固定版本运行时约定。 |
| `-RuntimeCache <目录>` | 指定运行时制品的构建缓存，与安装器的地图转换缓存不同。 |

```powershell
.\build.ps1 -PythonRuntime "C:\Build\terrain-runtime" -RuntimeCache "C:\Build\runtime-cache"
```

旧参数 `-WithTerrain` 和 `-TerrainSource` 会被拒绝并提示迁移。公开安装器不再内嵌转换后的游戏数据。开发者转换请直接调用 `tools/terrain/build-terrain.ps1`，见[地形工具说明](../tools/terrain/README.zh-CN.md#从零构建)；这一手动 Python/Datamine 流程与终端用户的自动本机读取流程分开。安装已有转换目录请在运行安装器时使用 `-source`。

发布包包含两个 EXE、文档、许可和辅助文件。两个 EXE 均可脱离相邻发布包目录运行。`build-manifest.json` 记录构建信息和 EXE 哈希；构建期间或失败后保留 `BUILD-INCOMPLETE.txt`，仅在发布包完整生成后删除。

## 转换并安装本机地形

将 `wrpl-terrain-installer.exe` 放在包含 `levels/` 和 `aces.vromfs.bin` 的《战争雷霆》游戏根目录，然后运行：

```powershell
.\wrpl-terrain-installer.exe
```

默认游戏根目录为 **EXE 所在目录**，不是当前工作目录。也可将 EXE 放在其他位置并显式指定游戏目录：

```powershell
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -resolution 4096
```

安装器从本机 `levels/*.bin` 读取地形，从 `aces.vromfs.bin` 读取配置。默认保持游戏资产只读，将 71 张已知地图转换到本地缓存，再安装到 `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`。本次变更不新增跑道几何。

| 安装器参数 | 说明 |
| --- | --- |
| `-game <目录>` | 本机游戏根目录，默认为 EXE 所在目录。 |
| `-cache <目录>` | 转换地图缓存及日志，默认为 `%LOCALAPPDATA%\wrpl-acmi\terrain`。 |
| `-resolution 4096` | 转换分辨率，默认 4096。 |
| `-destination <目录>` | 更改 Tacview 安装目标目录。 |
| `-profile auto` | 默认检测 Tacview 并选择高程编码；未找到时回退到 1.9.4。 |
| `-profile 1.9.4` | 使用兼容 1.9.4 的高程编码。 |
| `-profile 1.9.5` | 使用 1.9.5 的原生有符号高程布局。 |
| `-validate-only` | 检查输入，不修改目标目录；不转换全部地图，也不证明完整转换能够成功。 |
| `-convert-only` | 生成转换缓存，不向 Tacview 安装。 |
| `-source <目录>` | 手动指定已有转换目录，跳过自动读取游戏。 |
| `-atlas` | 安装 Atlas 技术布局，须与导出器 `-terrain-layout atlas` 配套。 |

```powershell
# 检查本机输入，不安装，也不转换完整地图目录
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -validate-only

# 仅转换到指定缓存
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -cache "D:\TerrainCache" -convert-only

# 安装手动准备的转换目录，不读取游戏资产
.\wrpl-terrain-installer.exe -source "C:\Terrain\converted-terrain"
```

地图转换缓存及日志保留，不自动删除。运行结束时显示缓存路径，并提示不再需要时手动清理；清理后再次安装需要重新转换。再次运行时会检查源文件哈希，再决定是否复用结果；游戏更新后也会核对。排查失败时请保留程序报告的日志。构建时的运行时缓存与运行时的地图转换缓存用途不同。

安装器在安装前核对转换来源的元数据与 SHA-256。使用 `-source` 时，完整转换目录根目录须同时含 `AtlasReferences.json` 和 `GeoReferences.json`。该模式下 `-validate-only` 检查指定来源，不改动目标目录。校验通过不等于 Tacview 已加载或正确渲染地形。

## 坐标布局与兼容性

两个程序默认使用 geographic 布局：14 张采用经审核的 WGS84 估算位置，57 张采用 Atlas 回退。Atlas 回退为技术显示位置。使用纯 Atlas 时，两侧应同时选择：

```powershell
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -atlas
.\wrpl-acmi.exe -terrain-layout atlas -in "replay.wrpl" -out "replay-atlas.acmi"
```

两侧布局或参考配置不同会导致轨迹与地形错位。安装后完全退出并重启 Tacview，再从原始 WRPL 重新导出既有 ACMI。导出器 `-alt` 只调整高度。

71 张地图包括日本港口（`avn_japan`）和红色沙漠（`avg_red_desert`）。本次安装器变更不扩充跑道几何。安装器支持升级旧地形安装，包括 1.9.4 格式。格式选择失败表示安装未完成，应保留程序报告的日志。

## 分发与许可

应发布新构建的、内含运行时的安装器。旧的 69 张或 71 张地图载荷安装器，以及复制这些旧制品的工作流，不能生成本机游戏转换安装器。

新 EXE 不含游戏资产或预生成地形，转换在用户本机进行。解压后的运行时含 `LICENSES/` 目录，保留 CPython 及内嵌依赖的许可声明；准备或分发运行时时须保留这些声明。

项目代码按 AGPL-3.0 发布，依赖保留各自许可。见[地形许可说明](../tools/terrain/README.zh-CN.md#输入来源与许可)和[转换器声明](../tools/terrain/converter/THIRD-PARTY-NOTICES.md)。游戏资产归 Gaijin Entertainment 所有。减少游戏数据的再分发，并不代表已获得提取、转换、使用或再分发这些资产的许可。
