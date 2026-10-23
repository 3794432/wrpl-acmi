# 导弹型号数据表与研究依据

[型号识别说明](../ACMI_MODELS.md) · [制导证据](missile-guidance.md)

本报告记录特定回放的型号推断、置信度及反例。结论限定在文中给出的样例哈希下，不能作为全局 `WT_WeaponID` 映射。

文中的 `work/`、`report/` 和 `deliverable/` 路径指向原始研究产物，未必包含在当前仓库中，也不是构建依赖。运行所需 JSON 已内嵌在 `cmd/acmi/data/`。

## 数据与历史研究文件

- 仓库数据表：[missile_models.json](../../cmd/acmi/data/missile_models.json)。
- 原始报告路径：`work/missile-model-completion/evidence.md`。
- 原始复现脚本：`work/missile-model-completion/build_missile_models.mjs`。

该项研究使用已有本地证据建立数据表，未重新下载完整资料库，也未运行 diagnose 或修改 Go 实现。以下结果属于当时的数据分析记录。

## 样例与输入

样例 SHA256：

`c0fcd26febf9f0ec75765337a841c1d55d089cd13b45740f0cbd41c190f418b2`

原始脚本校验 `probe-output.json` 中的 104 枚导弹是否覆盖 25 个 ID，并要求数据表恰好包含这 25 个 ID。研究输入的 SHA-256 如下，供持有原始文件时复核：

| 输入 | SHA256 |
|---|---|
| missile-name-map.csv | 033497a1f6e961f9891d718a56553153b143cc91706303178ecfd73f1f4961ee |
| probe-output.json | cc5811c3ee592bf1485ecb7645c86008d5dd38bcdf67ad3a794886adbb4ae924 |
| unit-storage-fields.json | a77859b47068828e7f66ea8e5a0372b5b8fe2040f4ddc4e4c3a7ee4e8fe76c92 |
| outputs/missile-guidance/missile_guidance.json | 4be1a3c447473a681f2722d7e981de0e049ba7f6e473b1168feea6fc964a18a9 |

## 数据契约

JSON 使用 schema 1，顶层结构为：

```text
schema
generated_from[]
aliases{internal_name: {model, internal_name, confidence, source}}
replays{SHA: {weapon_ids{ID: {model, internal_name, confidence, source, candidates}}}}
```

weapon ID 条目的置信度只允许 `high`、`medium` 或 `low`。`high` 条目提供可写入的型号；`medium` 和 `low` 条目的 `model` 留空，仅提供 `candidates`，以免将候选当作确定结论。`aliases` 只允许 `confirmed` 或 `high`；本表使用制导数据中已确认的内部名及其精确击杀别名。

## 样例结果

25 个 ID 全部写入样例作用域。按导弹数量：

| 置信度 | ID 数 | 导弹数 | 处理 |
|---|---:|---:|---|
| high | 15 | 81 | 11 个既有击杀确证 ID 不动；新增 35/60/69/157 共 7 枚 |
| medium | 3 | 10 | 53/99/159，仅保留单个候选，不写入确定型号 |
| low | 7 | 13 | 保留多个候选，不写入确定型号 |

相对于研究前的基线，新增确定型号涉及 4 个 ID、7 枚导弹，覆盖数由 74/104 增至 81/104。制导分类仍为 90 枚 ARH、2 枚 IR 和 12 枚 Unknown。

## 逐 ID 证据

下表保留原始分析中的模板标签，其中 `IR` 是研究表中的旧标签，不能视为已确认的制导类型。普通 rocket 模板也可能对应 SARH；最终制导结论应以[制导证据报告](missile-guidance.md)为准。

