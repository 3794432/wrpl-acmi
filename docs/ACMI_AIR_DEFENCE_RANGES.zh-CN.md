# 防空单位的静态参考范围

[English](ACMI_AIR_DEFENCE_RANGES.md) · [地面单位](ACMI_GROUND_UNITS.md) · [导出指南](acmi-export.zh-CN.md)

导出器使用内嵌的 `cmd/acmi/ground_threats.json` 为防空单位提供静态范围，不依赖 Tacview 型号数据库补充射程。范围反映型号配置或明确标注的显示设置，不能用于判断某一时刻是否能够命中目标。

表中条目按游戏内部型号的基本名称与明确的地面 UID 关联，匹配时忽略大小写；名称相似、国别和位置均不能代替型号证据。

## 来源与覆盖

数据固定到 War Thunder Datamine 提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457`，对应版本 `2.59.0.74`。表中共 255 个型号，主要来自 `unittags.blkx` 的 `type_spaa` 条目，并补充 `gamedata/units/air_defence` 下的固定及 AI 单位。

仅有雷达、火控或探照灯的支援单位可以保留在来源表中，但不生成武器范围圈。

## 范围如何确定

火炮和导弹分别按字段含义选择范围，不在含义不同的参数之间直接取最大值。

| 类别 | 取值顺序 |
| --- | --- |
| 火炮 | 武器配置的 `attackMaxDist`，其次为同一挂载的 `AttackShellMaxRadius`、`AttackMaxRadius`。 |
| 火炮的低置信度参考 | 上述字段均缺失时，保留武器 `aimMaxDist`，并标为 `StaticAimLimitReference`，表示瞄准参考而非交战上限。 |
| 导弹 | 从 `bullet.rocket` 读取 `rangeMax`，缺失时依次考虑制导控制距离、同一挂载的 `AttackMaxRadius`、发射器 `attackMaxDist`。 |

弹体飞行极限、雷达搜索距离和 `guaranteedRange` 不用于替代交战上限。同类武器的不同配置保留有来源的最大静态能力，每类最多输出一个主范围。

火炮有多种挂载时，先选择主防空炮口径，再在同口径候选中选择有来源的范围。烟雾、诱饵、假武器和无制导火箭不按防空炮处理；SAM 车辆的低口径自卫机枪不额外生成防空炮圈。

## 哪些单位不显示范围圈

只有来源确认未装备搜索或跟踪雷达，且没有有效导弹范围的纯炮或机枪配置，才隐藏范围圈。当前表中有 142 个型号符合该条件，其型号、轨迹和阵营仍照常保留。

光学及红外传感器不视为雷达；装备情况未知时，也不推定为无雷达。有雷达的炮车，以及具有有效导弹范围的 SAM 或弹炮混合系统继续显示范围，因此无雷达的红外或光学制导导弹系统不会仅因缺少雷达而被排除。

上述判断针对静态装备配置，与回放中的雷达开关状态无关。

## ZSU-37-2 的显示设置

`ussr_zsu_37_2` 使用项目明确设置的 2,000 米显示半径，输出 `EngagementRange=2000` 和 `EngagementMode=1`。`WT_EngagementRangeBasis` 标明这是显示设置，并非原始武器 `attackMaxDist`。

`WT_EngagementRangeSource` 同时保留“User-configured ZSU-37-2 display radius: 2000 m”和固定来源中 `37mm_2a11_user_cannon.blkx#/attackMaxDist=4000 m` 的记录。阅读时应区分当前显示半径与上游配置原值。

## ACMI 字段与开关

主范围写入 `EngagementRange`，单位为米、含义为半径；`EngagementMode=1` 用于启用静态圈显示。配套的 `WT_EngagementRangeSource`、`Mode`、`Kind` 和 `Basis` 字段说明参数出处、武器类别及是否属于瞄准参考。

混合系统将最大范围放在第一圈。第二类武器保存在兼容字段 `EngagementRange2`、`EngagementMode2` 及相应来源字段中。既有文档依据对第一圈支持较明确，第二圈见于旧版格式资料；尚未验证 Tacview 界面能否同时绘制两圈，因此只能确认次级范围数据已导出。

`-ground-models=false` 关闭型号及派生范围显示，阵营识别仍独立运行；`-ground-units=false` 关闭批量地面轨迹路径。普通飞机、非防空车辆、导弹轨迹、雷达指定和干扰物不使用此范围表。

## 验证范围

既有 Go 测试覆盖混合系统、缺少来源、非法数值、雷达支援单位排除、大小写匹配，以及型号和空地身份隔离。

历史实回放验证使用 `#2026.09.21 16.33.02.wrpl`，在 2 GiB Windows Job Object 限制下完成转换。前后比较核对了对象 ID、位置、时间、生命周期、名称、阵营和非地面数据，仅允许已有地面对象的范围字段及有证据的防空分类变化。

这些结果仅适用于已检查的样例，不保证静态资料与所有回放版本一致，也不构成 Tacview 图形界面验收。实际装弹、地形遮挡、目标运动及制导效果均不由此表确定。
