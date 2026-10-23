# 导弹制导数据表与研究依据

[制导信息说明](../ACMI_GUIDANCE.md) · [型号证据](missile-models.md)

本报告说明静态制导类型的判定依据、数据格式和样例内推断。文中的 `work/`、`report/` 和 `deliverable/` 路径是历史研究记录，未必存在于当前仓库，也不是构建依赖。运行所需 JSON 已内嵌在 `cmd/acmi/data/`。

原始研究范围限于制导数据与证据整理，不包含 Go 实现修改、干扰物分类、新探针或 diagnose 运行。以下样例统计和验证记录均对应当时的研究环境。

## 数据与历史研究文件

- 仓库数据表：[missile_guidance.json](../../cmd/acmi/data/missile_guidance.json)。
- 仓库证据表：[datamine-guidance.csv](datamine-guidance.csv)。
- 原始报告及证据路径：`work/missile-guidance/evidence.md`、`work/missile-guidance/datamine_guidance.csv`。
- 原始复现脚本：`work/missile-guidance/analyze_sample.mjs`、`extract_datamine.mjs`。

## 数据契约

JSON 使用 schema 1，包含 `schema`、`generated_from`、`models` 及可选的 `replays`。`models` 以内部武器名为键，条目包含 `guidance`、`source` 和 `confidence`。制导类型仅允许 ARH、IR、SARH，置信度仅允许 confirmed、high。

样例专用的 `replays` 条目严格绑定完整 SHA-256，仅包含 ID 60 和 157，均为 IR、high。哈希不匹配时不应用这些条目。

## 判定规则

原始来源为 `gszabi99/War-Thunder-Datamine@master` 下的 `aces.vromfs.bin_u/gamedata/weapons/rocketguns/*.blkx`。研究时本地部分克隆缺少 blob，因此使用了原始 JSON 快照。该研究没有以当前 master 状态作为复核依据，应使用[证据表](datamine-guidance.csv)中的逐文件 SHA-256 核对所用快照。

- radar 且 radarSeeker.active=true：ARH。
- radar 且 radarSeeker 无 active：SARH。已用 su_r_27r 和 us_aim7m_sparrow 交叉核对。
- AAM 的 optical 且存在 opticalSeeker：IR。只对下列已确认 AAM 套用，不外推到其他 optical 武器。
- rocket 模板本身不等于 IR；rocket+arh_shell 只证明 ARH。

## 型号级映射

| 型号 | 内部名键 | guidance | 证据 | 置信 |
|---|---|---|---|---|
| PL-12 | cn_pl12 | ARH | cn_pl12.blkx active radar seeker；击杀 cn_pl12 | confirmed |
| R-77 (RVV-AE) | su_rvv_ae | ARH | su_rvv_ae.blkx active radar seeker；击杀 su_rvv_ae | confirmed |
| AIM-120A | us_aim_120a | ARH | us_aim_120a.blkx active radar seeker；击杀 us_aim_120a | confirmed |
| AIM-120C-5 | us_aim_120c_5 | ARH | us_aim_120c_5.blkx active radar seeker；击杀 us_aim_120c_5 | confirmed |
| AAM-4 | jp_aam4 | ARH | jp_aam4.blkx active radar seeker；击杀 jp_aam4 | confirmed |
| RB 99 | swd_rb99 | ARH | swd_rb99.blkx active radar seeker；击杀 swd_rb99 | confirmed |
| AAM-3 | jp_aam3 | IR | jp_aam3.blkx optical seeker | confirmed |
| PL-5E II | pl_5e2 | IR | su_pl5e2.blkx bulletName pl_5e2，optical seeker | confirmed |
| AIM-9L | us_aim9l_sidewinder | IR | us_aim9l_sidewinder.blkx optical seeker | confirmed |
| AIM-9M | us_aim9m_sidewinder | IR | us_aim9m_sidewinder.blkx optical seeker | confirmed |
| R-27ET | su_r_27et | IR | su_r_27et.blkx optical seeker | confirmed |
| R-73 | su_r_73 | IR | su_r_73.blkx optical seeker | confirmed |
| R-27ER | su_r_27er | SARH | su_r_27er.blkx radar seeker 无 active；与 R-27R/AIM-7M 同型 | confirmed |

