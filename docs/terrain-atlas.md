# Atlas 地形布局

当前默认布局是 `-terrain-layout geographic`；`atlas` 是当前的技术布局选项。权威地图目录恰好包含 69 张地图：14 张使用审核后的 WGS84 估算位置，55 张使用 Atlas 回退。高程 XML 与飞机、导弹、炸弹、地面单位和干扰物共用同一份 GeoReferences。原理、筛选和回退原因见 [地理地形](TERRAIN_GEOGRAPHIC.md)。

## Atlas 技术布局

Atlas 把地图放到固定、互不重叠的技术坐标，便于在没有可用地理定位时并排检查地图，不代表真实地球位置。选择该布局时，导出器和安装器必须配套使用：

```
.\wrpl-acmi.exe -terrain-layout atlas -in "C:\Replays\replay.wrpl" -out "C:\Replays\replay-atlas.acmi"
.\tools\terrain\Install-All-Terrains.cmd -SourceRoot "<地形转换产物目录>" -AtlasOnly
```

Atlas 的 `MapId` 使用 `WarThunder.Atlas1.<id>`。导出器与安装器都必须选择 Atlas；只改一侧会造成轨迹与地形不匹配。安装器的 `-AtlasOnly` 跳过地理覆盖层，保留 69 项固定技术摆放。
`-alt` 仍可作为以米为单位的垂直高度偏移；水平定位始终由所选布局决定。

## 安装地形

默认地理布局使用：

```
.\tools\terrain\Install-All-Terrains.cmd -SourceRoot "<地形转换产物目录>"
```

转换产物根目录应带 `GeoReferences.json`、地图元数据和对应高程 `.data`。安装器核对配置、文件尺寸和哈希后更新 Tacview 的自定义地形目录；地图 `.data` 会复用，但不会从源码仓库自动下载。安装完成后完全退出并重启 Tacview。

地图 `.data` 不纳入 Git，源码仓库不附带地形资产。安装器选项、目录结构和校验方法以 [地形工具说明](../tools/terrain/README.zh-CN.md) 为准。

## 来源与边界

地理定位来自 [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE) 固定 commit `c26c0069558079df6597a7bc147cb91391509f89` 的估算结果，仍有不确定性；已知错误的 Sinai、Grand Canyon 和 Israel 定位不采用。地图与游戏资源归 Gaijin Entertainment 所有，本项目与其无隶属或背书关系。
