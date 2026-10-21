# 地面单位导出

所有 SPAA / SPAAM 的静态范围表、参数优先级、瞄准参考与兼容性边界见 [ACMI_AIR_DEFENCE_RANGES.md](ACMI_AIR_DEFENCE_RANGES.md)。

地面批量位置同步 `GroundModelPositions`（MPI `0xf073`）及服务器变体 `0xf074` 现在默认参与 ACMI 导出。旧版只匹配一种固定偏移的单单位位置消息，因而在部分空战回放中没有导出任何地面单位。

```bat
wrpl-acmi.exe -in "#2026.09.21 16.33.02.wrpl" -out "replay.acmi"
```

新增 `-ground-units=false` 可关闭这条批量地面同步路径，保留原有导出行为。日常使用 `run.bat` 无需增加参数。

解析保留完整状态，并通过每单位的有界历史缓存、RLE 解码和 XOR 更新还原增量状态，再读取真实位置。输出采用三分量 `T=经度|纬度|高度`，不伪造姿态。新地面对象从第一个有效位置开始创建，在既有对象后分配 ACMI ID，以保留飞机、武器、干扰物及雷达目标引用。

缺少 ECS 元数据时，直接使用回放里的地面单位 UID 作为独立身份，名称显示为 `Ground unit <uid>`，阵营为中立，并写入 `WT_UnitUID` / `WT_IdentitySource` 便于核对。不根据距离猜测型号、阵营或实体 ID。UID 0 是有效单位。仅在能够明确关联实体销毁记录时移除对应地面对象，缺失记录时不猜测击毁时刻。

同时修正 ECS `transform` 解析器丢弃平移的问题，保存真实平移到 `TMatrix.V4`；尚未解码的旋转不用于生成姿态。截断和非有限坐标被拒绝。

本次真实回放导出与验证只使用 `#2026.09.21 16.33.02.wrpl`。生成产物保留原有雷达锁定、导弹、炸弹及干扰物数据。验证覆盖单元测试、实际回放受限运行和前后 ACMI 数据比较，未进行 Tacview 界面渲染验收。
## 地面型号识别

默认开启 `-ground-models=true`。独立的 groundmeta ECS 解析器按真实 replay UID 将地面位置与 `unit__className` 关联，并用 `unit_storage__*` 的 IdFieldSerializer 字段 `0x6`（unit_name）交叉核对。型号冲突的 UID 不应用名称；没有证据时继续显示 Ground unit，不根据位置或模板声明顺序猜测。

部分地面模板包含 ecshashes 未收录的 float data component。独立解析器仅对确定宽度、未知 data-component 哈希的 POD 字段启用有界跳读并保留原始数据；原有飞机/武器 ECS 路径保持默认严格行为。存储解析限制在 1 MiB 并校验索引、尺寸和剩余数据。

Name / CallSign 使用本地游戏 lang/units.csv 中的具体型号名，查名时去掉 tankModels/、structures/ 等路径前缀，原文则完整保存在 WT_GroundModel；来源保存在 WT_GroundModelSource。通用 AAA/Static 等标签不能代替具体型号，找不到具体显示名时保留内部型号。明确的防空/设施分类可以细化 Type；tank 存储格式本身不作为装甲证据，因为它也用于无装甲反坦克炮。

在指定的 #2026.09.21 16.33.02.wrpl 中，140/140 已有地面轨迹全部命中型号，共 18 种：128 个车辆/火炮有可读名称，12 个机场雷达保留内部型号。另有 10 个 ECS 地面 UID 无已记录轨迹，不因获得名称而新增对象。型号功能本身不改变轨迹、生命周期或阵营；地面阵营分配独立于型号开关，见下节。

本次仍只验证这一份回放；其他版本、地面载具类别或服务器分片的型号覆盖率尚未实测。未进行 Tacview 界面渲染验收。

## 地面红蓝阵营

默认将 groundmeta 中的 ECS team 按实际地面 UID 关联到位置轨迹，与飞机使用相同映射：team=1 输出 Coalition=Blue / Color=Blue，team=2 输出 Coalition=Red / Color=Red。WT_GroundTeam 保留数值，WT_GroundTeamSource 保留 ecs:team 来源；不根据国别、型号、位置或阵营数量猜测。未知值、缺少来源或存在 UID 身份冲突时，不应用这条阵营证据。

阵营识别不依赖具体型号能否查到，也不受 -ground-models=false 影响；-ground-units=false 仍关闭批量地面轨迹导出。既有显式实体 team 的备用路径保持可用。

指定回放现有 140 个地面轨迹全部取得阵营：70 蓝、70 红。只改变这些地面对象的 Coalition / Color 并添加阵营来源字段；型号、位置、时间、生命周期及 954 个非地面对象保持一致。真实转换依然受 2 GiB Job Object 限制，未进行 Tacview 界面渲染验收。

## ItO 90M 的 Tacview 名称匹配

Tacview 按 Name 在自身数据库选择匹配条目；游戏标签 ItO 90M 在用户环境中被误匹配为 T-90M。对明确的 fr_crotale_ng / sw_crotale_ng 内部型号使用 Name=Crotale NG，CallSign 仍保留 ItO 90M，Type 保持防空类别。WT_GroundModel、来源、UID 和阵营继续保留原始证据。不会把真正的 ussr_t_90m_2020 改名，也不会覆盖用户手动指定的不同名称。

本次指定回放只改变 8 个 fr_crotale_ng 对象的 Name，其他所有 ACMI 字节保持一致。没有强制套用另一种防空系统的三维模型；具体外形取决于 Tacview 可用模型。未在 Tacview 界面中复验。

## ZA-35 威胁圈

对明确的 uk_rooikat_za_35 输出 EngagementRange=3000（半径，米）及 EngagementMode=1，用于启用静态参考范围显示，不再依赖 Tacview 自带型号数据库的射程。该值取自 War Thunder Datamine 2.59.0.74 / f7952e4291e9fd7b6fe38e84020dddef925c3457 中 ZA-35 两门 GA-35 引用的 gamedata/weapons/groundmodels_weapons/35mm_ga_35_user_cannon.blkx 的 attackMaxDist=3000；载具两武器块的 AttackShellMaxRadius=3000 作为佐证。

WT_EngagementRangeSource 记录配置出处及版本，WT_EngagementRangeMode=StaticModelReference 标明其为型号静态参考。EngagementMode=1 启用此参考圈，不表示从回放读到了雷达开机。瞄准上限、弹丸飞行截止距离及雷达搜索距离不用于交战半径；不添加未获证据的雷达状态或垂直包线。

参考数据不保证与所有回放的游戏版本相同，也不是实时可击杀包线。其他型号没有来源时不写入默认半径。本次指定回放的 12 个 ZA-35（蓝 6、红 6）均添加范围字段，其余所有 ACMI 内容保持一致；未在 Tacview 界面中复验。

## ZSU-23-4V 威胁圈

精确型号 ussr_zsu_23_4 采用同版本 23mm_2a7_user_cannon.blkx 的 attackMaxDist=2500，输出半径 2.5 km 的静态参考圈和来源字段。其单位配置另有 AttackShellMaxRadius=3000；当前使用武器层 2500 m 作为参考，不声称已证明两个字段在引擎中的覆盖优先级。瞄准上限 4 km、弹丸截止距离 5 km 和雷达范围均未用于此圈。

指定回放的 9 个 ZSU-23-4V 均增加范围字段；ZA-35 3 km 参考圈、ItO 90M 名称修复、所有阵营、轨迹及非地面数据均保持不变。测试及前后比较通过，未在 Tacview 界面中复验。
