# Atlas 与 legacy 地形布局

当前默认布局是 `-terrain-layout geographic`，不是 Atlas。地图目录共 69 张：14 张使用审核后的 WGS84 估算位置，55 张回退 Atlas 技术坐标。高程 XML 与飞机、导弹、炸弹、地面单位和干扰物共用同一份 GeoReferences。原理、筛选和回退原因见 [地理地形](TERRAIN_GEOGRAPHIC.md)。

## Atlas 技术布局

Atlas 把地图放到固定、互不重叠的技术坐标，便于在没有可用地理定位时并排检查地图，不代表真实地球位置。它需要显式和当前默认模式配套：

```
.\wrpl-acmi.exe -terrain-layout atlas -in "replay.wrpl" -out "replay-atlas.acmi"
.\tools\terrain\Install-All-Terrains.cmd -SourceRoot "<地形转换产物目录>" -AtlasOnly
```

Atlas 的 `MapId` 使用 `WarThunder.Atlas1.<id>`。导出器与安装器都必须选择 Atlas；只改一侧会造成轨迹与地形不匹配。安装器的 `-AtlasOnly` 跳过地理覆盖层，保留 69 项固定技术摆放。

## 手工 legacy 布局

`legacy` 是人工参考路径，仅用于已经自行配准地图的情况。以下数值只演示参数格式，坐标和中心点需要自行配准：

```
.\wrpl-acmi.exe -terrain-layout legacy -lat 30.0 -lon 31.0 -center-x 0 -center-z 0 -in "replay.wrpl"
.\tools\terrain\Install-All-Terrains.cmd -SourceRoot "<地形转换产物目录>" -LegacyCoordinates -Latitude 30.0 -Longitude 31.0 -CenterX 0 -CenterZ 0
```

只有 `legacy` 接受 `-lat`、`-lon`、`-center-x`、`-center-z` 等人工参数；默认 `geographic` 和 `atlas` 下会自动拒绝这些覆盖参数。人工参考点、安装器参数和地形 XML 必须属于同一套配准，工具不会替用户证明这组坐标正确。

## 安装地形

默认地理布局使用：

```
.\tools\terrain\Install-All-Terrains.cmd -SourceRoot "<地形转换产物目录>"
```

转换产物根目录应带 `GeoReferences.json`、地图元数据和对应高程 `.data`。安装器核对配置、文件尺寸和哈希后更新 Tacview 的自定义地形目录；地图 `.data` 会复用，但不会从源码仓库自动下载。安装完成后完全退出并重启 Tacview。

地图 `.data` 不纳入 Git，源码仓库不附带地形资产。安装器选项、目录结构和校验方法以 [tools/terrain/README.md](../tools/terrain/README.md) 为准。

## 旧 ACMI

当前版本放弃旧 ACMI 的坐标兼容保证。旧的 Atlas 或人工参考文件不能假定能直接匹配新地形；应使用同一份 WRPL 和新版导出器重新生成 ACMI。旧 `acmi-mapid` / `acmi-atlas` 迁移工具属于历史路径，当前不作为兼容性承诺。

## 来源与边界

地理定位来自 [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE) 固定 commit `c26c0069558079df6597a7bc147cb91391509f89` 的估算结果，仍有不确定性；已知错误的 Sinai、Grand Canyon 和 Israel 定位不采用。地图与游戏资源归 Gaijin Entertainment 所有，本项目与其无隶属或背书关系。
