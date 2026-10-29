# ACMI 元数据、书签与飞机遥测

[English](acmi-information.md) · [导出指南](acmi-export.zh-CN.md)

本页说明文件级信息、事件书签和飞机遥测字段的含义，并介绍静态地面对象及雷达几何的来源规则。位置和姿态来自回放；由这些记录计算的遥测值会注明估算依据。

## 文件信息与对象分组

| 字段 | 含义 |
| --- | --- |
| `Title` | 由地图、模式和回放时间组成的标题。 |
| `Author` | 作者或操作员，默认 `wrpl-acmi`，可用 `-author` 指定。 |
| `RecordingTime` | UTC 导出时间。 |
| `ReferenceTime` | 回放时间基准，与文件创建时间分开记录。 |
| `Category` | `War Thunder`。 |
| `DataRecorder` | `wrpl-acmi`。 |
| `Comments` | 构建版本、请求的布局、地理参考版本及估算策略等说明。 |

地理参考字段记录最终使用的 geographic 或 Atlas 布局。对象的 `Group` 根据明确的阵营信息生成；已识别的两类机场雷达使用 `Importance=0.75` 设置显示优先级，该值不表示作战状态。

`Name`、`CallSign` 和 `Pilot` 保留各自用途。按照 [Tacview ACMI 文档](https://www.tacview.net/documentation/acmi/en/)的字段约定，`ShortName`、`LongName` 和 `FullName` 由型号数据库提供，`Registration` 表示注册号，因此导出器不会用玩家名或未经证实的制造商信息填充这些字段。

## 事件书签

书签默认关闭（`-bookmarks=false`），保留普通消息，避免重复的高亮日志行。使用 `-bookmarks=true` 开启后，涵盖已确认的飞机击毁、导弹发射或首次观测、炸弹投放或首次观测、干扰物释放，以及雷达目标指定变化。同一时刻的重复书签会去重；原有消息与击毁事件仍保留。

标准 ACMI 没有单条事件背景色参数，书签高亮由 Tacview 决定；导出器不为 Critical damage 指定独立颜色。雷达指定书签仅表示目标指定发生变化，不能据此确认 STT 锁定或雷达开关状态。

## 飞机估算遥测

估算默认开启，使用 `-estimate-telemetry=false` 可关闭：

```powershell
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi" -estimate-telemetry=false
```

估算功能不修改飞机坐标、姿态、对象 ID 或击毁时刻，也不改变导弹飞行字段和 INS 标签。

### 计算方法

| 字段 | 方法与限制 |
| --- | --- |
| `HDG` | 从记录姿态取得航向。 |
| `TAS` | 在无风假设下使用轨迹速度。 |
| `Mach` | 采用分层标准大气，将游戏高度近似视为位势高度。 |
| `IAS` | 使用 `TAS * sqrt(rho / rho0)`，即 EAS 近似；不是精确的可压缩流 IAS/CAS，也不是座舱读数。 |
| `AOA` | 根据无风速度在机体坐标系中的方向计算；零速或向后飞行时留空。 |
| 三轴 G | 将比力 `(坐标加速度 - 重力) / g` 投影到机体的前、右、上轴，写入纵向、横向和法向 G 字段。 |

三轴 G 的物理含义与导弹坐标加速度模长不同。例如，水平匀速飞行的法向值为 +1G，自由落体为 0G，倒飞匀速为 -1G。计算没有还原风场、天气或完整气动模型，不能用作真实机体载荷和气动极限的证据。

### 样本筛选与空值

计算使用有界短窗口二次拟合，以降低采样抖动，不跨越超过两秒的缺口或异常区间。非有限数值、重复或逆序时间、超过 1,200 m/s 的速度，以及超过 100 个坐标 G 的加速度均会被拒绝；84.852 km 以上不进行大气估算。

缺失或无效字段以空值写出，清除此前状态。输出备注会说明这些数据由位置和姿态推导，并非直接记录的游戏遥测。

## 地面初始化快照

同一 UID 首份无歧义 ECS 构造之前，仅有不变坐标、尚未绑定实体代次的前缀不再导出成独立车辆。这类车队生成点的初始化快照原先会先创建临时对象，在构造信息到来时移除，即使没有显式 `Destroyed` 事件，Tacview 也可能将移除显示为销毁。规则依据身份边界和记录坐标是否变化，不使用固定开局秒数或地图位置黑名单。

有实际移动的前缀、没有后续构造依据的未知单位、构造元数据矛盾，以及有对应受害者报告的前缀仍保留以正常解析生命周期。已确认代次的车辆、真实静止单位和真实销毁事件继续保留原有轨迹和寿命。省略初始化对象可能重新编号后续地面/设施 ACMI ID，父对象和雷达引用会按保留身份重新生成。

## 静态地面对象

启用地面导出时，没有同步轨迹的建筑或工事可使用 ECS 构造 `transform` 的平移分量和实际包时间，但必须同时具有明确的静态类别证据。已有同步轨迹始终优先。

身份或型号冲突、UID 回收、无时间戳、非有限坐标、位置变化，以及 `dummy/dummy_immortal` 任务参照物均会被排除。车辆仅仅缺少轨迹，不足以将其作为固定建筑导出。

符合条件的对象使用 `Type=Ground+Static+Building`，保留型号和阵营，并标注：

```text
WT_GroundPositionSource=ECSConstructionTransform
WT_GroundStatic=1
```

明确匹配到 ECS 销毁记录时，对象会被移除。完整的身份和阵营规则见[地面单位导出](ACMI_GROUND_UNITS.md)。

## 空对空导弹型号

默认内嵌目录扫描固定 Datamine 提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457` 的全部 706 个 `rocketguns/*.blkx` 武器定义。`cmd/acmi/data/aam_models.json` 包含 683 个别名、119 个显示名称（114 个有译名，5 个保留未翻译的内部标识），覆盖 AIM-120A/B/C-5/D、R-77/RVV-AE/R-77-1、麻雀、响尾蛇、不死鸟、R-27、PL 系列及其他制导空空武器。无需型号出现在附带的样例回放中；同一内部身份保留已审查的旧显示拼法。

名称目录负责把明确的武器身份映射成名称，不能独自确定未知对象的型号。武器报告通过同一发射者和唯一结束时间匹配；一致的报告可命名同局相同原始构造标识、模板及武器类别的其他导弹。小于 129 的非零短标识也可参与关联，但不据此编造公开数字武器 ID。零值或超范围标识不传播，报告矛盾时保留未知。精确载机的实际挂载快照可提供唯一兼容型号；机型可选挂载可按下文规则推断基础型号，不作为已确认的具体变体。

重建命令为 `python tools/agm/build_catalog.py --cache <资产缓存> --fetch --role AAM --out cmd/acmi/data/aam_models.json`。优先使用弹体内部名的精确英文译名；只有单一制导弹体时才可读取该武器定义的精确发射器译名。没有译名时保留原始内部标识，并在目录来源中标注。共享弹体别名有歧义时省略该别名，精确发射器别名仍可使用。SAM 使用下文独立目录；未来新增资产需重新构建目录。

### 制导类型与基础型号回退

已支持的 AAM 飞行中导引头布局同时参与制导分类：IR 布局可在型号未知时确认红外类别；雷达布局仍需结合独立模板或型号依据区分 ARH/SARH。冲突、未解码布局保留未知，不据此编造锁定状态。

具体型号识别之后，默认 `-missile-models embedded` 可将同一弹族的兼容变体合并为基础型号，例如 AIM-120A/B/C/D 显示 `AIM-120`，AIM-9 和 R-73 的不同变体分别显示 `AIM-9`、`R-73`。优先使用完整实际载机弹药（`ReplayOwnerLoadoutFamily`）；不足时，可结合精确发射时刻的载机型号，从固定版本的可挂载槽目录中按武器类别、模板和已解码导引头类型筛选（`CarrierOptionsGuidanceFamily`）。目录扫描 1,652 份定义，覆盖 490 份飞机/直升机配置，仅使用明确的 `WeaponSlots`/`commonWeapons` 导弹引用。`weapons/containers` 组合挂架会递归展开到精确导弹定义，保留每一层依赖的 Git blob 哈希；循环、缺失引用或超过深度限制仍视为未解析。AAM 目录同时按已确立的资产约定，将省略或为假的 `radarSeeker.active` 识别为 SARH，不将该规则用于 AGM 被动雷达导引头。这可将 Sparrow 挂架对应为 `AIM-7`、MiG-29 的兼容 SARH 候选对应为 `R-27`，同时保留未分变体标记。没有挂载槽定义、对应武器类别引用未完整识别、载机身份未知或仍剩多个弹族时不命名。

基础型号写入原生 `Name/CallSign` 和 `WT_MissileModel`，保留变体候选，并标记置信度 `Inferred`、`WT_MissileFamily`、`WT_MissileVariantUnresolved=1`；载机可挂载目录推断另写 `WT_MissileFamilyCarrier`。`WT_MissileInternalName` 留空，表示没有解出具体变体。已有具体型号和手动显示名优先；这类推断不向其他导弹传播，也不把静态制导属性当作记录的工作状态。`-missile-models off` 关闭基础型号推断，已支持的导引头类别识别仍独立工作。若候选同时包含 AIM-9 和 R-73，可退回 `AAM (IR)`，不按国籍或发射顺序二选一。

先生成 AAM/AGM 别名表，再运行 `python tools/agm/build_carrier_catalog.py --cache <资产缓存> --fetch --out cmd/acmi/data/missile_carriers.json` 重建。每份飞行器定义均校验固定 Git 树中的 blob 哈希；去掉 `--fetch` 可离线重建。EXE 只内嵌精简名称、引用及来源，运行时不需要游戏资产或联网。

## 空对地导弹型号

默认 `-missile-models embedded` 使用固定 Datamine 版本的 AGM 目录（`cmd/acmi/data/agm_models.json`），依据明确的制导武器字段及 `units_weaponry.csv` 英文名称生成 680 个别名、177 个显示名称（166 个有译名，11 个保留未翻译的内部标识）。覆盖 Maverick、Hellfire、Kh-25/29/31/38/58/59、LD-10、YJ-91、HOT、Spike、Brimstone 等弹族。目录提供名称对应关系，不将某局数字武器 ID 当成全游戏通用型号编号。

武器报告按记录的同一发射者及 250 ms 内的销毁时刻唯一匹配导弹；一致的报告证据可命名同局相同原始构造标识、模板及 AGM 类别的其他导弹。精确载机的弹药快照也可提供唯一兼容型号，或在相同发射者、实体代次和武器组中传播强身份依据。即使数字 ID 相同，AAM 与 AGM 别名也分别处理。混装、未知或冲突的挂载保留候选，不按发射顺序、速度或挂点序号选择变体；SAM 的最新改进型假定不用于 AGM。

型号写入原生 `Name/CallSign` 和 `WT_MissileModel`，`WT_MissileInternalName`、型号来源和置信度保留识别依据。命名不改变位置、生命周期或记录的导引头状态，资产中 `optical` 也不等于已读到了红外或电视导引头遥测。不能确定时保留 `AGM (unknown)`。`-missile-models off` 关闭目录辅助的 AGM 命名；自定义型号表使用自身提供的别名。

重建命令为 `python tools/agm/build_catalog.py --cache <资产缓存> --fetch --out cmd/acmi/data/agm_models.json`。缓存需包含固定 Datamine 提交 `f7952e4291e9fd7b6fe38e84020dddef925c3457` 的 Git 递归树 `tree.json`；下载及缓存内容均校验 Git blob 哈希，去掉 `--fetch` 可离线重建。脚本扫描固定版本的全部 rocketgun 定义，以明确制导字段选择非 AAM、非 SAM 武器，不再限定文件名弹族。显式指定 `--role AGM` 与省略该参数相同；未来新增资产需重新构建目录。应用内嵌生成的别名，运行时不需要缓存或联网。

## 炸弹型号

炸弹型号默认开启（`-bomb-models embedded`）。`cmd/acmi/data/bomb_models.json` 将 Datamine 中明确的 `bombGun=true` 定义对应到同一固定版本的英文武器名称，涵盖常规、制导及核炸弹资产，也包含武器名称以 AGM 开头的 Bomb 构造（如 JSOW）。类型继续依据实际 Bomb 组件写为原生 `Type=Weapon+Bomb`，不按名称前缀重新分类。

武器报告按记录的同一投放者及 250 ms 销毁窗口关联。唯一匹配标为 `KillReportMatched` / `Matched`；本局相同原始构造标识和模板的一致报告证据，可补全其他炸弹名称，标为 `ReplayWeaponTokenFromKillReport` / `Inferred`。该标识不是全游戏通用型号 ID。同型号多枚炸弹同时落地只能确定类型，不据此指定哪一枚造成击杀。不同标识同时结束、报告矛盾或不同内部变体使用同一显示名时，不认定唯一变体。完整且唯一的载机实际挂载也可命名；机型所有可选挂载不能代替本次出击配置。

型号显示在原生 `Name/CallSign`；`WT_BombModel`、`WT_BombInternalName`、`WT_BombModelSource`、`WT_BombModelConfidence`、`WT_BombLeadingULEB` 及可选候选/冲突字段保留依据。构造身份、投放者或有效报告不足时继续显示 `Bomb`；仅为显示而补出的回放结束时间不用于匹配报告。命名本身不改变原有位置、对象 ID 或生命周期，单独的运动估算见下文，手动 `-names` 覆盖仍优先。`-bomb-models off` 可独立关闭此功能，不受 `-missile-models` 控制。

重建命令为 `python tools/bombs/build_catalog.py --cache <资产缓存> --fetch --out cmd/acmi/data/bomb_models.json`。使用 AGM 部分说明的固定提交 `tree.json` 及缓存，相邻 `tools/agm` 脚本提供共用下载逻辑，全部输入均校验 Git blob 哈希。去掉 `--fetch` 可离线重建；转换 EXE 内嵌结果，运行时不需要资产缓存或联网。

## 炸弹运动估算

`-estimate-bomb-trajectories=true` 默认开启，仅处理有构造记录、没有任何 WeaponSync 实际位置的炸弹。必须具有明确关联的载机出击记录，并能从投放前后 1.5 秒内的两个有限位置计算初速，样本间隔不得超过 1.5 秒。缺失、过期、歧义或异常速度时保留静态投放点。已有真实炸弹轨迹始终优先。

模型继承载机在本地坐标中的对地速度，施加向下 9.80665 m/s² 的恒定重力，不模拟阻力、风、投放冲量、减速伞或制导。每 0.2 秒生成一个位置，最长 180 秒，并受原生命周期、现有记录及海平面限制。海平面只是避免轨迹无限下落的显示截止，不是地形碰撞或测得的命中；转换时不要求安装地形。投放高度不大于零时不估算。

标签改为 `Estimated ballistic trajectory`。`WT_BombPositionSource=EstimatedBallistic`、`WT_BombTrajectoryEstimated=1`、`WT_BombEstimateModel`、`WT_BombInitialVelocity*`、`WT_BombEstimateEndReason` 及原始 `WT_BombRecordedEndTimeMS` 保留模型与来源。`WT_BombTrajectoryAvailable=0` 仍表示缺少真实同步轨迹。估算显示可能早于原销毁记录结束，文件备注会说明假设。设为 `-estimate-bomb-trajectories=false` 可恢复原来的 `Release position only` 显示。

## 地空导弹

SAM 飞行轨迹默认导出。地面武器组 19–21 的 Rocket 构造记录，必须通过完整发射者 EID、构造时间和型号匹配内嵌 SAM 目录；第 19 组也包含多管火箭炮和弹道火箭，因此不能仅凭触发组认定为 SAM。当前目录覆盖 65 种发射车配置、55 种不同弹药名称，包括 S-300、SAMP/T、BUK、铠甲、道尔、通古斯卡、罗兰、响尾蛇、SPYDER、HQ-11 和 03 式等配置。

每条导弹轨迹都要求有按完整实体身份及生命周期匹配的 WeaponSync 实际位置。只有构造记录时不估算飞行。写入 Tacview 原生 `Type=Weapon+Missile`、`T`、阵营，以及存在有效地面轨迹时的 `Parent`。发射车被击毁后，导弹仍按自身记录继续飞行。关闭 `-ground-units` 和 `-airfields` 不会关闭 SAM 轨迹或丢失发射车的已知阵营。

回退名称 `SAM (<发射车>)` 表示平台，不猜测具体导弹变体。`WT_WeaponRole=SAM`、`WT_Launcher*`、`WT_SAMAmmoCandidates` 和 `WT_SAMAmmoSource` 保留身份与静态弹药依据。混合配置保留所有候选，不按最大射程、发射顺序或位置选择弹药；静态候选也不代表当前导引头模式。发射车证据缺失或冲突时保持未分类。

经完整审查的发射车配置仅有一种弹药身份时，将其准确的 `bulletName` 对应到同一 Datamine 版本的 `units_weaponry.csv` 英文名称，写入原生 `Name/CallSign` 及 `WT_MissileModel`；`WT_MissileInternalName` 保留资产标识。`WT_MissileModelSource=LauncherConfigurationSingleAmmo`、置信度 `Inferred` 和 `WT_SAMModelEvidence=StaticLauncherConfiguration` 表明这是静态配置推导，并非直接读到了发射弹种。已有击杀报告身份和手动显示名称优先；`-missile-models off` 可关闭此配置命名步骤。

审查包含载具改装、武器预设及非默认导弹弹种。使用 `-sam-model-fallback off` 时，审查不完整、多弹种、缺少译名或模板证据矛盾会继续使用发射车回退名，并保留型号候选。型号命名不推断实时导引头模式，也不改变飞行坐标。

默认的 `-sam-model-fallback latest` 对未确定的 SAM 应用明确的显示假定：优先已知雷达弹，再选择同类最新已核对改进型。内嵌顺序可在 `cmd/acmi/data/sam_variant_preferences.json` 修改；“最新”指这份已核对表，不预判后续游戏更新。例如 2S6 → 9M311-1M、SPYDER → I-Derby ER，HQ-11 使用已知候选 FM-3000。非雷达弹族保留其实际制导类别。只能选择发射车候选中已有的弹药；没有已核对顺序的新弹族仍保持未确定，已有识别结果保持不变。

假定型号保留全部候选，并标记 `WT_MissileModelSource=UserLatestSAMVariant`、置信度 `Assumed`、`WT_SAMModelEvidence=UserPreferenceFallback` 和 `WT_SAMModelAssumed=1`，文件备注也记录当前策略。这是操作员偏好，不是回放实际弹种证据；记录的导引头模式、位置和阵营保持不变。`-missile-models off` 同时关闭这一命名步骤。

可用 `tools/sam/build_catalog.py --catalog <完整防空武器目录.json> --out cmd/acmi/data/sam_launchers.json` 离线重建精简表。输入为固定 Datamine `f7952e4291e9fd7b6fe38e84020dddef925c3457` 的 schema-1 完整武器目录，需包含每个车型的 `weapons` 证据；仅保留射程的 `ground_threats.json` 不能替代。运行时使用内嵌结果，无需外部文件或联网。

型号命名还需按 `tools/sam/build_missile_names.py` 的帮助说明，传入固定版本的资产缓存和完整目录，保留输出的 JSON 审查报告，再用 `build_catalog.py --evidence <审查报告.json>` 合并全部已核对变体及完整性依据。使用 `build_missile_names.py --names` 生成 `cmd/acmi/data/sam_missile_names.json`。不含审查报告的基础目录仍支持轨迹导出，但不能证明候选弹药已完整。

## 雷达几何

`LockedTargetAzimuth` 是机体相对方位角，向右为正；`LockedTargetElevation` 是机体相对俯仰角，向上为正，单位均为度。距离和角度只使用事件时刻或之前最多一秒的有效位置，并检查对象生命周期。

解锁、目标销毁或几何无效时，相应字段会清空；切换到具有新鲜几何信息的目标时立即更新。双方位置重合时，距离可为零，但角度没有定义。更多说明见[雷达目标指定](ACMI_RADAR_LOCKS.md)。

## CSV 与格式兼容性

四类诊断 CSV 默认不生成：`-list`、`-missile-flight-log`、`-countermeasure-log` 和 `-radar-lock-log` 分别接受独立的输出路径。

默认文件版本为 ACMI 2.1，可通过 `-acmi-version 2.2` 显式切换。上述字段不要求提高默认版本。既有检查涉及输出文本和测试，尚不能据此确认 Tacview 1.9.4、1.9.5 的界面加载、渲染或地形兼容性。

## 事件消息时间线

启用 `kill` 时，地面目标在原生 `Event=Destroyed` 中显示 `<目标> destroyed by <攻击载具> (<玩家>) with <武器>`，不再重复添加一条击杀 `Message`，适用于所有武器类型。目标必须精确匹配记录的身份和死亡时刻；武器使用已核对的显示名，未识别名称保留报告原文。销毁时的 `WT_KillSource=F058`、`WT_KillWeapon*`、`WT_KillerVehicle`、`WT_KillerPlayer` 以及有效 UID/PID 字段保留依据。

攻击者的明确 PID 按报告时刻的玩家名单解析，不要求攻击机仍然存在；不把 PID 当作载具 UID，也不重新创建已删除的攻击机。缺失 PID 与编号零分别处理，AI 地面单位不能借用无关玩家身份。缺少名称时留空，仅有武器时明确标注攻击者未知；目标身份或报告存在歧义、冲突时保留通用销毁事件。不根据爆炸范围或最近玩家猜测归属。关闭 `kill` 类别只关闭这些注释，仍保留普通销毁事件。

默认 `-events=kill,chat,join,damage,radar`。使用 `-events=none` 可关闭这些消息，也可选择逗号分隔的子集，例如 `-events=kill,radar`。奖励默认关闭，显式添加 `award` 或使用 `-events=all` 可开启。此开关与 `-bookmarks`、四类可选 CSV 独立。事件保留原始回放包时间，使用既有 ACMI 时间基准。

- 击杀消息只使用明确的击杀者、受害者身份和击杀者的 `PlayerWeapon`。飞机击杀时间必须与 F058 确认的销毁时刻完全一致。归属缺失、有歧义或相互冲突时跳过；不将 `DestroyedWeapon` 冒充击杀武器，不按距离归属，也不移动销毁时间。
- 聊天、玩家状态变化、严重/致命伤害报告和奖励来自对应回放字段。不能可靠关联对象的消息保留为不引用对象的文字；初始玩家名单不当作加入事件。
- 伤害消息显示为“受损载具（玩家）critical damaged by 攻击载具（玩家）”，Severe 同理使用 `severe damaged by`。无法确认的身份保持未知，显示文本不再包含槽位、原始 UID 或起火标志。
- 雷达消息表示目标指定及清除，有距离时显示 `Target <名称>; Range <米数> m`，距离仍由双方位置计算为几何斜距。消息沿用原生雷达属性的状态变化，包括两次源机位置同步之间的清除。它不证明 STT 硬锁。`-radar-locks=false` 关闭原生锁定属性；同时从 `-events` 中去掉 `radar` 才会关闭雷达消息。
- 损坏或过大的事件包会跳过，事件收集有数量上限；同一时刻的完全相同消息去重。消息不会重新创建尚未出现或已经删除的对象。

## 移动地面单位朝向与销毁

移动地面单位根据相邻真实位置推导地理航向。有效朝向使用六分量 `T`，并标注 `WT_HeadingSource=PathDerivative`，不代表实测车体或炮塔姿态。静态对象、单点轨迹和不可靠运动区间继续仅输出位置，不跨大段数据缺口推导方向。匹配到明确地面销毁记录时，在删除对象前输出 `Event=Destroyed`；轨迹最后一个位置点本身不视作销毁。