| ID | 枚 | 模板 | 载机 | 写入型号 | 置信 | 候选 | 关键依据 |
|---:|---:|---|---|---|---|---|---|
| 32 | 10 | ARH | J-11B | PL-12 | high | — | kill `cn_pl12` |
| 35 | 1 | ARH | J-11B | PL-12 | high | — | uid414 field67 只有 `pl12_slot3/4/5/6/7/8` 这一组 ARH 槽；同机 ID32 已由击杀确证 |
| 41 | 14 | ARH | Su-30MK2V | R-77 (RVV-AE) | high | — | kill `su_rvv_ae` |
| 53 | 2 | ARH | JAS39D | 空 | medium | RB 99 | uid431 field67 有 `rb99_bol/rb99_default_bol`，但没有 ID 到槽的直接绑定 |
| 60 | 1 | IR | J-10A | PL-5E II | high | — | uid436 field67 有 `pl5e_2/pl5e_2_default`；该行是非 ARH，guidance 映射 `cn_pl5e2` 为 PL-5E II |
| 61 | 12 | ARH | J-10A | PL-12 | high | — | kill `cn_pl12` |
| 69 | 4 | ARH | F-16C Blk50 | AIM-120A | high | — | uid423 field67 只有 `aim_120a_default/aim_120a_wingtip_slot1/aim_120a_wingtip_slot10` 这一组 ARH 槽 |
| 70 | 2 | ARH | F-16C Blk50 | AIM-120A | high | — | kill `us_aim_120a_default` |
| 85 | 22 | ARH | Su-30MKK | PL-12 | high | — | kill `cn_pl12` |
| 94 | 2 | IR | Su-33 | 空 | low | R-27ET / R-73 / R-27ER | uid427 field67 混有 `r73/r27er/r27et`，不能按发射顺序分配 |
| 95 | 3 | IR | Su-33 | 空 | low | R-27ET / R-73 / R-27ER | uid425/427 field67 混挂 R-73 与 R-27ER，uid427 另有 R-27ET |
| 96 | 1 | IR | Su-33 | 空 | low | R-27ET / R-73 / R-27ER | 同上；模板与顺序均不能唯一定型 |
| 99 | 6 | ARH | F-15E | 空 | medium | AIM-120C-5 | uid435 field67 是空的 8 bit；field51 的 `us_aim_120b_c` 与同机 ID100 确证不足以绑定 ID99 |
| 100 | 2 | ARH | F-15E | AIM-120C-5 | high | — | kill `us_aim_120c_5_default` |
| 122 | 1 | ARH | JAS39C | RB 99 | high | — | kill `swd_rb99_default` |
| 133 | 3 | ARH | F-16A MLU | AIM-120A | high | — | kill `us_aim_120a` |
| 134 | 2 | ARH | F-16A MLU | AIM-120A | high | — | kill `us_aim_120a_default` |
| 137 | 1 | ARH | Su-30MKK | 空 | low | PL-12 / R-77 (RVV-AE) | uid417 field67 同时有 `pl12_slot6/7` 与 `r77_slot3/5/8/10` |
| 143 | 2 | IR | F-2A | 空 | low | AIM-9L / AIM-9M / AAM-3 / AIM-7M | uid410 field67 含 `jp_aam3` 和 `aim_7m`；field51 还列有 AIM-9L/M/AAM-3 |
| 145 | 2 | IR | F-2A | 空 | low | AIM-9L / AIM-9M / AAM-3 / AIM-7M | 同上，无法逐 ID 绑定 |
| 146 | 2 | IR | F-2A | 空 | low | AIM-9L / AIM-9M / AAM-3 / AIM-7M | 同上，无法逐 ID 绑定 |
| 155 | 2 | ARH | J-10A | PL-12 | high | — | kill `cn_pl12_default` |
| 157 | 1 | IR | F-15J Kai | AAM-3 | high | — | uid416 field67 有 `aam3_slot1/aam3_slot11`；guidance 确证 `jp_aam3` 为 IR |
| 159 | 2 | ARH | F-15J Kai | 空 | medium | AIM-120A | uid416 field67 同时有 4 个 AAM-4 槽和 2 个 AIM-120A 槽；计数与 160/159 相符，但仍不是 ID 到槽的直接绑定 |
| 160 | 4 | ARH | F-15J Kai | AAM-4 | high | — | kill `jp_aam4` |

## 内部名别名

`aliases` 共 18 项：14 个 guidance 中 `confidence=confirmed` 的内部名，加 4 个击杀报告实际出现的精确 `_default` 变体。

| 内部名 | 型号 | 导引 |
|---|---|---|
| cn_pl12 | PL-12 | ARH |
| su_rvv_ae | R-77 (RVV-AE) | ARH |
| us_aim_120a | AIM-120A | ARH |
| us_aim_120c_5 | AIM-120C-5 | ARH |
| jp_aam4 | AAM-4 | ARH |
| swd_rb99 | RB 99 | ARH |
| jp_aam3 | AAM-3 | IR |
| pl_5e2 / su_pl5e2 | PL-5E II | IR |
| us_aim9l_sidewinder | AIM-9L | IR |
| us_aim9m_sidewinder | AIM-9M | IR |
| su_r_27et | R-27ET | IR |
| su_r_73 | R-73 | IR |
| su_r_27er | R-27ER | SARH |
| cn_pl12_default | PL-12 | ARH，击杀原文变体 |
| us_aim_120a_default | AIM-120A | ARH，击杀原文变体 |
| us_aim_120c_5_default | AIM-120C-5 | ARH，击杀原文变体 |
| swd_rb99_default | RB 99 | ARH，击杀原文变体 |

别名是独立于样例 ID 的击杀报告查表项；它不含 weapon ID 或槽位推导。没有纳入 guidance 中 `high` 的载机别名，也没有加入任何未确证的内部名。

## 复核结论与反例

- 35/60/69/157 可以升级：其载机 field67 的实际槽位在对应模板下只剩一个型号族，并且 ID 已由回放中的模板与同机证据交叉限制。
- 53 不升级为 high：field67 有 RB99 槽，但没有任何数值 weapon ID 与槽位直接相连；保留 medium 单候选。
- 99 不升级为 high：uid435 的 field67 为空。同机 ID100 确证 AIM-120C-5，不能替 ID99 完成绑定。
- 159 不升级为 high：4 枚 AAM-4 与 2 枚 AIM-120A 的计数吻合只是数量证据，不能证明哪一组 ID 对应哪个槽。
- F-2A 是明确反例：field67 同时含 `jp_aam3` 与 `aim_7m`，因此不能把 143/145/146 一概写成 IR，也不能按旧 CSV 的单一型号顺序赋值。
- Su-33 同机混挂 R-73、R-27ER、R-27ET；94/95/96 保留候选集合，拒绝按发射顺序推断。
- Su-30MKK 同机混挂 PL-12 与 R-77；ID 137 的证据不足以确定型号。
- 不把本表应用到其他 SHA，不把 `WeaponRef`、槽位序号或载机型号当作全局 weapon ID 映射。

## 历史验证记录

原始研究环境使用以下命令生成并校验数据表。脚本及其输入未随本报告完整发布，因此这不是当前仓库可直接执行的构建步骤：

`node work/missile-model-completion/build_missile_models.mjs`

当时的输出包含 25 个 ID、18 个别名；按导弹数量统计，high、medium、low 分别为 81、10、13 枚。脚本同时检查总计 104 枚导弹及 25 个 ID 的覆盖一致性，JSON 仅包含指定 SHA 的一个回放作用域。这些结果不表示当前环境重新执行过该研究。
