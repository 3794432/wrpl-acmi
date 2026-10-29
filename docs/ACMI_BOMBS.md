# 炸弹导出

[导出指南](acmi-export.zh-CN.md)

炸弹自动参与导出，无需单独开启。ACMI 对象类型为 `Weapon+Bomb`，型号尚未确定时显示为 `Bomb`。优先导出真实同步位置；仅有投放位置时默认尝试生成明确标记的重力估算轨迹。见[炸弹型号](acmi-information.zh-CN.md#炸弹型号)及[运动估算](acmi-information.zh-CN.md#炸弹运动估算)。

## 位置与生命周期

| 回放中的信息 | 导出方式 |
| --- | --- |
| 存在 class-1 WeaponSync | 导出记录的实际位置序列，不合成姿态、制导或 INS 字段。 |
| 只有 Bomb 构造记录，载机初速有效 | 默认显示 `Estimated ballistic trajectory`，使用载机初速与重力估算，不代表实际轨迹。 |
| 初速无效，或 `-estimate-bomb-trajectories=false` | 在已知投放位置显示 `Release position only` 标记。 |
| 有明确销毁时间 | 真实轨迹和静态标记按该时刻移除；估算显示也可能因海平面或时长限制提前结束，保留原始销毁时间字段。 |
| 只有投放位置且没有销毁时间 | 以当前记录末尾作为显示截止，不推算命中时刻。 |

投放位置标记会停留在原处，这只表示已知的释放地点，不表示炸弹实际悬停在空中。

父机关联只使用构造记录中的 owner 与实际飞机 UID。没有关联证据时，轨迹保持中立，不以距离推断归属。

## 来源字段与名称

`WT_BombPositionSource=WeaponSync` 或 `Construction` 标明位置来源；`WT_BombTrajectoryAvailable=1` 或 `0` 区分实际位置序列与投放位置标记。

估算使用 `WT_BombPositionSource=EstimatedBallistic` 和 `WT_BombTrajectoryEstimated=1`；`WT_BombTrajectoryAvailable=0` 保留“没有真实同步轨迹”的含义。估算不模拟空气阻力、风、减速伞或制导，海平面截止也不是真实落点。

在 `-list` 生成的 CSV 中，炸弹的 kind 为 `bomb`。`-names` 支持通过 `bomb:<id>` 覆盖显示名称。对象类型依据 [Tacview 官方 ACMI 格式规范](https://raia-software-inc.gitbook.io/tacview/technical-documentation/acmi-telemetry-file-format)。

## 历史样例验证

一组标记为 `.swrpl-test` 的 25 分片样例包含 18 条 Bomb 构造记录及销毁时间，但未识别到 ShellsData 或 ShellsDataServerReplay 位置同步。导出结果包含 52 架飞机和 18 个投放位置标记，首个标记出现于相对时间 `+00:05:13.667`，回放仍标注为缺少尾部。

当时相关 Go 单元测试通过；该目录在 2 GiB Windows Job 限制下完成导出，峰值进程 Commit 为 326,352,896 字节。客户端回归样例增加 20 个炸弹对象，其中 4 个有实际轨迹、16 个只有投放标记，原有飞机、导弹和干扰物的逐对象数据保持一致。

这些结果说明已检查样例中的导出行为，不代表其他回放具有相同的位置同步覆盖率。
