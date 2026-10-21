# Terrain installation / 地形安装

The source repository contains installation tools and coordinate metadata, not War Thunder terrain assets.
本源码仓库仅包含安装器与坐标元数据，不包含游戏高程资产。准备已从自己的游戏资产转换的目录，
其中必须有 69 个 `maps/<id>/<id>.data`、`metadata.json` 和零点 `CustomHeightmapList.xml`。
不能直接把游戏 `levels/*.bin` 当作 `.data`。

Run from the repository root / 从仓库根目录执行：

```powershell
Copy-Item .\tools\terrain\GeoReferences.json "D:\converted-war-thunder-terrain\GeoReferences.json"
.\tools\terrain\Install-All-Terrains.cmd -SourceRoot "D:\converted-war-thunder-terrain" -AtlasReferences .\tools\terrain\AtlasReferences.json
```

Default placement is geographic: 14 reviewed estimated WGS84 placements and 55 Atlas fallbacks.
默认使用经纬度同步：14 张地图采用审核后的估算地理定位，55 张使用技术坐标；不保证旧 ACMI 兼容。
The installer updates the 69 XMLs in place, preserves raster bytes and validates hashes and non-overlap.
安装器更新原位置的 69 张地图配置，复用高程数据，并校验哈希与范围不重叠。安装后重启 Tacview 并重新导出。

Use `-AtlasOnly` with exporter `-terrain-layout atlas` only when deliberately choosing technical coordinates.
For hand placement use installer `-LegacyCoordinates` and exporter `-terrain-layout legacy` with matching parameters.
Details: [TERRAIN_GEOGRAPHIC.md](../../docs/TERRAIN_GEOGRAPHIC.md), [coverage](georeference/coverage.json).

Source-only installer tests / 不读取真实游戏资产的安装测试：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\test-install-all-terrains.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\geographic\New-Fixture.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tools\terrain\smalltests\geographic\test-install-geographic-terrains.ps1
```

Upstream geographic evidence: WT-MAP-ANALYZE commit c26c0069558079df6597a7bc147cb91391509f89.
Coordinate fit is estimated, not replay GPS; altitude remains game height. Attribution is in georeference/.
