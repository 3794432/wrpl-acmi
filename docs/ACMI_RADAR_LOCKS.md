# Tacview 雷达目标指定显示

默认将 War Thunder FM/AircraftSmall 同步中的目标指定写成 Tacview 原生锁定属性。日常使用 `run.bat` 或拖放回放的方式保持不变；重新导出 ACMI 后，在 Tacview 中打开 **Display Radar Locks（显示雷达锁定）** 即可查看连线。

```bat
wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi" -radar-lock-log "locks.csv"
wrpl-acmi.exe -in "replay.wrpl" -out "without-locks.acmi" -radar-locks=false
```

`-radar-locks` 默认开启；`-radar-lock-log` 为可选的状态变化 CSV。CSV 的 `time_sec` 使用原始回放包时间（秒），而 ACMI 的 `#` 是减去导出起点后的相对时间。CSV 中包含源机、目标 UID、ACMI 对象 ID、名称和主目标斜距。

数据只取完整解码的 `TargetsData` 中 `kind=6`、带有 `0xFFFF0000 | uid` 的记录，关联到同一回放中实际存在的飞机。它表示目标指定/跟踪集合，可能包括 TWS，不能据此确认 STT 硬锁、雷达工作模式或武器导引状态。

- 主目标写入 `LockedTarget`，其余目标写入 `LockedTarget2` 至 `LockedTarget9`；当前 FM 格式最多携带 8 条指定。
- 有有效目标时写 `LockedTargetMode=1`，移除目标时清空对应槽位，完全解除时写 `LockedTargetMode=0` 和 `LockedTargetRange=0`。
- `LockedTargetRange` 为双方本地坐标算出的斜距，单位米；使用当前或过去 1 秒内的位置，不从未来位置估算。
- 完整的空列表表示解除；缺失或解码失败的列表不冒充空列表。未知单位、自指、重复指定，以及尚未出现或已销毁的目标不输出。
- 目标销毁时，即使源机在该时刻没有位置同步，也会增加必要的清锁行。关闭雷达导出后，输出与本次修改前的工具保持一致。
- 保留既有飞机、导弹、炸弹、干扰物、地形布局和不完整服务器回放功能。

实现参考用户提供的 `deliverable/radar-locks/README.md` 与补丁，并针对当前源码整合；采用显式导出上下文，避免重复导出时沿用上一次的锁定状态。

格式依据：[Tacview 官方 ACMI 规范](https://raia-software-inc.gitbook.io/tacview/technical-documentation/acmi-telemetry-file-format)。验证覆盖单元测试、实际回放结构、目标生命周期，以及开启/关闭功能时的原有数据对比；未进行 Tacview 界面渲染验收。
