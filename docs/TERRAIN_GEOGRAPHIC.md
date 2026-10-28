# 地形与回放对象的地理坐标

[地形工具](../tools/terrain/README.zh-CN.md) · [Atlas 布局](terrain-atlas.md) · [导出指南](acmi-export.zh-CN.md)

导出器默认使用 `-terrain-layout geographic`，将游戏本地坐标映射到经审核的地理位置。地形高程和回放对象必须使用相同的映射配置，才能在 Tacview 中对齐。

定位资料来自 [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE)，固定版本及审核结果保存在 `internal/terrainmap/geographic_references.json`，并与发布使用的 `GeoReferences.json` 保持一致。

## 构建与安装

在仓库根目录构建独立安装器后，指定本机游戏目录运行；也可将 EXE 放到游戏根目录后直接双击：

```powershell
.\build.ps1
.\dist\wrpl-acmi\wrpl-terrain-installer.exe -game "C:\Path\To\War Thunder"
```

完整环境准备见[从零构建](../tools/terrain/README.zh-CN.md#从零构建)。安装器内置转换运行环境，从本机 `levels/*.bin` 读取地形，从本机 `aces.vromfs.bin` 读取边界和水位，不附带预生成高程，也不需要用户安装 Python 或准备 Datamine。构建阶段只下载固定哈希的开源运行库；运行阶段离线转换。

若使用脚本安装入口，`-SourceRoot` 必须指向包含全部 71 张地图的转换目录，其中应有 `maps/<id>/<id>.data`、元数据和零点 XML，不能指向 Datamine 或原始关卡目录：

```powershell
.\dist\wrpl-acmi\tools\terrain\Install-All-Terrains.cmd -SourceRoot .\dist\wrpl-acmi\terrain -AtlasReferences .\dist\wrpl-acmi\tools\terrain\AtlasReferences.json
```

安装后应完全退出并重启 Tacview。已有 ACMI 需要匹配当前地形时，应使用同版导出器从原始 WRPL 重新导出。

## 坐标变换

启用地理定位的地图使用 WGS84 水平经纬度和 `WarThunder.Geo1.<地图ID>` 标识。高程 XML 的角点及飞机、导弹、炸弹、地面单位、干扰物均遵循同一变换：

```text
u = (x - localMinX) / (localMaxX - localMinX)
v = (z - localMinZ) / (localMaxZ - localMinZ)
longitude = minLongitude + u * (maxLongitude - minLongitude)
latitude  = minLatitude  + v * (maxLatitude  - minLatitude)
```

`local_bounds` 使用地图世界坐标范围，不能替换为小地图纹理的 `textureBounds`。高程角点采用实际采样边界 `sample_bounds_m`，不能把像素中心误作地图外缘。

地图以北朝上，边界外的位置按同一变换外推，不截断轨迹。变换包含每张地图的水平比例，因此并非简单移动经纬度原点。地理拟合后的水平距离可能与游戏米制距离不同，不宜作为真实飞行测量依据。

全局 `ReferenceLongitude`、`ReferenceLatitude` 和 `ReferenceAltitude` 保持为零，`T` 已包含绝对位置，避免重复偏移。坐标布局不会重新推导姿态、生命周期或事件；运动估算仍按其各自的数据规则处理。高度沿用游戏基准，没有通过 DEM 匹配求取海拔基准或垂直修正。`-alt` 可额外增加以米为单位的高度偏移。

## 定位筛选与 Atlas 回退

参考集包含 71 张地图，其中 14 张采用已审核的地理估算位置（5 张为 high、9 张为 medium），57 张使用 Atlas 回退。已知错误、低置信、未解算、范围不匹配或与最终地形重叠的结果均被排除。

采用地理估算的地图写入 `WT_GeoreferenceStatus=Estimated`。回退地图则写入 `WT_GeoreferenceStatus=AtlasFallback` 及原因。Atlas 坐标用于技术展示，不代表真实地理位置，也不是针对旧回放的兼容模式。

Sinai、Grand Canyon 和 Israel 的已知错误定位不予采用，其中包括将 Grand Canyon 定位到 Greenland 的结果。审核清单、禁用原因及来源证据随地形资料提供；启用的估算位置仍具有不确定性。

## 配置一致性与更新

ACMI 记录 `WT_GeoreferenceConfigSHA256` 和上游提交，安装清单记录同一配置的校验值，可据此核对导出器与地形是否使用同版映射。

配置在编译时内嵌。修改 JSON 后，需要重新编译导出器并重新安装地形，不能只更新其中一份文件。转换数据、导出器配置和安装器元数据应始终使用完整且一致的 71 地图参考集。

安装器更新每张地图的 XML，不同时保留该地图旧的 Atlas 定义。高程 `.data` 可复用，无需仅为更新水平位置而重新提取高度；该过程也不包含地表纹理。

需要统一使用技术坐标时，导出器选择 `-terrain-layout atlas`，独立安装器选择 `-atlas`；使用旧脚本入口时，对应参数为 `-AtlasOnly`。完整示例见 [Atlas 布局](terrain-atlas.md)。
