# 高程与 ACMI 的经纬度同步

导出器默认使用 `-terrain-layout geographic`。地图定位来自
[WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE)，固定版本与逐图审核结果见
`internal/terrainmap/geographic_references.json`。发布包中的 `GeoReferences.json` 与其字节一致。

从仓库根目录构建并安装完整包：

```powershell
.\build.ps1 -WithTerrain -GameRoot "C:\Path\To\War Thunder" -DatamineRoot ".\War-Thunder-Datamine" -Python ".\.venv\Scripts\python.exe"
.\dist\wrpl-acmi\tools\terrain\Install-All-Terrains.cmd -SourceRoot .\dist\wrpl-acmi\terrain -AtlasReferences .\dist\wrpl-acmi\tools\terrain\AtlasReferences.json
```

**SourceRoot** 必须已经包含全部 69 张地图的 `maps/<id>/<id>.data`、对应 metadata 与零点 XML。
高程原始数据来自本机游戏 `levels/*.bin`；[War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine)
在固定提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457` 中提供地图边界、水位等配置，不能单独生成高程。
仓库中的 `tools/terrain/converter` 与 `tools/terrain/build-terrain.ps1` 负责提取和转换，根目录 `build.ps1 -WithTerrain`
把转换结果放入 `dist/wrpl-acmi/terrain`。SourceRoot 应指向该转换产物，不能指向 Datamine 克隆目录或原始 `.bin` 目录。
完整从零命令见[项目说明](../README.zh-CN.md)，输入来源与目录要求见[地形工具说明](../tools/terrain/README.zh-CN.md#输入来源与许可)。
这些本地地形输入不在 Git 中。安装后完全退出并重启 Tacview，再用新版 **wrpl-acmi.exe** 或
**run.bat** 导出 WRPL。没有旧 ACMI 兼容保证；已有文件应重新导出。

## 同步规则

启用的定位使用 WGS84 水平经纬度与 `WarThunder.Geo1.<地图ID>`。
高程 XML 的四个角和所有飞机、导弹、炸弹、地面单位、干扰物的经纬度都应用同一个变换：

```
u = (x - localMinX) / (localMaxX - localMinX)
v = (z - localMinZ) / (localMaxZ - localMinZ)
longitude = minLongitude + u * (maxLongitude - minLongitude)
latitude  = minLatitude  + v * (maxLatitude  - minLatitude)
```

`local_bounds` 来自地图世界坐标范围，不能用小地图纹理的 `textureBounds` 替代。
高程角点使用实际高度采样边界 `sample_bounds_m`，不能把像素中心当作地图最外缘。
地图以北朝上，超出边界的位置沿相同变换外推，不钳制轨迹。
此变换包含各地图的水平比例；不是只把经纬度原点移到城市中心。

全局 `ReferenceLongitude/Latitude/Altitude` 仍为零，`T` 已包含绝对位置，避免重复偏移。
高度、姿态、原生 U/V、速度、加速度、生命周期、锁定与武器事件仍为回放原数据。
没有从 DEM 匹配推导海拔基准或垂直修正；高程仍为游戏高度。
地理拟合后的水平距离可能与游戏米制距离有差别，不能将其作为真实飞行测量。

ACMI 记录 `WT_GeoreferenceConfigSHA256` 和上游 commit；安装清单记录同一配置校验值。
这能用于确认 EXE 和已安装地形是否使用同一版映射。配置在编译时内嵌，修改 JSON 后须重编译
并重新安装地形，不能只改其中一份文件。

## 定位筛选与回退

仅采用通过审核的 high/medium 定位；已知错误、低置信、未解算、范围不匹配或与最终地形重叠的地图
自动使用 Atlas 技术坐标，并写入 `WT_GeoreferenceStatus=AtlasFallback` 和具体原因。
Atlas 经纬度不是该地图的现实地理位置。这是定位质量回退，不是旧回放兼容模式。

上游估计仍有不确定性，启用地图会标记 `WT_GeoreferenceStatus=Estimated`。
已知错误的 Grand Canyon → Greenland 结果不会被采用。
审核清单、禁用原因与原始证据随地形发布包提供。

安装器直接更新每张地图的 XML，不同时保留该地图的旧 Atlas 地形。
地图数据 `.data` 复用原高程；这里不需要重新提取全部高度，也不包含地表纹理。
`-terrain-layout atlas` 可显式使用纯技术坐标，配套安装器的 `-AtlasOnly` 用于同一摆放方式。
`-terrain-layout legacy` 才允许手动 `-lat/-lon/-center-x/-center-z`，手动位置需另行配准高程。
