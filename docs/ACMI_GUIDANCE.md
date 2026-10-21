# 导弹制导信息

此功能在现有导弹型号和 INS 时长之外，增加静态制导类型及证据字段。导出方式不变，继续把 WRPL 拖到 `run.bat` 即可。

## 数据含义

- `ARH`：主动雷达制导。
- `IR`：红外制导。
- `SARH`：半主动雷达制导。
- `Unknown`：证据不足；不把普通 `rocket` 模板一律当作 IR。

导弹首帧新增 `WT_GuidanceType`、`WT_GuidanceSource`、`WT_GuidanceConfidence`。有歧义时另写候选或冲突信息。它们是 ACMI 自定义原始数据属性，不保证 Tacview 标准遥测面板自动将其显示为专用仪表。原来的 `INS x.xx s` 标签保留。

## 开关

默认使用内嵌证据表：

```powershell
./wrpl-acmi.exe -in "D:\回放.wrpl" -out "D:\回放.acmi"
./wrpl-acmi.exe -missile-guidance off -in "D:\回放.wrpl" -out "D:\兼容旧版.acmi"
./wrpl-acmi.exe -missile-guidance "D:\missile_guidance.json" -in "D:\回放.wrpl"
```

关闭后恢复本次改动前的导出行为。手工 `-names` 只覆盖显示名，不会把任意字符串当成制导类型的证据。

## 证据边界

`arh_shell` 构造模板可确认主动雷达类。普通 `rocket` 可能是 IR 或 SARH，需进一步证据。型号映射使用明确的内部名，避免前缀或槽位猜测。单回放研究中的 weapon ID 不具有跨回放保证，不能直接全局套用。候选型号若涵盖不同制导类型，则保留 Unknown。

数据来自用户提供的 `handoff-missile-models-cm.md` 及其引用的报告和本地资产，随附 `missile_guidance.json` 记录来源。静态类型不代表某一帧导引头正在锁定、雷达已开机或已进入末制导；原始 seeker 位的动态语义不在本次补全范围内。

## 样例限定推断

用于建表的 `#2026.09.17 13.50.59.wrpl` 的 SHA256 是 `c0fcd26febf9f0ec75765337a841c1d55d089cd13b45740f0cbd41c190f418b2`。仅当完整文件哈希匹配时，表中该样例的 weapon ID 60/157 才可按实际挂载和型号资产证据判为 IR。其他回放不会套用这两个 ID 的结论。

F-2A 的挂载中含 AIM-7M，与“候选全是红外”的旧简表存在冲突，因此不把 143/145/146 强行判为 IR。Su-33 混挂也不能按发射顺序判别。相关反例见 `evidence.md`。
