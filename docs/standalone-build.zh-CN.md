# 单文件发布构建

[English](standalone-build.md) · [项目说明](../README.zh-CN.md)

`build.ps1` 会生成两个 Windows 可执行文件，二者可以分别复制和使用：

| 可执行文件 | 用途 | 运行要求 |
| --- | --- | --- |
| `wrpl-acmi.exe` | 把一个客户端回放或一场服务器回放的分片目录转换为 ACMI。正常导出所需的默认查找数据（包括 `ecshashes`）均编译在 EXE 内。 | Windows。导出不需要相邻的 `data/` 目录、已转换或已安装的地形、本机《战争雷霆》、Python、Go、Git 或 Tacview；Tacview 只用于查看导出的 ACMI。 |
| `wrpl-terrain-installer.exe` | 把转换后的《战争雷霆》地形安装到 Tacview。构建时可以把地形 payload 附加到 EXE。 | 系统 Windows PowerShell（`powershell.exe`）。完整离线安装器还需要为临时展开和安装预留数 GB 磁盘空间。安装时不需要游戏、Python、Go、Git 或转换脚本。 |

两个文件职责独立，可以单独发布。没有提供地形输入时，`build.ps1` 仍会生成地形安装器，但该副本只有安装逻辑，不能称为完整的离线地形安装器；只有为它提供 `-source` 转换目录，或用 `-TerrainSource` / `-WithTerrain` 重新构建，才能实际安装地形数据。

## 构建

源码构建需要 Windows PowerShell 和 Go 1.25.6 或更高版本，在仓库根目录执行。默认输出到 `dist\wrpl-acmi`。

### 导出器与仅含逻辑的安装器

```powershell
.\build.ps1
```

该命令生成 `wrpl-acmi.exe` 和 `wrpl-terrain-installer.exe`。导出器本身已是完整单文件程序；地形安装器不含地形数据，安装时须用 `-source` 指定转换目录，或改用 `-TerrainSource` / `-WithTerrain` 重新构建。

### 打包已有转换地形

```powershell
.\build.ps1 -TerrainSource ".\converted-terrain"
```

`-TerrainSource` 接受已经转换好的地形目录，例如已有的 69 张地图转换结果。构建会把该 payload 附加到 `wrpl-terrain-installer.exe`。用这条路径打包已有结果时，构建机不需要 Python、Git、本机游戏或 Tacview；仍需源码树和 Go 工具链来生成 EXE。

`-TerrainSource` 不会重新转换、改形或移动目录中的地图，也不改变转换器默认的 `-out` 规则：转换器未指定 `-out` 时仍写到当前工作目录。

### 构建时转换并内嵌

```powershell
.\build.ps1 -WithTerrain `
  -GameRoot "C:\Path\To\War Thunder" `
  -DatamineRoot ".\War-Thunder-Datamine" `
  -Python ".\.venv\Scripts\python.exe"
```

该命令从本机游戏和固定版本 Datamine 转换 69 张地图，再把结果内嵌到同一个安装器 EXE。转换环境见[地形工具说明](../tools/terrain/README.zh-CN.md)。转换中断后，用相同参数加 `-ResumeTerrain` 继续；`-TerrainSource` 与 `-WithTerrain` 不能同时使用。

构建会写出 `build-manifest.json`，记录产物哈希和地形安装器是否包含数据。构建进行中或失败后会保留 `BUILD-INCOMPLETE.txt`，只有包完整生成后才删除该标记。

## 安装地形

安装器内含 payload 时，默认安装这组 69 张地图：

```powershell
.\wrpl-terrain-installer.exe
```

改用外部转换目录：

```powershell
.\wrpl-terrain-installer.exe -source "C:\Terrain\converted-terrain"
```

支持的参数如下：

| 参数 | 含义 |
| --- | --- |
| `-source <目录>` | 可选的转换后地形目录；提供时覆盖内嵌 payload。使用不含地形数据的安装器构建时必须提供。 |
| `-destination <目录>` | 安装目标。默认 `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`。 |
| `-profile auto` | 检测已安装的 Tacview 版本并写入匹配的高度编码；这是默认值。 |
| `-profile 1.9.4` | 生成兼容 1.9.4 的高度编码。 |
| `-profile 1.9.5` | 安装 1.9.5 使用的原生有符号高度布局。 |
| `-validate-only` | 仅验证 source，不向目标写入任何内容。 |
| `-atlas` | 安装技术 Atlas 坐标布局；须与导出器 `wrpl-acmi.exe -terrain-layout atlas` 配套。 |

`-profile auto` 会查找 Tacview 可执行文件并选择匹配的高度编码；找不到时回退到 1.9.4 配置。重新运行安装器可以复用缓存。默认 4096 分辨率的完整 69 地图集应预留至少 8 GB 磁盘空间，用于临时展开、原始栅格及 1.9.4 高程副本。

安装器在写入目标前按来源 SHA-256 元数据校验数据。校验能确认 source 数据和所支持的编码路径，不代表 Tacview GUI 已成功加载或渲染地形。

## 坐标布局

默认使用 geographic 布局，由审核后的地理参考和 Atlas 回退组成。只有对应的 ACMI 导出使用以下选项时，才应在安装时加 `-atlas`：

```powershell
.\wrpl-acmi.exe -terrain-layout atlas ...
```

安装器的 `-atlas` 与导出器的 `-terrain-layout` 必须一致；两边布局不同会造成地形与 ACMI 对象不对齐。两边默认都是 geographic。

## 发布边界

游戏高程数据不加入 Git。带高程的安装器把转换数据附加在 EXE 中；仓库保存转换器、安装器和配置，完整地形安装程序使用本地转换后的游戏高程构建。

导出器默认数据内嵌，因此不依赖包目录。`wrpl-acmi.exe -ecshashes` 默认为 `embedded`；显式 JSON 路径会覆盖内嵌表，空字符串会关闭该表。未安装《战争雷霆》地形时，Tacview 可以显示默认海面或默认背景地形，但 ACMI 中的飞机、地面对象、导弹、炸弹和干扰物仍会正常导出。安装地形只改变显示景观，不影响回放对象导出。
