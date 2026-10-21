# 炸弹导出

转换器自动导出炸弹，无需新增命令参数。Tacview 对象类型使用 `Weapon+Bomb`，尚未确定型号时名称为 `Bomb`。

- 存在 class-1 WeaponSync 时，导出记录中的真实位置序列，不合成姿态、制导或 INS 字段。
- 只有 Bomb 构造记录时，导出已知的投放位置标记，标签为 `Release position only`。该标记停留在投放位置，不代表炸弹实际停在空中，也不代表已经还原落弹轨迹。
- 有明确销毁时间则按该时间移除；只有投放位置而无销毁时间时，以当前记录末尾作为显示截止，不推算命中时刻。
- 父机仅来自构造记录中的 owner 与实际飞机 UID 的关联。没有关联证据的轨迹保持中立，不用距离猜测归属。
- `WT_BombPositionSource=WeaponSync` / `Construction` 区分位置来源；`WT_BombTrajectoryAvailable=1` / `0` 区分真实位置序列与单个投放位置。
- `-list` CSV 中 kind 为 `bomb`；`-names` 支持 `bomb:<id>` 名称覆盖。

本次 `.swrpl-test` 的 25 个分片中，识别出 18 条 Bomb 构造记录及其销毁时间，没有识别到 ShellsData / ShellsDataServerReplay 的位置同步。因此生成的 `.swrpl-test.partial.acmi` 包含 52 架飞机和 18 个炸弹投放位置标记；首个标记从相对时间 `+00:05:13.667` 开始。原有飞机数据保持一致，缺少回放尾部的状态也保持不变。

验证：相关 Go 单元测试通过。该服务器目录在 Windows Job 的 2 GiB 硬限制下导出成功，峰值进程 Commit 为 326,352,896 字节。客户端回归样本增加 20 个炸弹对象，其中 4 个有实际位置序列、16 个只有投放标记；既有飞机、导弹和干扰物逐对象数据保持一致。

类型依据：[Tacview 官方 ACMI 格式规范](https://raia-software-inc.gitbook.io/tacview/technical-documentation/acmi-telemetry-file-format)。
