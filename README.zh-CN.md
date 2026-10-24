# WRPL-ACMI

[简体中文](README.zh-CN.md) | [English](README.md)

> 一个将 战争雷霆`.wrpl`回放转换为 Tacview 兼容`.acmi`的单文件`.exe`工具

图片 图片

[使用指南](docs/acmi-export.zh-CN.md) | **[下载发布版本](https://github.com/3794432/wrpl-acmi/releases)** | [文档目录](docs/README.md) | [构建方式](docs/standalone-build.zh-CN.md)

## 功能与用途

### 基本功能

* 将 [战争雷霆](https://warthunder.com/) 的空战回放转换为可在 [Tacview](https://www.tacview.net/) 中播放的格式
* 将本地回放的单个`.wrpl`, 服务器回放多个`.wrpl`构成的目录转换为`.acmi`文件
* 可直接将文件/目录拖放到`.exe`上进行转换, 或使用`-in`等方式传入文件地址

### 高级功能

* 支持开关干扰物, 雷达目标等多种数据的导出
* 支持导出轨迹清单, 导弹飞行参数等`.csv`数据文件
* 可根据飞行器位置与姿态估算航向, 速度, G值等`.acmi`原生数据


项目基于 [wrpl-inspector v3](https://github.com/maxsupermanhd/wrpl-inspector)，保留了 Go 回放解析库和 dear-imgui 图形检查器，并提供无图形界面的导出器`cmd/acmi`。开发分支为 [dev](https://github.com/3794432/wrpl-acmi/tree/dev)；Go 模块路径仍为`github.com/maxsupermanhd/wrpl-inspector/v3`。

## 快速开始

### 下载

从[最新发布](https://github.com/3794432/wrpl-acmi/releases/latest)下载可执行文件:

* ["wrpl-acmi.exe"](https://github.com/3794432/wrpl-acmi/releases/latest/download/wrpl-acmi.exe)用于将回放转换为`.acmi` (必选)
* ["wrpl-terrain-installer.exe"](https://github.com/3794432/wrpl-acmi/releases/latest/download/wrpl-terrain-installer.exe)用于为 Tacview 安装地形 (可选)

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

含地形数据的安装器可直接运行：

```powershell
.\wrpl-acmi\wrpl-terrain-installer.exe
```

地形默认安装位置为`%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`, 安装器依赖 Windows PowerShell; 完整 69 张地图在默认分辨率下安装时, 建议预留至少 8 GB 空间, 安装后占用约 4 GB 空间. 安装后应完全退出并重启 Tacview, 再从原始`.wrpl`重新导出先前导出的回放, 以确保轨迹与地形使用相同坐标配置.

导出和安装默认使用 geographic 布局, 69 张地图中. 14 张采用经审核的 WGS84 估算位置, 55 张使用 Atlas 回退坐标. Atlas 坐标用于技术展示, 不代表真实地理位置, 显式选择 Atlas 时,安装器的`-atlas`必须与导出器的 `-terrain-layout atlas` 配套使用,`-alt`仅调整垂直偏移.

地形高度来自游戏内的 `levels/*.bin`, 边界, 水位等来自 [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine), 在地球上的经纬度定位来自 [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE) 的经纬度参考.

## ACMI内容与数据类型

通过`.wrpl`转换出的`.acmi`同时包含原始回放记录, 由记录推导的估算值, 以及静态配置资料. 阅读时应结合对象的来源字段来区分这三类信息. 默认输出 acmi 2.1.

| 内容 | 数据类型 |
| --- | --- |
| 空中与地面单位 | 使用回放中的位置, 飞行器保留姿态; 对移动地面单位的朝向进行推测, 销毁时先写入`Destroyed` 再移除; 符合条件的静态建筑可使用带时间戳的 ECS 构造位置
| 导弹与炸弹 | 优先使用实际位置, 导弹运动参数通过位置进行计算; 只有构造记录的炸弹仅显示投放位置, 并标记`Release position only`
| 干扰物 | 有可靠弹药证据时区分 Flare 和 Chaff, 无法确认时显示为 Chaff, 干扰物轨迹通过推测得出
| 机场与跑道 | 按地图, 任务和可核对的雷达记录进行配置, 目录覆盖 69 张地图, 457 个任务, 其中 67 张有可用跑道信息
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
* `wrpl-terrain-installer.exe` 将转换后的游戏地形安装到 Tacview, 普通构建不包含地形数据, 安装时需额外提供地形目录.

可将完整的转换后地形目录打包到安装器中: 

```powershell
.\build.ps1 -TerrainSource ".\converted-terrain"
```

从本机游戏提取和转换地形, 还需要 64 位 Python 3.12, Git, 转换器依赖, 及固定版本的 [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) 配置. 环境准备和完整命令见[地形工具说明](tools/terrain/README.zh-CN.md#从零构建); 构建参数, 断点续作, 和发布产物见[构建与发布指南](docs/standalone-build.zh-CN.md). 历史发布可能落后于 dev, 需要当前实现时应从该分支构建.

## 责任, 相关项目, 与许可

本项目尚在早期开发中, 运行本仓库编译及发布产物时自行对运行造成的后果负责. 仓库的[源码构建工作流](.github/workflows/source-build.yml)配置了导出器, 解析库及合成地形检查, 地形转换及其安装器依赖构建环境的游戏资产, Tacview 图形界面的加载和渲染也需要另行验证.

本项目基于 [wrpl-inspector](https://github.com/maxsupermanhd/wrpl-inspector) v3 开发, 保留了 Go 回放解析库和 dear-imgui 图形检查器, 并提供无图形界面的导出器`cmd/acmi`; Go 模块路径仍为`github.com/maxsupermanhd/wrpl-inspector/v3`.
本项目部分内容来自 [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) 和  [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE).

本项目及 [wrpl-inspector](https://github.com/maxsupermanhd/wrpl-inspector) 库按 [AGPL-3.0 license](https://github.com/3794432/wrpl-acmi#AGPL-3.0-1-ov-file) 发布. 上游版权与许可声明予以保留. 地图和游戏资产归 [Gaijin Entertainment](https://gaijinent.com/) 所有.