表中的载机别名键（cn_pl5e2、su_pl5e2、us_aim_9l、us_aim_9m）也在 JSON 中保留，标为 high，便于载机字段直接命中；它们不影响上述 13 个 confirmed 型号的制导结论。

## 样例覆盖

样例回放 SHA256 为 c0fcd26febf9f0ec75765337a841c1d55d089cd13b45740f0cbd41c190f418b2。

probe-output.json 统计：104 枚导弹，25 个 weaponID；rocket+arh_shell 90 枚，rocket 14 枚。

- 90 枚 ARH：模板直接给出 ARH；有击杀内部名的弹由 models 表补充来源。
- 2 枚 IR：weaponID 60（J-10A，1 枚）与 157（F-15J，1 枚），由 replay-scoped 表解析。
- 12 枚 Unknown：F-2A 143/145/146 共 6 枚（field 67 出现 aim_7m，撤回）+ Su-33 94/95/96 共 6 枚（混合挂载）。

| weaponID | 数量 | 载机 | source | confidence |
|---|---:|---|---|---|
| 60 | 1 | j_10a uid436 | probe-output 预设 pl5e_2；datamine su_pl5e2 opticalSeeker；report J-10A IR=PL-5E II | high |
| 157 | 1 | f_15j_kai uid416 | probe-output 预设 aam3_slot11/aam3_slot1；datamine jp_aam3 opticalSeeker | high |

## 样例内 ID 的判定

| weaponID | 数量 | 载机 | 证据 | 结论 |
|---|---:|---|---|---|
| 60 | 1 | j_10a uid436 | 实际预设含 pl5e_2，对应 su_pl5e2.blkx optical seeker | 已写入 replays，IR high |
| 157 | 1 | f_15j_kai uid416 | 实际预设含 aam3_slot11、aam3_slot1；jp_aam3.blkx optical seeker | 已写入 replays，IR high |
| 143/145/146 | 各 2 | f_2a_adtw uid410 | CSV 候选 AIM-9L/AIM-9M/AAM-3 均为 IR；但 field 67 预设同时出现 aim_7m（SARH） | 撤回，不写；保持 Unknown |
| 94/95/96 | 2/3/1 | su_33 uid427/425 | 挂载混合 r73、r27er、r27et；rocket 模板无区分 | Unknown，不按旧顺序推 SARH |

60/157 有单载机实际预设与 datamine 导引头双重证据，已写入；F-2A 的 aim_7m 与“6 枚全 IR”统计冲突，143/145/146 撤回；Su-33 的三种弹混挂无法用模板或发射顺序区分，94/95/96 保持 Unknown。

## 历史验证记录

以下为原始研究环境中的执行结果。相关脚本、回放和工具链路径并未完整随仓库提供，不能将其视为当前检出的测试状态。

- node work/missile-guidance/analyze_sample.mjs 已确认 104 = 90 ARH + 2 IR + 12 Unknown，25 个 weaponID。
- go test ./cmd/acmi 通过，使用工作区 work/toolchain/go，GOCACHE/GOPATH 指向工作区。
- JSON 自检通过：schema 1，generated_from 为数组，models 的 guidance/confidence 枚举合法，replays 只有 60/157 两行。

## 结论的适用范围

1. optical 在 War Thunder 中也用于 TV 武器；本表只对上述 AAM 型号解释为 IR。
2. datamine 使用 master 快照，版本漂移时以 datamine_guidance.csv 的 SHA256 复核。
3. 载机字段名与 datamine bulletName 存在别名差异；未命中时应补别名，不应猜型号。
4. F-2A 的 field 67 出现 aim_7m，不能把 143/145/146 一律写成 IR。
5. Su-33 的 94/95/96 保留 Unknown，R-27ER 的 SARH 只作为模型级证据，不绑定到具体回放 ID。
