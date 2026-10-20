# 导弹型号 ID 补全

导出方式不变，继续把 WRPL 拖到 `run.bat`。数字 `WT_WeaponID` 是回放里的武器索引，不是跨所有回放通用的型号编号。

## 识别来源

1. 保留原来的击杀报告关联：根据杀手、导弹结束时间和同回放 `weaponID + template` 建立型号证据。
2. 新增精确内部名别名，扩展红外、半主动雷达导弹的击杀记录识别；不按字符串前缀或发射顺序猜测。
3. 特定回放的 ID 表只在 WRPL 完整 SHA256 匹配时启用。高置信条目可以补具体型号，中低置信只写候选，原有击杀型号优先。

手工 `-names` 仍具有最高显示优先级。型号补全不改变飞行轨迹、INS 标签、干扰物或地图位置。

## 开关和外部表

```powershell
./wrpl-acmi.exe -in "D:\回放.wrpl" -out "D:\回放.acmi"
./wrpl-acmi.exe -missile-models off -in "D:\回放.wrpl"
./wrpl-acmi.exe -missile-models "D:\missile_models.json" -in "D:\回放.wrpl"
```

`-missile-models off` 只关闭本次补全，保留先前的击杀型号识别和制导信息功能。外部表必须符合版本和枚举校验；普通数字 ID 不能脱离其回放 SHA 添加为全局规则。

## 不确定性

F-2A 的可见挂载含 AIM-7M，不能把所有普通 rocket 直接当 IR。Su-33 的混挂不能仅凭数量或发射次序在 R-73、R-27ET、R-27ER 间强行命名。JAS39D/F-15E 的未确证项目采用保守候选。

ACMI 的 `WT_MissileModelSource`、`WT_MissileModelConfidence` 及候选/冲突属性用于核对证据。新增自定义属性不保证 Tacview 标准遥测面板提供对应专用仪表。

## 本次结果

研究样例的具体型号从 74/104 提高到 81/104（约 77.9%）：ID 35 → PL-12（1 枚）、60 → PL-5E II（1 枚）、69 → AIM-120A（4 枚）、157 → AAM-3（1 枚）。余下 23 枚保留候选：10 枚 medium、13 枚 low。

现有 `2026.09.19 16.33.36example.wrpl` 不属于样例哈希，仍通过新增的通用击杀内部名识别多得到 1 枚 AAM-3（该回放的 WeaponID 是 39），总型号覆盖从 10/106 提高到 11/106。这也说明不能把不同回放的数字 ID 当作同一型号。

两份回放共 130,558 条位置记录通过比较，没有非预期数据变动。`-missile-models off` 的样例 ACMI 和 CSV 与本次改动前逐字节一致；错误 SHA 的外部表未套用样例型号行。
