# SPAA / SPAAM 静态威胁范围

导出器内嵌 `cmd/acmi/ground_threats.json`，不依赖 Tacview 自带型号数据库给这些单位补射程。表中每条记录以游戏内部型号 basename 关联回放的明确地面 UID，匹配时统一大小写；不按名称相似、国别或位置猜测型号。

数据固定到 War Thunder Datamine `f7952e4291e9fd7b6fe38e84020dddef925c3457`（`2.59.0.74`）。清单取自 `unittags.blkx` 的全部 193 个 `type_spaa` 条目，另有 `gamedata/units/air_defence` 下 62 个固定/AI 条目。只有雷达、火控或探照灯的条目仍保留在表中，但不生成武器圈。整体 Git tree 响应虽被截断，该防空目录的独立子 tree 已核对完整，193 个标签条目也均找到对应资产。

## 范围规则

- 火炮先取真实武器配置的 `attackMaxDist`；缺失时依次取同一武器挂载的 `AttackShellMaxRadius`、`AttackMaxRadius`。仍缺失时，保留武器 `aimMaxDist` 作为低置信度瞄准参考，并标为 `StaticAimLimitReference`，不称为实际交战上限。
- 导弹从 `bullet.rocket` 正确读取参数，先取 `rangeMax`；缺失时依次取制导控制距离、同一挂载的 `AttackMaxRadius`、发射器 `attackMaxDist`。不跨不同语义字段取最大值，也不以弹体飞行极限、雷达搜索距离或 `guaranteedRange` 代替交战上限。
- 同一类武器的不同配置保留有来源的最大静态能力。实际回放装弹、雷达开关、地形遮挡、目标运动和制导效果不由这张表确定。
- 火炮有多种挂载时优先主防空炮口径，再在同口径候选中选有来源的范围。烟雾、诱饵、假武器、无制导火箭不当作防空炮；SAM 车的低口径自卫机枪不另画防空炮圈。
- 已由完整单位/传感器配置确认无搜索或跟踪雷达、且只有火炮/机枪范围的单位，不输出威胁圈。M16 等无雷达炮车也适用；型号、轨迹和阵营照常保留。光学或红外传感器不能当作雷达。雷达装备未知时不推定为无雷达。
- 有雷达的炮车，以及有有效导弹范围的 SAM 或弹炮混合系统，继续输出其武器范围；无雷达的红外或光学制导导弹系统不会因此被删除。这里判断的是静态装备配置，不是回放中的雷达开关状态。

## ACMI 字段

主范围写入 `EngagementRange`（米，半径）和 `EngagementMode=1`。附带 `WT_EngagementRangeSource`、`WT_EngagementRangeMode`、`WT_EngagementRangeKind`、`WT_EngagementRangeBasis`，区分导弹、火炮、参数出处与瞄准参考。该 Mode 只是启用静态圈显示，不表示从回放读取到了雷达开机。

混合系统的最大范围始终放在第一圈。第二类武器同时保留为历史兼容的 `EngagementRange2` / `EngagementMode2` 及对应来源字段。当前 Tacview 文档仅明确第一圈，第二圈字段可见于旧版官方文档；本次未在 GUI 验证同时绘制两圈，因此只保证导出最大主范围与保留次级范围数据。

普通飞机、非防空地面载具、导弹轨迹、雷达锁定和干扰物不采用此型号范围表。`-ground-models=false` 会关闭型号及衍生的范围显示，阵营识别仍独立运行；`-ground-units=false` 关闭批量地面轨迹路径。

## 验证边界

Go 测试覆盖混合系统、无来源/非法数字、雷达支援排除、大小写、型号与空地身份隔离。真实回放只使用 `#2026.09.21 16.33.02.wrpl`，在 2 GiB Windows Job Object 限制下转换。前后比较保护对象 ID、位置、时间、生命周期、名称、阵营和所有非地面数据，仅允许已有地面对象的范围字段和有证据的防空分类改变。

此表对应固定版本的静态资料，不能保证与任意回放版本完全一致。未做 Tacview 界面渲染验收。
