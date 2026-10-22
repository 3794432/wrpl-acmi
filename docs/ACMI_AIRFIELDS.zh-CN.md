# 机场定位与跑道

[English](ACMI_AIRFIELDS.md) · [逐地图覆盖情况](ACMI_AIRFIELDS_COVERAGE.zh-CN.md)

默认启用 `-airfields=true`，按回放的地图、关卡路径和任务名选择跑道。导航标记追加在物理对象之后，保留原有 ID、轨迹和关联。机场与地面单位可分别开启、关闭。雷达仍显示为 `AFF Radar`、`AFM Radar`，雪地版本相同；完整型号保留在 `WT_GroundModel`。

目录为现有 69 张地形地图逐一记录覆盖状态。覆盖表区分已取得源定义的跑道和未解决的地图；存在目录条目不代表一定有跑道。缺失信息不使用通用跑道填补。

来源为 [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) 固定提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457`。地图只由根任务确定，导入模板中的占位 level 不作为地图依据。任务的 object-group 放置矩阵，变换预制资产明确的 `airfield.start`、`end`、`width`；同时收集任务内的 `addAirfield` 定义。不从文件名猜尺寸：例如名称含 3000 的 `dynaf_pg_1line_3000_universal` 实际定义长 3200 米、宽 120 米。

无条件配置可直接导出。带等级或其他条件的布局，必须由同阵营真实主雷达和两个飞行雷达的明确身份，在水平 2 米内与源坐标唯一匹配。实体回收、角色重复、位置冲突均会拒绝。重复候选中的相同几何只导出一次，有歧义的几何不导出。没有可靠生效状态依据的条件触发式跑道不选择。未知布局仍可保留有回放证据的设施锚点。

每条跑道使用原生 `Ground+Static+Aerodrome` 和源尺寸，加两个 `Navaid+Static+Waypoint` 端点，通过 `Parent`、`Next` 关联。根任务中明确命名的跑道起止区域也可提供轴线；源数据没有宽度时省略 `Width`，标记 `WidthUnknown`，不补造数值。航向是几何轴线，不代表当前降落方向；飞行雷达对不是跑道两端。所有标记使用与轨迹和高程一致的 `terrainPosition` 变换，沿用 Geographic/Atlas 选择。

有匹配雷达时优先使用其平均离海高度，否则可使用从游戏米制高程栅格采样的高度，两者均标明是机场平面近似。两者都不可用时，源矩阵的名义端点高度明确标为 `NominalTransformedEndpointY;NotGameGround`。`WT_Airfield*` 保留来源提交与路径、任务组、预制资产、雷达 UID、坐标和高度来源。雷达被摧毁不移除静态导航标记。这些标记是额外 ACMI 对象，不列入物理 tracks CSV。

正常 Go 构建直接嵌入已提交的 `cmd/acmi/data/airfields_all.json`，不需要联网。可用 Python 标准库重建：

```powershell
python tools/airfields/build_catalog.py
python tools/airfields/build_catalog.py --source-root C:/War-Thunder-Datamine
python tools/airfields/build_catalog.py --source-root C:/War-Thunder-Datamine --terrain-root ./dist/wrpl-acmi/terrain
```

本地来源需要包含固定版本的任务和 object-group 文件。`--cache-dir` 保留有大小限制的下载缓存。可选 `--terrain-root` 接受转换器生成的 `maps/<id>/metadata.json` 和有符号 `<id>.data`，采样前校验尺寸、边界、行方向和 SHA-256，只读取少量高程点。`build_reviewed_falklands.py` 可重建较小的 Falklands 已审核后备配置。

仍使用 ACMI 2.1，不修改地形安装流程。自动来源、几何检查及受控导出验证与 Tacview 界面验证分别记录。
