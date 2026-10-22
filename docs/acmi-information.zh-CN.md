# ACMI 元数据、书签与飞机估算遥测

[English](acmi-information.md)

文件新增地图、模式及回放时间组成的 `Title`、`Author`（默认 `wrpl-acmi`，可用 `-author` 指定）、UTC 导出时间 `RecordingTime`、`Category=War Thunder`、`DataRecorder=wrpl-acmi` 和 `Comments`。备注说明导出器构建版本、请求的地形布局、地理参考提交及估算策略。`ReferenceTime` 仍表示回放时间基准，与文件创建时间分开；原有地理参考字段说明最终使用的地理或 Atlas 布局。

默认 `-bookmarks=true`。时间轴书签覆盖已确认的飞机击毁、导弹发射或首次观测、投弹或首次观测、干扰物投放和雷达目标指定变化。保留原有消息与击毁事件，同一时刻的重复书签会去重。目标指定不能证明 STT 锁定或雷达开关机。使用 `-bookmarks=false` 可关闭新增高亮。

对象新增由明确阵营派生的 `Group`，两种已识别的机场雷达新增显示优先级 `Importance=0.75`；该优先级不代表测得的作战状态。保留原有 `Name`、`CallSign` 和 `Pilot`。[官方 ACMI 文档](https://www.tacview.net/documentation/acmi/en/)建议 `ShortName/LongName/FullName` 由 Tacview 型号数据库提供，`Registration` 则表示机身注册号，因此不把玩家名或编造的制造商信息填入这些字段。

## 飞机估算遥测

```powershell
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi" -estimate-telemetry=false
```

飞机估算遥测现在默认 **开启**；上面的示例使用 `-estimate-telemetry=false` 关闭，省略该参数即保持默认开启。启用后，飞机更新写入原生 `HDG/TAS/Mach/IAS/VerticalGForce/LongitudinalGForce/LateralGForce/AOA`。不改动既有导弹飞行字段和 INS 标签。

- 航向来自记录姿态；TAS 假设无风，使用轨迹速度。Mach 使用分层标准大气，游戏高度近似作为位势高度。IAS 使用 `TAS * sqrt(密度 / 海平面密度)`，属于 **EAS 近似**，并非精确的可压缩流 IAS/CAS；不还原天气和风。
- 三轴 G 使用比力 `(坐标加速度 - 重力) / g`，按记录的航向、俯仰、滚转投影到前、右、上轴。水平匀速飞行为法向 +1G，自由落体为 0G，倒飞匀速为法向 -1G。它与既有导弹坐标加速度模长含义不同。
- AOA 根据无风速度在机体轴上的方向计算；零速或向后飞行时留空。该值不是解码出的气动传感器读数。
- 使用有界短窗口二次拟合抑制采样抖动，不跨越大于两秒的缺口或异常区间。拒绝非有限值、重复或逆序时间、超过 1200 m/s 的速度及超过 100 个坐标 G 的加速度；84.852 km 以上不估算大气。缺失字段写空值清除旧状态。
- 保持飞机坐标、姿态、对象 ID 与击毁时刻。备注明确标注“由位置/姿态推导，非游戏遥测”，不能作为真实座舱读数、机体载荷或气动极限的证据。

## 可选 CSV 诊断输出

轨迹清单（`-list`）、导弹飞行证据（`-missile-flight-log`）、干扰物构造（`-countermeasure-log`）和雷达锁定状态变化（`-radar-lock-log`）默认都不输出。需要哪类报告，就为对应参数显式提供 CSV 路径；只有路径非空时才会写出该文件。

## 静态地面对象

启用地面导出时，没有同步轨迹的建筑或工事可使用成功解码的 ECS 构造 `transform` 平移及实际包时间。必须有明确静态类别证据。身份或型号冲突、UID 回收、非有限或变化的位置、无时间戳、`dummy/dummy_immortal` 任务参照物均不补出；已有同步地面轨迹优先。

接受的对象使用 `Type=Ground+Static+Building`，保留型号和阵营，新增 `WT_GroundPositionSource=ECSConstructionTransform`、`WT_GroundStatic=1`；明确匹配到 ECS 销毁时移除。不因车辆没有轨迹就将其固定在地图上。对象数量取决于回放；已有同步地面轨迹保留。

## 雷达几何与文件版本

`LockedTargetAzimuth` 向右为正，`LockedTargetElevation` 向上为正，均为机体相对角度，单位度。沿用斜距的校验：仅使用事件时刻及之前、最多一秒旧的位置和有效生命周期。解锁、目标销毁或几何无效时清空；切换到有新鲜几何的目标时立即更新。位置重合时距离可为零，但角度无定义。

默认仍为 `FileVersion=2.1`，保留既有兼容基线；可显式使用 `-acmi-version 2.2`。本次字段不要求升级默认格式。已检查输出文本和测试，但没有完成 Tacview 1.9.4/1.9.5 的界面加载或渲染验收，也没有完成另一项地形加载器双版本兼容工作。
