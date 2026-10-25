# 导弹制导信息

[导出指南](acmi-export.zh-CN.md) · [型号识别](ACMI_MODELS.md) · [制导证据](evidence/missile-guidance.md)

导出器默认在导弹对象中写入静态制导类型及来源信息，并保留已有的 INS 时长标签。静态类型说明型号具备哪类制导方式，不能据此判断某一帧导引头是否已锁定、雷达是否开启或导弹是否进入末制导。

发射后运行状态另由 `-missile-seeker` 解析回放中的导引头块；与静态制导类型分开，见[传感器状态与原生兼容性](ACMI_SENSOR_STATES.zh-CN.md)。

## 类型与字段

| 类型 | 含义 |
| --- | --- |
| `ARH` | 主动雷达制导。 |
| `IR` | 红外制导。 |
| `SARH` | 半主动雷达制导。 |
| `Unknown` | 当前证据不足，或候选包含不同制导类型。 |

导弹首帧写入 `WT_GuidanceType`、`WT_GuidanceSource` 和 `WT_GuidanceConfidence`；有歧义时保留候选或冲突信息。它们是 ACMI 自定义属性，不保证 Tacview 标准遥测面板将其显示为专用仪表。`INS x.xx s` 标签继续保留。

## 使用方式

默认使用内嵌证据表。关闭该功能或更换数据表时，分别使用 `off` 和 JSON 路径：

```powershell
.\wrpl-acmi.exe -in "D:\Replays\replay.wrpl" -out "D:\Replays\replay.acmi"
.\wrpl-acmi.exe -missile-guidance off -in "D:\Replays\replay.wrpl" -out "D:\Replays\without-guidance.acmi"
.\wrpl-acmi.exe -missile-guidance "D:\missile_guidance.json" -in "D:\Replays\replay.wrpl"
```

关闭制导信息不会把手工名称转化为其他证据。`-names` 始终只负责覆盖显示名称。

## 判定依据

`arh_shell` 构造模板可以确认主动雷达类；普通 `rocket` 模板可能对应 IR 或 SARH，需要进一步证据。型号映射采用明确的内部名，不以名称前缀、槽位或发射顺序推测。

数据表源于历史研究材料 `handoff-missile-models-cm.md` 及其引用的报告和本地资产，来源记录保存在内嵌的 `missile_guidance.json` 中。相关推断和反例已整理到[制导证据报告](evidence/missile-guidance.md)。原始 seeker 位的动态语义不在这些静态类型结论的范围内。

## 仅适用于特定回放的推断

研究样例 `#2026.09.17 13.50.59.wrpl` 的 SHA-256 为：

```text
c0fcd26febf9f0ec75765337a841c1d55d089cd13b45740f0cbd41c190f418b2
```

只有完整文件哈希匹配时，样例内的 weapon ID 60 和 157 才会依据实际挂载及型号资产判为 IR。其他回放不会沿用这两个数字 ID 的结论。

F-2A 的挂载包含 AIM-7M，与早期“候选均为红外导弹”的简表冲突，因此 ID 143、145、146 保持 Unknown。Su-33 的混合挂载也无法按发射顺序区分。保留这些不确定性，是为了避免将候选写成确定的制导类型。
