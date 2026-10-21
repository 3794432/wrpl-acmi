# Terrain installation / 地形安装

The source repository contains installation tools and coordinate metadata, not War Thunder terrain assets.
本源码仓库仅包含安装器与坐标元数据，不包含游戏高程资产。准备已从自己的游戏资产转换的目录，
其中必须有 69 个 `maps/<id>/<id>.data`、`metadata.json` 和零点 `CustomHeightmapList.xml`。
不能直接把游戏 `levels/*.bin` 当作 `.data`。

## Input sources / 输入来源

| Input / 输入 | Source / 来源 | Purpose / 用途 |
| --- | --- | --- |
| Compiled terrain / 编译后地形 | Your installed game's `levels/<id>.bin` / 本机游戏文件 | Actual terrain heights and geometry / 高程与地形几何 |
| Level configuration / 关卡配置 | [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine), e.g. `aces.vromfs.bin_u/levels/<id>.blkx` | Bounds (`mapCoord0`, `mapCoord1`), water level and related settings / 地图边界、水位等 |
| Geographic placement / 地理定位 | Reviewed [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE) estimates in `GeoReferences.json` | Place both terrain and replay tracks / 同步摆放高程与回放 |

For example, the pinned [Sinai configuration](https://github.com/gszabi99/War-Thunder-Datamine/blob/f7952e4291e9fd7b6fe38e84020dddef925c3457/aces.vromfs.bin_u/levels/avg_egypt_sinai.blkx)
provides `mapCoord0`, `mapCoord1` and `water_level`; it is not the compiled `avg_egypt_sinai.bin` or a Tacview height raster.
例如，上述西奈 `.blkx` 提供边界和水位，不是游戏编译后的地形文件，也不是 Tacview 高程栅格。
Use configuration compatible with your game assets; a changing Datamine checkout does not identify the version of your local `.bin` files.
配置应与游戏资产版本匹配，Datamine 的当前版本不能替代本地地形文件的版本核对。

**Datamine alone cannot build this terrain package.** Cloning it does not provide the compiled terrain required here.
The current Git repository publishes installation scripts and coordinate tables, but not the complete `.bin` decoder/rasterizer
used to create the local terrain package. There is currently no from-scratch terrain build command in this repository.
`go build ./cmd/acmi` builds the replay exporter only.

**不能仅从 Datamine 构建此地形包。** 当前 Git 仓库发布的是安装器和坐标表，尚未发布生成本地地形包所用的完整
`.bin` 解码与栅格化工具，因而没有可直接执行的从零高程构建命令；`go build ./cmd/acmi` 只构建回放导出器。
从游戏文件生成高程需要另有提取/转换工具，将实际高度图或地形网格写成下述安装格式。

## Converted package / 转换产物

`-SourceRoot` is the converted output directory, **not** the Datamine checkout, a game installation directory, or a directory of raw `.bin` files.
`-SourceRoot` 指向转换后的目录，不能直接使用 Datamine 克隆目录、游戏安装目录或原始 `.bin` 目录。

```text
converted-war-thunder-terrain/
  GeoReferences.json
  maps/
    <map-id>/                       # all 69 supported maps / 全部 69 张支持的地图
      <map-id>.data
      metadata.json
      CustomHeightmapList.xml
```

`.data` is a signed little-endian 16-bit raster in metres, ordered north-to-south and west-to-east.
`metadata.json` records its dimensions, sample bounds and SHA-256; the source XML is the zero-origin template expected by the installer.
Renaming a `.bin` or `.blkx` to `.data` does not convert it. Synthetic `smalltests` fixtures test installation only and are not playable terrain.

`.data` 为米制、16 位有符号小端高程，按从北到南、从西到东排列；metadata 记录尺寸、采样边界及 SHA-256，
源 XML 使用安装器要求的零点模板。改扩展名不能完成转换；`smalltests` 中的合成数据只用于安装测试，不是真实地图。

## Install converted terrain / 安装已有高程包

Run from the repository root / 从仓库根目录执行：

```powershell
Copy-Item .\tools\terrain\GeoReferences.json "D:\converted-war-thunder-terrain\GeoReferences.json"
.\tools\terrain\Install-All-Terrains.cmd -SourceRoot "D:\converted-war-thunder-terrain" -AtlasReferences .\tools\terrain\AtlasReferences.json
```

Default placement is geographic: 14 reviewed estimated WGS84 placements and 55 Atlas fallbacks.
默认使用经纬度同步：14 张地图采用审核后的估算地理定位，55 张使用技术坐标；不保证旧 ACMI 兼容。
The installer updates the 69 XMLs in place, preserves raster bytes and validates hashes and non-overlap.
安装器更新原位置的 69 张地图配置，复用高程数据，并校验哈希与范围不重叠。安装后重启 Tacview 并重新导出。
These commands install existing rasters; they do not extract heights from the game or Datamine.
上述命令安装已有高程，不从游戏或 Datamine 提取地形。

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
