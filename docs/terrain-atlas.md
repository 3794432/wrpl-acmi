# Atlas 地形布局

[地理坐标](TERRAIN_GEOGRAPHIC.md) · [地形工具](../tools/terrain/README.zh-CN.md)

Atlas 将地图放置在固定、互不重叠的技术坐标上，便于检查缺少可靠地理定位的地图。这些经纬度不表示地图在现实世界中的位置。

默认 geographic 布局已包含 Atlas 回退：71 张地图中，14 张使用已审核的 WGS84 估算位置，57 张回退到 Atlas。显式选择 `atlas` 时，所有地图统一采用技术布局。

## 同时设置导出器与安装器

导出器与地形安装器必须使用相同布局。独立安装器的示例如下：

```powershell
.\wrpl-terrain-installer.exe -source "C:\Terrain\converted-terrain" -atlas
.\wrpl-acmi.exe -terrain-layout atlas -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay-atlas.acmi"
```

安装器已经内嵌地形数据时，可以省略 `-source`。Atlas 的地图标识使用 `WarThunder.Atlas1.<id>`。仅修改导出器或安装器一侧，会导致轨迹与地形不匹配。

使用源码中的脚本入口时，对应选项为 `-AtlasOnly`，而不是独立 EXE 的 `-atlas`：

```powershell
.\tools\terrain\Install-All-Terrains.cmd -SourceRoot "C:\Terrain\converted-terrain" -AtlasOnly
```

该脚本选项跳过地理覆盖层，保留 71 项固定技术位置。`-alt` 仍只调整高度，不改变水平布局。

## 恢复默认地理布局

重新安装时省略 Atlas 选项，并按默认布局重新导出：

```powershell
.\wrpl-terrain-installer.exe -source "C:\Terrain\converted-terrain"
.\wrpl-acmi.exe -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay.acmi"
```

转换目录应包含 `GeoReferences.json`、地图元数据和对应高程 `.data`。安装器核对配置、尺寸及哈希后更新 Tacview 自定义地形。高程文件可以复用，但不会自动从源码仓库下载。

安装后应完全退出并重启 Tacview。已有 ACMI 需要匹配新布局时，应从原始 WRPL 重新导出。

## 来源与限制

源码仓库不包含游戏高程资产。目录结构、转换步骤及校验方法见[地形工具说明](../tools/terrain/README.zh-CN.md)。

地理估算来自 [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE) 提交 `c26c0069558079df6597a7bc147cb91391509f89`。已知错误的 Sinai、Grand Canyon 和 Israel 定位不采用，其余估算也仍有不确定性。

地图和游戏资源归 Gaijin Entertainment 所有，本项目与其无隶属或背书关系。
