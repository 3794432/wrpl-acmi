# 干扰物类型

[English](ACMI_COUNTERMEASURES.md)

干扰物使用 Tacview 原生类型 `Misc+Decoy+Flare` 或 `Misc+Decoy+Chaff`。无法可靠识别的释放记录统一按 **Chaff** 显示，同时标记 `WT_CountermeasureTypeConfidence=fallback`、`WT_CountermeasureTypeSource=UnknownAsChaff:<原因>`。这是导出规则，不代表回放已确认其为箔条。不根据速度、未知标志位、注册编号或发射器序号推测弹种。

导出器对飞机 storage 的 `0x33` 字段进行长度校验，保留释放前同一个完整发射者 EID 的发射器、弹药名称和数量快照，再查内嵌配置表。空弹药名对应配置中明确的默认 `bullet` 块。只有已知挂载里的有效干扰弹全部为同一类型时，才将其作为直接类型依据；混装记录无法逐枚对应，按规则归为 Chaff。数量为零的记录不作为当前有效弹药依据。`WeaponRef` 低 16 位是发射器序号，不是 Flare/Chaff 枚举；不同实体代次不共享弹药快照。

内嵌表覆盖 4 种已审核的分离式发射器配置，包括大型、MAW 和 BOL 变体。来源是 [War-Thunder-Datamine 固定提交 f7952e4](https://github.com/gszabi99/War-Thunder-Datamine/tree/f7952e4291e9fd7b6fe38e84020dddef925c3457/aces.vromfs.bin_u/gamedata/weapons/rocketguns)：外层 `bulletType=flr` 表示 Flare，`bulletType=chff` 表示 Chaff。未知发射器或弹药同样按 Chaff 处理；这不代表已经覆盖所有游戏版本。

`-countermeasure-log output.csv` 在原有 17 列后增加 `kind`、`type_source`、`type_confidence`、`launcher`、`bullet`。ACMI 使用 `WT_CountermeasureType*` 字段记录类型、来源和置信标记。消息和书签同步使用 Flare/Chaff 名称。

弹种判断与轨迹估算相互独立。名称中的 `(estimated)` 仍只表示原有的短时释放轨迹估算，对应 `WT_Estimated=1`、`WT_TrajectorySource=EstimatedFromRelease`；真实 WeaponSync 位置优先。对象 ID、归属、生命周期和轨迹模型均不因弹种分类而变化。

从固定、经过哈希校验的来源重建内嵌表：

```powershell
python tools/countermeasures/build_catalog.py
python tools/countermeasures/build_catalog.py --source-root C:/War-Thunder-Datamine
```

第二条命令完全离线，需要对应固定提交的原始文件。运行 EXE 无需下载资产。仍可使用 `-countermeasures=false` 关闭干扰物导出。类型使用 ACMI 2.1，不要求升级至 2.2。
