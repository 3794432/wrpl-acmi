# WRPL-ACMI

[简体中文](README.zh-CN.md) | [English](README.md)

> 一个将 战争雷霆`.wrpl`回放转换为 Tacview 兼容`.acmi`的单文件`.exe`工具

<p align="center">
  <img src="docs/01.png" width="48%" />
  <img src="docs/02.png" width="48%" />
</p>

[使用指南](docs/acmi-export.zh-CN.md) | **[下载发布版本](https://github.com/3794432/wrpl-acmi/releases)** | [文档目录](docs/README.md) | [构建方式](docs/standalone-build.zh-CN.md)

## 功能与用途

### 基本功能

* 将 [战争雷霆](https://warthunder.com/) 的空战回放转换为可在 [Tacview](https://www.tacview.net/) 中播放的格式
* 将本地回放的单个`.wrpl`, 服务器回放多个`.wrpl`构成的目录转换为`.acmi`文件
* 将飞机多次出击分别导出为独立对象，支持复用单位 UID 或同一机型的再次出击
* 可直接将文件/目录拖放到`.exe`上进行转换, 或使用`-in`等方式传入文件地址

### 高级功能

* 支持开关干扰物, 雷达目标等多种数据的导出
* 支持导出轨迹清单, 导弹飞行参数等`.csv`数据文件
* 可根据飞行器位置与姿态估算航向, 速度, G值等`.acmi`原生数据
* 使用原生 ACMI 字段和标签显示回放记录的发射后导引头状态及载机雷达模式，见[传感器状态与兼容性](docs/ACMI_SENSOR_STATES.zh-CN.md)
* 导出[目录内地空导弹系统](docs/acmi-information.zh-CN.md#地空导弹)的实际飞行轨迹，关联明确的发射车身份与红蓝阵营
* 使用完整固定版本的 AAM/AGM 名称目录，覆盖样例回放中没有出现的型号（如 AIM-120D/B、R-77-1）；具体变体优先采用武器报告或实际挂载依据，也可按制导类型和载机可挂载目录推断基础型号
* 根据武器报告及精确载机挂载记录识别[空对地导弹型号](docs/acmi-information.zh-CN.md#空对地导弹型号)，无法区分的混装配置保留候选
* 根据武器报告及实际挂载识别[炸弹型号](docs/acmi-information.zh-CN.md#炸弹型号)，保留真实轨迹；仅有投放点时，尽可能补充明确标记的初速与重力估算轨迹
* 未确定的 SAM 型号默认优先采用雷达弹及同类最新已核对改进型，标记为 `Assumed`；使用 `-sam-model-fallback off` 可仅按证据命名
* 在地面单位原生销毁事件中显示有记录依据的攻击载具、玩家和武器；攻击机已消失时，也可使用报告保留的有效玩家身份
* 省略车队生成点尚未绑定实体身份的静止初始化快照，避免临时地面对象及开局移除/销毁误报


项目基于 [wrpl-inspector v3](https://github.com/maxsupermanhd/wrpl-inspector)，保留了 Go 回放解析库和 dear-imgui 图形检查器，并提供无图形界面的导出器`cmd/acmi`。开发分支为 [dev](https://github.com/3794432/wrpl-acmi/tree/dev)；Go 模块路径仍为`github.com/maxsupermanhd/wrpl-inspector/v3`。

## 快速开始

### 下载

从[最新发布](https://github.com/3794432/wrpl-acmi/releases/latest)下载可执行文件:

* ["wrpl-acmi.exe"](https://github.com/3794432/wrpl-acmi/releases/latest/download/wrpl-acmi.exe)用于将回放转换为`.acmi` (必选)
* ["wrpl-terrain-installer.exe"](https://github.com/3794432/wrpl-acmi/releases/latest/download/wrpl-terrain-installer.exe)用于为 Tacview 安装地形 (可选)

下文所述的本机游戏转换安装器由当前 `dev` 分支构建。较旧的 Release 可能仍提供内含预生成游戏数据的地形安装器，请先查看发布说明，确认是否适用以下安装方式。

### 将`.wrpl`回放转换为`.acmi`文件

在 PowerShell 中运行该转换程序的示例如下:

```powershell
.\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
.\wrpl-acmi\wrpl-acmi.exe -in "C:\Replays\server-session" -out "C:\Replays\server-session.acmi"
```

可以在其他路径单独运行`wrpl-acmi.exe`, 默认 LUT 已编译在程序中. 转换文件时并不需要相邻的目录, 本机游戏, 已安装地形, Powershell, Python, Go, Git, Tacview 等等. Tacview 用于打开生成的 ACMI 文件.

服务器回放应先下载到本地, 然后将同一场的分片放在同一目录中, 再将目录作为输入. 程序会根据分片头排序, 不接受单个服务器分片. 具体信息见[服务器回放分片导出](docs/ACMI_SERVER_REPLAYS.md).

也可以将单个客户端回放文件或服务器回放目录拖放到`wrpl-acmi.exe`上来进行转换. 未指定`-out`时, 程序会在当前工作目录生成`<输入文件名>.acmi`; 服务器回放缺少尾片时, 默认生成`<输入目录名>.partial.acmi`, 并标注回放不完整. 包内的`run.bat`是可选入口, 运行时会先切换到运行目录.

从命令行运行还支持如下的单个位置参数. 使用这种写法时, 所有选项必须位于输入路径之前, 且不能再指定`-in`参数.

```powershell
.\wrpl-acmi.exe -out "C:\Replays\replay.acmi" "C:\Replays\replay.wrpl"
```

完整参数, 输出规则, 及常见问题见[导出指南](docs/acmi-export.zh-CN.md).

### 安装地形以在 Tacview 中使用

安装地形是可选步骤. 未安装游戏内地形时, ACMI 仍会包含导出的回放对象, 但 Tacview 的背景地形可能与游戏地图不符.

独立安装器不含预生成的 `.data` 地形。将 `wrpl-terrain-installer.exe` 放在《战争雷霆》游戏根目录（包含 `levels/` 和 `aces.vromfs.bin` 的目录），然后运行：

```powershell
.\wrpl-terrain-installer.exe
```

默认游戏目录取 EXE 所在目录，与当前工作目录无关。安装器以默认 4096 分辨率转换本机资产，再安装到 `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`，游戏资产保持只读。EXE 内含便携 CPython 和四个固定版本依赖；用户无需安装 Python、Go 或 Git，运行时无需联网，也不下载游戏数据。安装辅助步骤使用系统 Windows PowerShell。

首次运行会依次转换全部 71 张地图，可能需要数分钟；当前为逐图串行转换，总体 CPU 占用较低属于正常现象。默认分辨率下，请为缓存、安装结果和临时文件预留至少 8 GB 空间。再次运行会复用未变化的地图转换结果；删除提示中的地图缓存后，下次需要重新转换。

可用 `-game <目录>` 指定其他游戏目录，`-cache <目录>` 更改默认的 `%LOCALAPPDATA%\wrpl-acmi\terrain` 缓存，`-resolution 4096` 指定转换分辨率。地图转换缓存及日志保留以便续作，复用前核对源文件哈希。运行结束时显示缓存路径，并提示不再需要时手动清理，不自动删除。`-profile auto`、`-profile 1.9.4`、`-profile 1.9.5` 用于选择 Tacview 高程编码。`-validate-only` 检查输入，不转换全部地图，也不修改安装目标；`-convert-only` 仅生成转换缓存，不安装。`-source <目录>` 手动指定已有转换目录，此模式跳过自动读取游戏。命令示例见[地形工具说明](tools/terrain/README.zh-CN.md)。

安装后应完全退出并重启 Tacview，再从原始 `.wrpl` 重新导出先前导出的回放，以确保轨迹与地形使用相同坐标配置。

导出和安装默认使用 geographic 布局, 71 张地图中. 14 张采用经审核的 WGS84 估算位置, 57 张使用 Atlas 回退坐标. Atlas 坐标用于技术展示, 不代表真实地理位置, 显式选择 Atlas 时,安装器的`-atlas`必须与导出器的 `-terrain-layout atlas` 配套使用,`-alt`仅调整垂直偏移.

地形集已包含比赛地图日本港口（`avn_japan`）和红色沙漠（`avg_red_desert`），两张均使用 Atlas 回退。使用这两张地图时，请同时更新两个 EXE，运行新版安装器，再从原始回放重新导出。已有地图坐标保持不变；安装器支持从旧版 69 张地图升级，包括 Tacview 1.9.4 格式。

自动安装器从本机 `levels/*.bin` 读取高程，从本机 `aces.vromfs.bin` 读取边界、水位等配置。独立的开发者流程仍可使用 [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) 配置。经纬度定位来自 [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE) 的参考数据。已知地图目录仍为 71 张；本次安装器变更不新增跑道几何。

## ACMI内容与数据类型

通过`.wrpl`转换出的`.acmi`同时包含原始回放记录, 由记录推导的估算值, 以及静态配置资料. 阅读时应结合对象的来源字段来区分这三类信息. 默认输出 acmi 2.1.

| 内容 | 数据类型 |
| --- | --- |
| 空中与地面单位 | 使用回放中的位置, 飞行器保留姿态; 对移动地面单位的朝向进行推测, 销毁时先写入`Destroyed` 再移除; 符合条件的静态建筑可使用带时间戳的 ECS 构造位置
| 导弹与炸弹 | 优先使用实际位置, 导弹运动参数通过位置进行计算; 只有构造记录的炸弹默认补充明确标记的初速/重力估算轨迹; `-estimate-bomb-trajectories=false` 保留投放点标记
| 干扰物 | 有可靠弹药证据时区分 Flare 和 Chaff, 无法确认时显示为 Chaff, 干扰物轨迹通过推测得出
| 机场与跑道 | 按地图, 任务和可核对的雷达记录进行配置, 目录覆盖 71 张地图, 457 个任务, 其中 67 张有可用跑道信息
| 雷达目标指定 | 写入 Tacview 的`LockedTarget` 属性及可计算的距离, 角度信息, 目标指定可能包含跟踪信息不代表雷达处于跟踪模式
| 防空参考范围 | 硬编码 255 个型号
| 元数据与事件 | 包括标题, 作者, 时间, 来源备注, 阵营分组, 及回放事件消息. Award 和高亮书签默认关闭, 飞机遥测估算默认开启 |

默认情况下`-events=kill,chat,join,damage,radar`输出有明确归属和武器的击杀, 聊天, 玩家名单变化, Critical/Severe Damage, 及雷达目标指定变化; 击杀归属缺失或冲突时跳过, 飞机击杀时间必须与销毁时间匹配.可以使用`-events=none`关闭，或通过逗号分隔想要选择的类别子集. Award默认关闭, 添加`award`或使用`-events=all`可开启. 高亮书签默认关闭, 保留普通 Chaff/Flare 日志, 避免重复高亮行, 需要时设置 `-bookmarks=true`. 详见[事件消息说明](docs/acmi-information.zh-CN.md#事件消息时间线).

轨迹清单, 导弹飞行参数, 干扰物构造和雷达指定变化的 CSV 报告均为可选输出, 只有提供对应路径时才会生成.

飞机速度和G值等为无风, 标准大气环境下计算; 其中 IAS 为 EAS 近似, 三轴G值为比力估算. 静态制导类型和导引头工作状态无关, 跑道信息也不能证明该跑道在当前游戏中启用. 详细信息参考文档.

## 从源码构建

构建 Windows 发布包需要 PowerShell 和 Go 1.25.6 或更高版本. 在 PowerShell 中获取源码并运行构建脚本:

```powershell
git clone --branch dev --single-branch https://github.com/3794432/wrpl-acmi.git
Set-Location .\wrpl-acmi
.\build.ps1
```

默认输出目录为 `dist\wrpl-acmi`, 其中包含两个可独立使用的程序：

* `wrpl-acmi.exe` 用于导出回放, 默认数据已内嵌.
* `wrpl-terrain-installer.exe` 内含转换运行时，读取用户本机游戏并将转换后的地形安装到 Tacview，不含预生成的 `.data` 文件。

`build.ps1` 默认在构建时下载经哈希固定的开源 CPython 运行时和 wheel，版本及哈希以 `tools/terrain/runtime-lock.json` 为准，不下载游戏数据。`-PythonRuntime` 可选择已准备的运行时目录，`-RuntimeCache` 可指定构建缓存。旧参数 `-WithTerrain` 和 `-TerrainSource` 会被拒绝并提示迁移；公开安装器不再打包转换后的游戏资产。

开发者手动流程 `tools/terrain/build-terrain.ps1` 仍使用 Python 和固定版本 Datamine，与终端用户的自动本机转换分开。环境及命令见[地形工具说明](tools/terrain/README.zh-CN.md#从零构建)，构建参数和发布产物见[构建与发布指南](docs/standalone-build.zh-CN.md)。历史发布可能落后于 dev，需要当前实现时应从该分支构建。

## 责任, 相关项目, 与许可

本项目尚在早期开发中, 运行本仓库编译及发布产物时自行对运行造成的后果负责. 仓库的[源码构建工作流](.github/workflows/source-build.yml)配置了导出器、解析库及合成地形检查。构建独立安装器不需要游戏资产，真实地形转换需要用户本机游戏；Tacview 图形界面的加载和渲染也需要另行验证。

本项目基于 [wrpl-inspector](https://github.com/maxsupermanhd/wrpl-inspector) v3 开发, 保留了 Go 回放解析库和 dear-imgui 图形检查器, 并提供无图形界面的导出器`cmd/acmi`; Go 模块路径仍为`github.com/maxsupermanhd/wrpl-inspector/v3`.
本项目部分内容来自 [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) 和  [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE).

本项目及 [wrpl-inspector](https://github.com/maxsupermanhd/wrpl-inspector) 库按 [AGPL-3.0 license](https://github.com/3794432/wrpl-acmi#AGPL-3.0-1-ov-file) 发布. 上游版权与许可声明予以保留. 地图和游戏资产归 [Gaijin Entertainment](https://gaijinent.com/) 所有.

内嵌运行时及依赖的许可声明随运行时解压到 `LICENSES/` 目录。避免分发预生成的游戏数据可减少再分发，但并不代表已获得提取、转换、使用或再分发游戏资产的许可。
