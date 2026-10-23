# 干扰物类型与轨迹

[English](ACMI_COUNTERMEASURES.md) · [导出指南](acmi-export.zh-CN.md)

干扰物默认参与导出，使用 Tacview 原生类型 `Misc+Decoy+Flare` 或 `Misc+Decoy+Chaff`。类型识别与轨迹来源分别记录：确定了弹种，不代表已经取得真实飞行轨迹；显示了轨迹，也不代表弹种已被确认。

## 类型判定

导出器读取飞机 storage 的 `0x33` 字段，完成长度校验后，保存释放前同一个完整发射者 EID 的发射器、弹药名称和数量快照，再与内嵌配置表核对。不同实体代次之间不共享弹药快照。

只有已知挂载中的有效干扰弹全部属于同一类型时，才能据此确定 Flare 或 Chaff。数量为零的记录不算有效弹药；空弹药名只在配置明确提供默认 `bullet` 块时使用该默认值。

混装、未知发射器或未知弹药无法可靠对应到单次释放，统一按 Chaff 显示，并写入：

```text
WT_CountermeasureTypeConfidence=fallback
WT_CountermeasureTypeSource=UnknownAsChaff:<原因>
```

这是一项显示回退规则，不表示回放已经确认弹药为箔条。速度、未知标志位、注册编号和发射器序号均不用于猜测弹种。`WeaponRef` 的低 16 位表示发射器序号，并非 Flare/Chaff 枚举。

## 配置来源

内嵌表覆盖 4 种经过审核的分离式发射器配置，包括大型、MAW 和 BOL 变体。数据来自 [War-Thunder-Datamine 固定提交 f7952e4](https://github.com/gszabi99/War-Thunder-Datamine/tree/f7952e4291e9fd7b6fe38e84020dddef925c3457/aces.vromfs.bin_u/gamedata/weapons/rocketguns)：外层 `bulletType=flr` 表示 Flare，`bulletType=chff` 表示 Chaff。该范围不代表已覆盖所有游戏版本。

## 轨迹与诊断字段

真实 WeaponSync 位置优先。缺少位置同步时，导出器可生成短时释放轨迹估算，并通过名称中的 `(estimated)`、`WT_Estimated=1` 和 `WT_TrajectorySource=EstimatedFromRelease` 标明。

弹种分类不改变对象 ID、归属、生命周期或轨迹模型。ACMI 的 `WT_CountermeasureType*` 字段记录类型及其依据；消息和书签使用对应的 Flare/Chaff 名称。

需要诊断报告时，可传入 `-countermeasure-log output.csv`。报告在原有 17 列之后提供 `kind`、`type_source`、`type_confidence`、`launcher` 和 `bullet`，便于核对分类依据。

## 重建内嵌表

在仓库根目录执行以下任一命令：

```powershell
python tools/countermeasures/build_catalog.py
python tools/countermeasures/build_catalog.py --source-root C:/War-Thunder-Datamine
```

第一种方式获取固定且经过哈希校验的来源；第二种方式使用本地对应提交的原始文件，可离线运行。日常使用 EXE 不需要下载这些资产。

使用 `-countermeasures=false` 可关闭干扰物导出。上述类型兼容 ACMI 2.1，无需改为 2.2。
