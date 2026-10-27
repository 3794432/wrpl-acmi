# 机场定位与跑道

[English](ACMI_AIRFIELDS.md) · [逐地图覆盖](ACMI_AIRFIELDS_COVERAGE.zh-CN.md) · [导出指南](acmi-export.zh-CN.md)

机场导出默认开启，可通过 `-airfields=false` 关闭。导出器根据回放的地图、关卡路径和任务名称选择跑道，再结合必要的回放证据核对条件布局。

机场标记是附加的静态导航对象，独立于地面单位开关。它们在物理对象之后分配 ID，不改变原有轨迹或关联，也不列入物理轨迹 CSV。

## 数据来源与覆盖

机场配置来自 [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine) 固定提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457`。目录为 71 张地形地图逐一记录覆盖状态；目录中存在地图条目，并不表示该地图一定有可导出的跑道。

地图身份由根任务确定，导入模板中的占位 level 不作为依据。跑道几何通过任务 object-group 的放置矩阵，变换预制资产中明确的 `airfield.start`、`end` 和 `width` 得到，同时收集任务内的 `addAirfield` 定义。

尺寸依据源字段，不从文件名推测。例如，`dynaf_pg_1line_3000_universal` 虽然名称含 3000，实际定义仍为长 3200 米、宽 120 米。缺失的源信息不会用通用跑道补齐。

## 布局选择

无条件配置可直接导出。带等级或其他条件的布局，则必须由同阵营的一个主雷达和两个飞行雷达提供明确身份，并在水平 2 米容差内与源坐标唯一匹配。

实体回收、角色重复或位置冲突会导致候选被拒绝。相同几何只导出一次；存在歧义，或条件触发式跑道缺少可靠生效依据时，不选择该跑道。即使布局尚未确定，仍可保留有回放证据的设施锚点。

机场雷达显示名称保留游戏缩写 `AFF Radar` 和 `AFM Radar`，分别对应飞行雷达与主雷达，雪地变体使用相同名称；完整内部型号保存在 `WT_GroundModel`。

## 几何与 ACMI 表示

每条跑道使用 `Ground+Static+Aerodrome` 类型及源尺寸，并以两个 `Navaid+Static+Waypoint` 标记端点，通过 `Parent` 和 `Next` 关联。

根任务中明确命名的起止区域也可提供跑道轴线。源数据没有宽度时，省略 `Width` 并标记 `WidthUnknown`。跑道航向表示几何轴线，不代表当前降落方向；一对飞行雷达的位置也不能直接当作跑道端点。

所有标记都使用与轨迹、高程一致的 `terrainPosition` 变换，遵循 geographic 或 Atlas 布局。

## 高度与来源字段

高度按以下顺序选择，并记录采用的依据：

1. 有匹配雷达时，优先使用雷达的平均离海高度。
2. 否则使用经过校验的游戏米制高程栅格采样结果。
3. 两者均不可用时，使用源矩阵变换后的名义端点高度，并标记 `NominalTransformedEndpointY;NotGameGround`。

前两种方式也只是机场平面高度的近似。`WT_Airfield*` 字段保留来源提交、文件路径、任务组、预制资产、雷达 UID，以及位置和高度来源。雷达被摧毁后，静态导航标记不会随之移除。

## 重建配置目录

正常 Go 构建直接嵌入已提交的 `cmd/acmi/data/airfields_all.json`，不需要联网。需要更新目录时，可在仓库根目录使用 Python 标准库运行：

```powershell
python tools/airfields/build_catalog.py
python tools/airfields/build_catalog.py --source-root C:/War-Thunder-Datamine
python tools/airfields/build_catalog.py --source-root C:/War-Thunder-Datamine --terrain-root ./dist/wrpl-acmi/terrain
```

本地来源应包含固定版本的任务和 object-group 文件；仅用于地形转换的 levels 稀疏检出不足以重建机场目录。`--cache-dir` 可保留受大小限制的下载缓存。

可选的 `--terrain-root` 接受转换器生成的 `maps/<id>/metadata.json` 和有符号高程 `<id>.data`。采样前会校验尺寸、边界、行方向和 SHA-256，之后只读取少量高程点。`build_reviewed_falklands.py` 用于重建较小的 Falklands 已审核后备配置。

机场字段兼容 ACMI 2.1，不要求修改地形安装流程。来源检查、几何测试和受控导出验证不能替代 Tacview 界面的实际显示验证。
