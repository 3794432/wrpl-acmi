# 地面单位导出

[导出指南](acmi-export.zh-CN.md) · [静态对象](acmi-information.zh-CN.md#静态地面对象) · [防空参考范围](ACMI_AIR_DEFENCE_RANGES.zh-CN.md)

导出器默认读取地面批量位置同步，并在证据充分时关联型号、阵营和销毁记录。没有同步轨迹的建筑或工事，也可在满足静态类别及构造位置要求时导出。

```powershell
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi"
```

`-ground-units=false` 关闭批量地面同步路径；`-ground-models=false` 关闭型号识别及其派生显示，但不关闭独立的阵营识别。

## 位置、身份与生命周期

批量同步来自 `GroundModelPositions`（MPI `0xf073`）及服务器变体 `0xf074`。解析器通过每单位的有界历史缓存、RLE 解码和 XOR 更新恢复增量状态，读取实际位置。

地面轨迹使用三分量 `T=经度|纬度|高度`，不合成未解码的姿态。对象从第一个有效位置开始创建，并在已有对象之后分配 ACMI ID，以维持飞机、武器、干扰物及雷达引用关系。

缺少 ECS 元数据时，单位以回放中的地面 UID 保持独立身份，显示为 `Ground unit <uid>`，阵营为中立。`WT_UnitUID` 和 `WT_IdentitySource` 记录依据；UID 0 也是有效值。型号、阵营和实体 ID 均不通过距离猜测，销毁时刻仅在事件能够明确关联时采用。

ECS `transform` 的平移保存在 `TMatrix.V4`，截断或非有限坐标会被拒绝。尚未解码的旋转不用于生成姿态。

## 静态建筑与工事

没有同步轨迹时，明确属于静态类别的对象可使用带实际包时间的 ECS 构造位置。身份冲突、UID 回收、位置变化、非法坐标和任务占位物会被排除；已有同步轨迹优先。

对象名称本身不足以新增静态对象，也不能因为车辆没有轨迹就将其固定在地图上。具体字段及排除规则见[静态地面对象](acmi-information.zh-CN.md#静态地面对象)。

## 运动方向与销毁

移动单位可根据相邻记录位置推导地理航向，写入六分量 `T`，并标注 `WT_HeadingSource=PathDerivative`。它表示轨迹方向，不是实测车体或炮塔姿态；静态对象、单点轨迹及不可靠运动区间仍只输出位置，不跨越超过两秒的缺口。明确匹配到销毁记录时，先输出 `Event=Destroyed`，再删除对象；最后一个位置点本身不视为销毁。

## 型号识别

独立的 groundmeta ECS 解析器按实际 UID 关联 `unit__className`，再用 `unit_storage__*` 中 IdFieldSerializer 的字段 `0x6`（unit_name）交叉核对。出现型号冲突时，不应用该名称；缺少证据时继续保留通用身份。

部分模板含 ECS 哈希表未收录的浮点数据组件。独立解析器只对宽度确定、哈希未知的 POD 字段执行有界跳读，并保留原始数据。存储解析限制为 1 MiB，同时校验索引、尺寸及剩余数据；原有飞机和武器 ECS 路径继续采用严格行为。

`Name` 和 `CallSign` 使用源于游戏 `lang/units.csv` 的具体型号名。查找时移除 `tankModels/`、`structures/` 等路径前缀，完整内部名仍写入 `WT_GroundModel`，依据写入 `WT_GroundModelSource`。找不到具体显示名时保留内部型号，不以 AAA 或 Static 等通用标签替代。

明确的防空或设施证据可以细化对象类型。tank 存储格式本身不能证明装甲类别，因为无装甲反坦克炮也可能使用该格式。机场雷达保留 `AFF Radar`、`AFM Radar` 显示名，雪地变体相同。

### ItO 90M 名称匹配

Tacview 按 `Name` 查询型号数据库。历史使用环境曾将 ItO 90M 误匹配为 T-90M，因此对明确的 `fr_crotale_ng` 和 `sw_crotale_ng` 使用 `Name=Crotale NG`，同时在 `CallSign` 保留 ItO 90M。

此规则保留防空类型、原始型号、来源、UID 和阵营，不影响真正的 `ussr_t_90m_2020`，也不覆盖用户手工指定的不同名称。三维外形仍取决于 Tacview 的可用模型。

## 阵营关联

地面阵营依据 groundmeta 的 ECS team 与实际 UID 关联，映射与飞机一致：

| ECS team | ACMI 阵营与颜色 |
| --- | --- |
| 1 | `Coalition=Blue`、`Color=Blue` |
| 2 | `Coalition=Red`、`Color=Red` |

`WT_GroundTeam` 保留数值，`WT_GroundTeamSource` 保留 `ecs:team` 来源。未知值、缺少来源或 UID 身份冲突时不应用该证据，不根据国别、型号、位置或红蓝数量推测。

阵营识别不依赖型号是否识别成功，也不受 `-ground-models=false` 影响。原有的显式实体 team 备用路径仍可使用。

## 防空参考范围

完整表、取值顺序及显示规则见[防空静态范围](ACMI_AIR_DEFENCE_RANGES.zh-CN.md)。以下两个型号说明了为何必须区分武器配置、瞄准上限和雷达搜索距离。

| 型号 | 显示半径 | 固定来源中的依据 |
| --- | --- | --- |
| `uk_rooikat_za_35` | 3,000 米 | 两门 GA-35 所引用的 `35mm_ga_35_user_cannon.blkx` 中 `attackMaxDist=3000`，单位挂载的 `AttackShellMaxRadius=3000` 提供佐证。 |
| `ussr_zsu_23_4` | 2,500 米 | `23mm_2a7_user_cannon.blkx` 中 `attackMaxDist=2500`。单位配置另有 `AttackShellMaxRadius=3000`，此处采用武器层数值。 |

上述文件位于 Datamine 提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457`（2.59.0.74）的 `gamedata/weapons/groundmodels_weapons/`。选择武器层数值不表示已经证明游戏引擎中各字段的覆盖优先级。

`EngagementRange` 表示半径，`EngagementMode=1` 启用静态圈显示；`WT_EngagementRangeSource` 和 `WT_EngagementRangeMode=StaticModelReference` 记录出处与性质。瞄准上限、弹丸截止距离和雷达搜索距离不能代替该参考范围，缺少来源的型号也不会获得默认半径。

## 历史样例验证

既有实回放验证集中使用 `#2026.09.21 16.33.02.wrpl`，在 2 GiB Windows Job Object 限制下执行。不同功能迭代的结果如下：

| 检查内容 | 样例结果 |
| --- | --- |
| 型号识别 | 140 条已有地面轨迹全部关联到型号，共 18 种。 |
| 阵营关联 | 140 条地面轨迹均取得阵营，蓝方和红方各 70 个；954 个非地面对象保持一致。 |
| ItO 90M 名称 | 8 个 `fr_crotale_ng` 对象更新 `Name`，该次比较的其他 ACMI 字节保持一致。 |
| ZA-35 范围 | 12 个对象添加范围字段，蓝红各 6 个。 |
| ZSU-23-4V 范围 | 9 个对象添加范围字段。 |

相关单元测试和前后数据比较核对了轨迹、时间、生命周期及非目标数据。这些是历史样例结论；其他版本、载具类别和服务器分片的覆盖率仍需分别验证，Tacview 界面渲染也未由上述检查确认。
