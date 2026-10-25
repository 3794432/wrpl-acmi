# 雷达与发射后导引头状态

[English](ACMI_SENSOR_STATES.md) · [导出指南](acmi-export.zh-CN.md)

`-missile-seeker` 和 `-radar-modes` 默认开启，与导出载机目标指定的
`-radar-locks` 独立。分别使用 `-missile-seeker=false`、`-radar-modes=false`
关闭。默认仍为 ACMI 2.1，读取原生字段和标签不需要额外插件。

## 原生字段与显示

| 信息 | 原生 ACMI 表达 | 限制 |
| --- | --- | --- |
| 雷达开关和指向 | `RadarMode=0/1`、`RadarAzimuth`、`RadarElevation` | 未知状态用 0 清除显示，另以 `WT_RadarMode=unknown` 区分已确认关闭。 |
| 游戏中的具体模式 | `Label`，例如 `TWS MED` | 显示名独立映射，`WT_RadarPattern` 保留原始 ID；不虚构 TWS、STT、PD 的 Tacview 数字枚举。 |
| 雷达导引头跟踪 | `LockedTargetMode=1` 及有效的 `LockedTargetRange` | 距离是导引头报告的估计值，不是几何真值。 |
| 明确 ARH 的搜索／跟踪 | 导弹的 `RadarMode=1` | 只有雷达导引头块不足以证明主动雷达制导。 |
| 导引头详细状态 | 原生 `Label`：`SRC`（搜索）、`TRK`（跟踪）、`N/A`（未知／不可用），追加在原有 INS 标签之后 | IR 块没有已确认的锁定位，计时为 0 不作为锁定证据。 |
| 已确认导弹型号 | 原生 `Name` 等既有命名字段 | 歧义型号保持未确认。 |

已用 Tacview 1.9.4.200 实际批量导入并保存 ACMI 2.1，核对了原生字段及
`WT_*` 来源字段的保留。Tacview 会把数字字段的空赋值规范为 0，因此区分
“未知”和“已关闭／无锁定”需要同时查看来源字段。这验证了文件读取和保存，
不等于所有图形面板已验收。尝试运行本机 1.9.5 的批处理时未生成保存文件，
因此不声称已完成该版本的实际导入验证。

## 数据依据与解释

解析器保留有长度限制的原始导引头块，支持 607 位雷达搜索／跟踪、639 位
捕获过渡和 283 位 IR 布局；未知长度保持不透明。仅输出有限坐标和通过单位
向量合理性检查的视线数据。不通过邻近位置猜目标对象，也不把导弹航迹方向
冒充机体姿态来输出相对角。

载机模式解释目前针对回放版本 `0x00018C1C`。固定版本传感器表覆盖 1,311 个
型号名称／别名和 298 个传感器定义，保留 RWR 的槽位占用及资产中收发机、
扫描模式、信号的原始顺序，排除了 8 个有歧义的型号别名。IR、光学和被动
收发机不会标为主动雷达；存在多个雷达时按确定规则选择一个可解析雷达。
未知状态、槽位切换和缺失记录会清除旧值。不编造扫描量程；波束宽度需有明确
数据才输出，目前内嵌表不提供原生波束宽度。

保留既有运行时位布局。跟踪样本证明天线角应取 `Unk4`／`Unk5`，方位向右
为正、俯仰向上为正。在 `#2026.09.17 13.50.59.wrpl` 的 87 组 F-2A ADTW
样本中，与独立计算的目标几何角中位差约为 0.063° 和 0.048°。这是样本验证，
不是对全部格式版本的保证。

## 型号关联扩展

使用精确父实体的弹药快照，并用一致的导引头类别缩小显式型号别名的候选。
只有唯一且高置信度的候选才确定型号；任何未识别的正数量挂载都会阻止
“只识别到一个型号就认为唯一”。混挂歧义、冲突和低置信度别名不赋予型号，
保留已有身份。回放内传播受精确 owner、generation、WeaponID、模板和武器
类别约束，不使用发射顺序、最近对象或跨回放数字 ID 映射。目前别名表支持
空空导弹，新关联层不会给 AGM 指定型号。

## 来源与重建

以后可修改 [`cmd/acmi/data/radar_mode_labels.json`](../cmd/acmi/data/radar_mode_labels.json)
调整雷达显示名，然后重新编译 EXE、重新导出。这份 105 项显示表与传感器配置表
独立；未映射的名称回退到原始 ID。标签不带 `Radar` 前缀和 `(mprf)`／`(hprf)` 收发机后缀，
原始收发机信息保留在 `WT_RadarTransceiver` 中。

- [WT Knowledge Docs 逐帧状态](https://furro404.github.io/WT_KNOWLEDGE_DOCS/03-replays/#24-per-tick-unit-state)
- [Tacview 官方 ACMI 规范](https://raia-software-inc.gitbook.io/tacview/technical-documentation/acmi-telemetry-file-format)
- [固定版本 War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine/tree/f7952e4291e9fd7b6fe38e84020dddef925c3457)

静态表已内嵌，正常构建和运行不需要网络或 Datamine 目录。主动更新时可在源码仓库根目录运行：

```powershell
python tools/radar-modes/build_radar_modes.py
python -m unittest discover -s tools/radar-modes -p "test_*.py"
```

生成器下载固定版本输入并保留顺序。游戏资产更新可能改变索引含义，仅索引
未越界不代表其他资产版本也兼容。
