# ACMI 元数据、书签与飞机遥测

[English](acmi-information.md) · [导出指南](acmi-export.zh-CN.md)

本页说明文件级信息、事件书签和飞机遥测字段的含义，并介绍静态地面对象及雷达几何的来源规则。位置和姿态来自回放；由这些记录计算的遥测值会注明估算依据。

## 文件信息与对象分组

| 字段 | 含义 |
| --- | --- |
| `Title` | 由地图、模式和回放时间组成的标题。 |
| `Author` | 作者或操作员，默认 `wrpl-acmi`，可用 `-author` 指定。 |
| `RecordingTime` | UTC 导出时间。 |
| `ReferenceTime` | 回放时间基准，与文件创建时间分开记录。 |
| `Category` | `War Thunder`。 |
| `DataRecorder` | `wrpl-acmi`。 |
| `Comments` | 构建版本、请求的布局、地理参考版本及估算策略等说明。 |

地理参考字段记录最终使用的 geographic 或 Atlas 布局。对象的 `Group` 根据明确的阵营信息生成；已识别的两类机场雷达使用 `Importance=0.75` 设置显示优先级，该值不表示作战状态。

`Name`、`CallSign` 和 `Pilot` 保留各自用途。按照 [Tacview ACMI 文档](https://www.tacview.net/documentation/acmi/en/)的字段约定，`ShortName`、`LongName` 和 `FullName` 由型号数据库提供，`Registration` 表示注册号，因此导出器不会用玩家名或未经证实的制造商信息填充这些字段。

## 事件书签

书签默认开启，涵盖已确认的飞机击毁、导弹发射或首次观测、炸弹投放或首次观测、干扰物释放，以及雷达目标指定变化。同一时刻的重复书签会去重；原有消息与击毁事件仍保留。

使用 `-bookmarks=false` 可关闭这些附加书签。雷达指定书签仅表示目标指定发生变化，不能据此确认 STT 锁定或雷达开关状态。

## 飞机估算遥测

估算默认开启，使用 `-estimate-telemetry=false` 可关闭：

```powershell
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi" -estimate-telemetry=false
```

估算功能不修改飞机坐标、姿态、对象 ID 或击毁时刻，也不改变导弹飞行字段和 INS 标签。

### 计算方法

| 字段 | 方法与限制 |
| --- | --- |
| `HDG` | 从记录姿态取得航向。 |
| `TAS` | 在无风假设下使用轨迹速度。 |
| `Mach` | 采用分层标准大气，将游戏高度近似视为位势高度。 |
| `IAS` | 使用 `TAS * sqrt(rho / rho0)`，即 EAS 近似；不是精确的可压缩流 IAS/CAS，也不是座舱读数。 |
| `AOA` | 根据无风速度在机体坐标系中的方向计算；零速或向后飞行时留空。 |
| 三轴 G | 将比力 `(坐标加速度 - 重力) / g` 投影到机体的前、右、上轴，写入纵向、横向和法向 G 字段。 |

三轴 G 的物理含义与导弹坐标加速度模长不同。例如，水平匀速飞行的法向值为 +1G，自由落体为 0G，倒飞匀速为 -1G。计算没有还原风场、天气或完整气动模型，不能用作真实机体载荷和气动极限的证据。

### 样本筛选与空值

计算使用有界短窗口二次拟合，以降低采样抖动，不跨越超过两秒的缺口或异常区间。非有限数值、重复或逆序时间、超过 1,200 m/s 的速度，以及超过 100 个坐标 G 的加速度均会被拒绝；84.852 km 以上不进行大气估算。

缺失或无效字段以空值写出，清除此前状态。输出备注会说明这些数据由位置和姿态推导，并非直接记录的游戏遥测。

## 静态地面对象

启用地面导出时，没有同步轨迹的建筑或工事可使用 ECS 构造 `transform` 的平移分量和实际包时间，但必须同时具有明确的静态类别证据。已有同步轨迹始终优先。

身份或型号冲突、UID 回收、无时间戳、非有限坐标、位置变化，以及 `dummy/dummy_immortal` 任务参照物均会被排除。车辆仅仅缺少轨迹，不足以将其作为固定建筑导出。

符合条件的对象使用 `Type=Ground+Static+Building`，保留型号和阵营，并标注：

```text
WT_GroundPositionSource=ECSConstructionTransform
WT_GroundStatic=1
```

明确匹配到 ECS 销毁记录时，对象会被移除。完整的身份和阵营规则见[地面单位导出](ACMI_GROUND_UNITS.md)。

## 雷达几何

`LockedTargetAzimuth` 是机体相对方位角，向右为正；`LockedTargetElevation` 是机体相对俯仰角，向上为正，单位均为度。距离和角度只使用事件时刻或之前最多一秒的有效位置，并检查对象生命周期。

解锁、目标销毁或几何无效时，相应字段会清空；切换到具有新鲜几何信息的目标时立即更新。双方位置重合时，距离可为零，但角度没有定义。更多说明见[雷达目标指定](ACMI_RADAR_LOCKS.md)。

## CSV 与格式兼容性

四类诊断 CSV 默认不生成：`-list`、`-missile-flight-log`、`-countermeasure-log` 和 `-radar-lock-log` 分别接受独立的输出路径。

默认文件版本为 ACMI 2.1，可通过 `-acmi-version 2.2` 显式切换。上述字段不要求提高默认版本。既有检查涉及输出文本和测试，尚不能据此确认 Tacview 1.9.4、1.9.5 的界面加载、渲染或地形兼容性。
