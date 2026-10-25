# Windows 单文件构建与发布

[English](standalone-build.md) · [项目说明](../README.zh-CN.md) · [地形工具](../tools/terrain/README.zh-CN.md)

根目录的 `build.ps1` 生成导出器和地形安装器。两者可以分别复制、运行和发布，是否内嵌地形数据取决于构建参数。

| 程序 | 用途 | 运行要求 |
| --- | --- | --- |
| `wrpl-acmi.exe` | 将客户端回放或服务器分片目录转换为 ACMI。 | Windows；默认查找表已内嵌，不需要游戏、地形目录、Python、Go、Git 或 Tacview。 |
| `wrpl-terrain-installer.exe` | 将转换后的地形安装到 Tacview 的地形目录。 | 系统 Windows PowerShell；地形数据可来自 EXE 内嵌资源或外部转换目录。 |

Tacview 用于查看导出的结果。安装地形不会改变导出器读取回放的能力。

## 选择构建方式

源码构建需要 Windows PowerShell 和 Go 1.25.6 或更高版本。以下命令均在仓库根目录运行，默认输出到 `dist\wrpl-acmi`。

### 只构建程序

```powershell
.\build.ps1
```

该命令生成完整的单文件导出器，以及不含地形数据的安装器。后者安装时必须使用 `-source` 指定已转换的地形目录，不能作为完整离线地形包使用。

### 打包已有地形

```powershell
.\build.ps1 -TerrainSource ".\converted-terrain"
```

`-TerrainSource` 指向已经转换好的地形目录。构建会将数据附加到安装器 EXE，不重新转换地图。构建机仍需要源码、PowerShell 和 Go，但不需要 Python、Git、游戏或 Tacview。

正常的 geographic 地形集应包含全部 69 张地图及配套参考配置。目录结构见[地形工具说明](../tools/terrain/README.zh-CN.md)。

### 转换地形后打包

```powershell
.\build.ps1 -WithTerrain `
  -GameRoot "C:\Path\To\War Thunder" `
  -DatamineRoot ".\War-Thunder-Datamine" `
  -Python ".\.venv\Scripts\python.exe"
```

该方式需要本机游戏、固定版本 Datamine、64 位 Python 3.12 及转换器依赖。脚本先转换全部 69 张地图，再将结果内嵌到安装器。首次执行前，请按[从零构建](../tools/terrain/README.zh-CN.md#从零构建)准备环境。

转换中断后，可在原命令后添加 `-ResumeTerrain` 继续。该选项必须与 `-WithTerrain` 同用；未指定时，地形构建会拒绝覆盖已有的非空输出目录。`-TerrainSource` 与 `-WithTerrain` 不能同时使用。

### 构建参数与产物

| 参数 | 用途 |
| --- | --- |
| `-OutputRoot` | 输出目录，默认为 `dist\wrpl-acmi`。 |
| `-TerrainSource` | 打包现成的地形转换目录。 |
| `-WithTerrain` | 构建时转换并内嵌地形。 |
| `-ResumeTerrain` | 继续中断的地形转换。 |
| `-GameRoot` | 本机《战争雷霆》目录。 |
| `-DatamineRoot` | 固定版本 Datamine 目录。 |
| `-Python` | 转换用 Python 解释器，默认为 `python`。 |

构建会输出两个 EXE、可选启动脚本 `run.bat`、项目说明、许可文件、文档、辅助数据和工具目录。两个 EXE 以关闭 CGO 的方式构建；默认导出不依赖旁边的辅助文件。`-WithTerrain` 还会在 `terrain/` 中保留转换结果，构建本身不会向 Tacview 安装地形。

`build-manifest.json` 记录程序哈希及安装器是否包含地形数据。构建期间或失败后会保留 `BUILD-INCOMPLETE.txt`；只有发布包完整生成后才会删除该标记。

## 安装地形

含地形数据的安装器可以直接运行：

```powershell
.\wrpl-terrain-installer.exe
```

使用外部转换目录时，传入 `-source`。该目录会优先于内嵌数据：

```powershell
.\wrpl-terrain-installer.exe -source "C:\Terrain\converted-terrain"
```

安装过程不需要游戏、Python、Go、Git 或转换脚本。默认 4096 分辨率的 69 张地图建议预留至少 8 GB 空间，供临时展开、原始栅格及 1.9.4 高程副本使用。再次运行时可以复用缓存。

| 参数 | 说明 |
| --- | --- |
| `-source <目录>` | 外部地形转换目录；安装器不含数据时必须提供。 |
| `-destination <目录>` | 目标目录，默认 `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`。 |
| `-profile auto` | 自动检测 Tacview 版本并选择高度编码；默认值，未找到 Tacview 时采用 1.9.4 配置。 |
| `-profile 1.9.4` | 使用兼容 1.9.4 的高度编码。 |
| `-profile 1.9.5` | 使用 1.9.5 的原生有符号高度布局。 |
| `-validate-only` | 仅验证来源数据，不写入安装目标。 |
| `-atlas` | 安装 Atlas 技术坐标布局，须与导出器的 Atlas 选项配套。 |

安装器在写入目标前核对元数据和 SHA-256。数据校验通过表示来源和编码检查通过，不等同于 Tacview 已完成界面加载或渲染验证。安装后应完全退出并重启 Tacview。

## 保持地形与轨迹一致

安装器和导出器默认均使用 geographic 布局。需要 Atlas 布局时，两侧应同时选择：

```powershell
.\wrpl-terrain-installer.exe -atlas
.\wrpl-acmi.exe -terrain-layout atlas -in "replay.wrpl" -out "replay-atlas.acmi"
```

两侧布局或参考配置不同，会导致地形与回放对象错位。已有 ACMI 应从原始 WRPL 重新导出。`-alt` 只能调整高度，不能修复水平位置差异。

## 安装问题与发布范围

地形安装器未变化时，维护者可手动运行 Actions 中的
`Copy terrain installer to draft release`，将既有发布中经过 SHA-256
校验的 69 张地图安装器复制到新草稿。需要提供来源标签、目标草稿标签及其
确切源码提交；草稿须已包含且仅包含转换器。工作流不覆盖现有附件，完成后
仍保持草稿，由维护者发布；它不构建新地形，也不替换已变化的安装器。

旧版 1.9.4 的汇总索引可能在缓存或格式状态删除后残留。安装器可以接管与当前地图定义一致的条目，并重建缺失的高程副本；路径、几何不一致或 ID 重复时会报错。格式切换失败表示安装尚未完成，应保留错误信息指定的 `install.log` 供排查。

游戏高程不提交到 Git。源码仓库保存转换器、安装器和配置；含地形的安装器由本地转换结果构建。转换和打包不会改变回放导出规则：导出器仍可独立运行，`-ecshashes` 默认为 `embedded`，未指定输出路径时仍按输入名称在当前工作目录生成 ACMI。
