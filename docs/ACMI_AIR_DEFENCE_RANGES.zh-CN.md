# SPAA / SPAAM 静态威胁范围

[English](ACMI_AIR_DEFENCE_RANGES.md)

导出器内嵌 `cmd/acmi/ground_threats.json`，不依赖 Tacview 自带型号数据库给这些单位补射程。表中每条记录以游戏内部型号 basename 关联回放的明确地面 UID，匹配时统一大小写；不按名称相似、国别或位置猜测型号。

数据固定到 War Thunder Datamine `f7952e4291e9fd7b6fe38e84020dddef925c3457`（`2.59.0.74`）。表中共有 255 个型号；清单取自 `unittags.blkx` 的 `type_spaa` 条目，并补充 `gamedata/units/air_defence` 下的固定/AI 条目。只有雷达、火控或探照灯的单位可以保留在来源表中，但不生成武器圈。

## 范围规则

- 火炮先取真实武器配置的 `attackMaxDist`；缺失时依次取同一武器挂载的 `AttackShellMaxRadius`、`AttackMaxRadius`。仍缺失时，保留武器 `aimMaxDist` 作为低置信度瞄准参考，并标为 `StaticAimLimitReference`，不称为实际交战上限。
- 导弹从 `bullet.rocket` 正确读取参数，先取 `rangeMax`；缺失时依次取制导控制距离、同一挂载的 `AttackMaxRadius`、发射器 `attackMaxDist`。不跨不同语义字段取最大值，也不以弹体飞行极限、雷达搜索距离或 `guaranteedRange` 代替交战上限。
- 同一类武器的不同配置保留有来源的最大静态能力；每类最多输出一个主范围。实际回放装弹、雷达开关、地形遮挡、目标运动和制导效果不由这张表确定。
- 火炮有多种挂载时优先主防空炮口径，再在同口径候选中选择有来源的范围。烟雾、诱饵、假武器和无制导火箭不当作防空炮；SAM 车的低口径自卫机枪不另画防空炮圈。
- 只有来源确认无搜索或跟踪雷达、且没有有效导弹范围的纯炮/机枪配置才隐藏威胁圈。当前 255 个型号中有 142 个符合该策略；型号、轨迹和阵营照常保留。光学或红外传感器不能当作雷达，雷达装备未知时也不推定为无雷达。
- 有雷达的炮车，以及有有效导弹范围的 SAM 或弹炮混合系统继续输出武器范围；无雷达的红外或光学制导导弹系统不会因此被删除。这里判断的是静态装备配置，不是回放中的雷达开关状态。

## ZSU-37-2 显示半径

`ussr_zsu_37_2` 使用用户确认的 2,000 米显示半径，输出 `EngagementRange=2000` 和 `EngagementMode=1`。这是静态显示半径覆盖，`WT_EngagementRangeBasis` 明确说明它不是原始武器 `attackMaxDist`。

来源字段没有被覆盖：`WT_EngagementRangeSource` 同时记录“User-configured ZSU-37-2 display radius: 2000 m”，以及固定 Datamine 中 `37mm_2a11_user_cannon.blkx#/attackMaxDist=4000 m` 的原值。因此 ACMI 能区分当前 2,000 米显示选择与上游 4,000 米原值，不能把显示半径误读为 Datamine 推导结果。

## ACMI 字段

主范围写入 `EngagementRange`（米，半径）和 `EngagementMode=1`。附带 `WT_EngagementRangeSource`、`WT_EngagementRangeMode`、`WT_EngagementRangeKind`、`WT_EngagementRangeBasis`，区分导弹、火炮、参数出处与瞄准参考。该 Mode 只是启用静态圈显示，不表示从回放读取到了雷达开机。

混合系统的最大范围始终放在第一圈。第二类武器同时保留为历史兼容的 `EngagementRange2` / `EngagementMode2` 及对应来源字段。当前 Tacview 文档仅明确第一圈，第二圈字段可见于旧版官方文档；本次未在 GUI 验证同时绘制两圈，因此只保证导出最大主范围与保留次级范围数据。

普通飞机、非防空地面载具、导弹轨迹、雷达指定和干扰物不采用此型号范围表。`-ground-models=false` 会关闭型号及衍生的范围显示，阵营识别仍独立运行；`-ground-units=false` 关闭批量地面轨迹路径。

## 验证边界

Go 测试覆盖混合系统、无来源或非法数字、雷达支援排除、大小写、型号与空地身份隔离。真实回放验证只使用 `#2026.09.21 16.33.02.wrpl`，并在 2 GiB Windows Job Object 限制下转换。前后比较保护对象 ID、位置、时间、生命周期、名称、阵营和所有非地面数据，仅允许已有地面对象的范围字段和有证据的防空分类改变。

此表对应固定版本的静态资料，不能保证与任意回放版本完全一致。上述数据、输出和测试不构成 Tacview GUI 或实际渲染验收。
