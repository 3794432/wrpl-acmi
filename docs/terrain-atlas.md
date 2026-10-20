# Atlas 地形与旧回放

默认 Atlas V1 将 68 张已知地图放在固定、互不重叠的技术坐标。导出器的内嵌表 `internal/terrainmap/atlas_references.json` 与安装器的 `tools/terrain/AtlasReferences.json` 必须一致，不能单独改动。它们不表示真实地理位置。

## 安装已有高程包

本源码仓库不包含游戏地图资产或约 2.1 GiB 的 `.data` 文件。以下命令要求已经有完整的转换产物目录，其中 `maps/<id>/` 带高程 `.data`、metadata、result 和 XML 模板：

```powershell
./tools/terrain/Install-All-Terrains.cmd -SourceRoot ./war-thunder-air-terrain -AtlasReferences ./tools/terrain/AtlasReferences.json
```

安装器会先核对全部源文件、SHA256、尺寸和边界，更新 `%APPDATA%/Tacview/Data/Terrain/Custom/WarThunderMaps`，并复用相同数据。旧 XML 在更新前保存为 `.before-atlas.bak`，不会被作为额外 XML 加载。完成后完全退出并重启 Tacview 一次。安装后无需逐回放替换配置，也不依赖 Auto Terrain Switch 才能避免叠层。

默认安装 XML 使用 `WarThunder.Atlas1.<id>` 和 `Visible=true`。手动参考坐标只用于 `-LegacyCoordinates` 模式；导出器相应使用 `-terrain-layout legacy`，参数必须匹配。默认模式中传入人工参考参数会报错，避免静默配错。

## 旧 ACMI

多数本导出器的旧文件使用 T6，未保存原始 U/V，建议用新版重新导出 WRPL。仅补 MapId 不会迁移轨迹位置。

```powershell
go build -o ./acmi-mapid.exe ./cmd/acmi-mapid
go build -o ./acmi-atlas.exe ./cmd/acmi-atlas
./acmi-atlas.exe -in ./old-replays
```

`acmi-mapid` 只补地图标识，适合旧布局；`acmi-atlas` 只转换本导出器且含原生 U/V 的文本 T5/T8 文件。迁移默认另存 `.atlas.acmi`，原文件不变，缺失 U/V 或压缩文件会报错。不要对 T6 文件猜测原始参考点。

## 验证

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File ./tools/terrain/smalltests/test-install-all-terrains.ps1
go test ./cmd/acmi ./cmd/acmi-mapid ./cmd/acmi-atlas ./internal/terrainmap
```

测试检查参考参数、68 个固定 ID、边界、安装备份和数据复用；它们不等同于在 Tacview 图形界面逐图加载验证。
