# 雷达目标指定显示

[导出指南](acmi-export.zh-CN.md) · [元数据与几何字段](acmi-information.zh-CN.md)

导出器默认将 War Thunder FM/AircraftSmall 同步中的目标指定写入 Tacview 原生锁定属性。重新导出 ACMI 后，在 Tacview 中启用 **Display Radar Locks（显示雷达锁定）** 可查看对应连线。

这些记录表示目标指定或跟踪集合，可能包括 TWS，不能单独证明 STT 锁定、雷达工作模式或武器导引状态。

## 使用与诊断

`-radar-locks` 默认开启。`-radar-lock-log` 接受可选 CSV 路径，便于检查状态变化；关闭导出时使用 `-radar-locks=false`。

```powershell
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi" -radar-lock-log "locks.csv"
.\wrpl-acmi.exe -in "replay.wrpl" -out "without-locks.acmi" -radar-locks=false
```

CSV 包含源机、目标 UID、ACMI 对象 ID、名称及主目标斜距。`time_sec` 使用原始回放包时间，单位为秒；ACMI 的 `#` 时间则减去了导出起点。比较两者时应先对齐时间基准。

## 哪些记录会被采用

导出器只使用完整解码的 `TargetsData` 中 `kind=6`、目标标识形式为 `0xFFFF0000 | uid` 的记录，并关联到同一回放中实际存在的飞机。

未知单位、自指、重复指定，以及尚未出现或已销毁的目标不会输出。完整的空列表表示解除指定；列表缺失或解码失败不能视为空列表。

## 输出字段与状态更新

| 字段 | 含义 |
| --- | --- |
| `LockedTarget` | 主目标的 ACMI 对象引用。 |
| `LockedTarget2` 至 `LockedTarget9` | 其他目标槽位；当前 FM 格式最多携带 8 条指定。 |
| `LockedTargetMode` | 有有效目标时为 1，全部解除时为 0。 |
| `LockedTargetRange` | 双方本地坐标计算的斜距，单位为米；全部解除时写 0。 |
| `LockedTargetAzimuth` | 机体相对方位角，向右为正，单位为度。 |
| `LockedTargetElevation` | 机体相对俯仰角，向上为正，单位为度。 |

距离和角度只使用事件时刻或之前最多一秒的有效位置，不从未来位置推算。双方位置重合时可保留零距离，但不输出角度。

移除目标时会清空对应槽位。解锁、目标销毁或几何无效时，相应几何字段也会清空；即使源机在目标销毁时刻没有位置同步，仍会写出必要的清除记录。默认普通事件消息包含目标、有效时的主目标几何距离及指定/清除时刻，包括没有源机位置同步的清除时刻。这不证明 STT 锁定。`-radar-locks=false` 关闭原生属性；若还要关闭雷达消息，应同时从 `-events` 中去掉 `radar`。高亮书签默认关闭，可用 `-bookmarks=true` 开启。

## 实现与验证范围

锁定状态保存在每次导出的上下文中，避免重复导出沿用上一份回放的状态。原始整合依据包括历史交付材料 `deliverable/radar-locks/README.md` 及补丁；该路径是研究记录，不是仓库运行依赖。

格式依据为 [Tacview 官方 ACMI 规范](https://raia-software-inc.gitbook.io/tacview/technical-documentation/acmi-telemetry-file-format)。既有验证覆盖单元测试、实际回放结构、目标生命周期，以及开关功能前后的数据比较，尚未完成 Tacview 界面渲染验收。
