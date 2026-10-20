# Headless ACMI 导出

`cmd/acmi` 从 `.wrpl` 生成 Tacview 可读的文本 ACMI 2.1 文件。当前输出包括飞机、地面目标、导弹、导弹运动学估计、INS 标签、干扰措施及释放事件。它用于检查和可视化回放记录，不等同于游戏内部物理遥测。

## 构建

使用 Go 1.25.6，并在仓库根目录关闭 CGO 构建：

```powershell
go version
$env:CGO_ENABLED = "0"
go build -o .\wrpl-acmi.exe .\cmd\acmi
```

POSIX shell 的等价命令是 `CGO_ENABLED=0 go build ./cmd/acmi`。这里保留现有 module 路径 `github.com/maxsupermanhd/wrpl-inspector/v3`；构建的包路径是 `./cmd/acmi`，无需改 module 名称。

## 运行

Windows Release 运行包解压后，可直接把 `.wrpl` 拖到包内的 `run.bat`。脚本以自身目录为工作目录，输出文件使用输入文件名并把扩展名改为 `.acmi`。也可显式指定路径和参考经纬度：

```powershell
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi" -lat 0 -lon 0
```

主要参数如下：

| 参数 | 含义 | 默认值 |
| --- | --- | --- |
| `-in` | 输入 `.wrpl`；也可用一个位置参数给出 | 必填 |
| `-out` | 输出 `.acmi` | 输入文件基本名加 `.acmi`，写入当前目录 |
| `-lat` | 转换局部 Z 坐标时使用的参考纬度，单位为度 | `0` |
| `-lon` | 转换局部 X 坐标时使用的参考经度，单位为度 | `0` |

默认读取 `data/ecshashes.json`。该 ECS 组件哈希表提供实体类名、对象资料、导弹构造和位置解析所需的映射；缺少它时，导出器只能降级运行，实体类名、队伍、飞行员关联、导弹构造及型号信息可能缺失或无法识别。不要把 EXE 单独移出运行包后误删 `data`。`-lat`/`-lon` 只是人工参考点，不应被当作地图真实定位。

## 轨迹与姿态

飞机从飞行模型包读取时间、局部 XYZ 位置和姿态。ACMI 的 `T` 值为 `经度|纬度|高度|滚转|俯仰|航向`；游戏模型的滚转符号取反以匹配 Tacview，航向顺时针补偿 90°，再归一化到 `[0°, 360°)`。这里的航向是可显示的兼容值，不应再按原飞行模型轴解释。

机型名称从 ECS 的 `unit__className` 映射到内置显示名，无法映射时保留回放类名或回退为 `air:<UID>`。玩家信息单独写入 `Pilot`，`Name`/`CallSign` 用于机型或轨迹名称。队伍写入 `Coalition`/`Color`。地面目标使用精确移动轨迹，并按相邻轨迹点计算航向；它没有飞机的飞行模型姿态。

## 导弹位置与运动学

样例回放含 104 枚导弹和 11,152 个去重位置样本。`T` 写成 `经度|纬度|高度|U|V`，其中 `U`/`V` 保留原始局部平面坐标。导弹没有可信的回放机体姿态，因此不写滚转、俯仰、航向三列，也不会填成零。Tacview 可根据轨迹推测显示朝向，但该朝向不是回放记录的真实弹体姿态。

`WT_` 前缀是自定义扩展字段，主要包含：

- `VelocityX/Y/Z`、`Speed`、`GroundSpeed`、`ClimbRate`：局部 XYZ 速度、三维速度、水平速度和升降率，单位为 m/s。
- `Course`、`FlightPath`：航迹方向和爬升角，单位为度。
- `AccelerationX/Y/Z`、`Acceleration`、`TurnRate`：局部加速度、模长和水平转向率。
- `KinematicG`、`NormalAccelerationG`：位置差分得到的运动学估计，以标准重力归一化。
- `WT_FlightSource=PositionDifferenceLocalFit`、`WT_FlightValid=1/0`：来源与当前样本是否有有效估计。

这些数值来自实际同步位置的时间差分；记录足够时使用短窗口局部二次拟合，记录不足时退回三点或两点估计。它们不是游戏内部逐帧物理量，也不是真实机体载荷：`KinematicG`/`NormalAccelerationG` 没有计入推力、完整气动力、质量、燃料或机体轴姿态。`Speed` 也不是真空速 TAS，不能据此推断马赫数、发动机状态、战斗部载荷、目标锁定或制导模式。

无效时刻、非有限坐标、异常跳跃和过长采样间隔不会用于相应差分；缺失估计会清空上一帧的 `WT_` 数值。没有可靠记录时，不补造连续姿态或速度。

## 导弹型号

型号只从当前回放的证据关联，不把某次回放的编号当成跨回放固定表。唯一击杀报告中与导弹销毁时刻接近、发射者一致的记录可直接匹配型号；同一回放内再按 `Leading` 武器 ID 与 `Template` 组合沿用，冲突或歧义时回退未知。

当前样例的 104 枚导弹中识别出 74 枚：20 枚来自唯一击杀报告关联，54 枚来自同回放、同 `Leading`、同模板的沿用，其余 30 枚保持 `unknown`。样例分布为 PL-12 46 枚、R-77 (RVV-AE) 14 枚、AIM-120A 7 枚、AIM-120C-5 2 枚、AAM-4 4 枚、RB 99 1 枚。关联结果写入 `Name`/`CallSign`，审计字段包括 `WT_MissileModel`、`WT_MissileModelSource` 和 `WT_MissileModelConfidence`。

模板本身不能可靠区分所有导引方式，因此未识别时使用 `AAM (ARH)`、`AAM (unknown)` 或 `AGM (unknown)` 一类回退名称，不按飞机型号或低位槽号猜型号。

## INS 标签

标准 `Label` 当前只显示 `INS x.xx s`；没有有效计时器时显示 `INS -- s`，并在自定义字段中清空上一帧的值。原始 `seekerValue` float 本身已是秒值，**不要再除以 48**。48 Hz 只描述推断出的源量化频率，不把秒值换成 tick。

`WT_SeekerTimer` 保留原始 float，`WT_InertialTime` 直接使用秒值，`WT_InertialTimeValid` 表示当前样本是否有效。`WT_InertialTimeSource=SeekerTimer48HzInferred` 明示语义是推断：游戏内部命名和受控实验尚未确证它，因此不能把它解释成累计惯导时间、导引头开机状态或系统开机时长。原值回零就显示 0，不从上一段累计；缺值也不按回放时间自动续走。

## 干扰措施

默认导出干扰措施对象和释放事件。存在真实 WeaponSync 位置样本时直接使用；没有位置样本时，从释放点和载机速度出发，以统一阻力、重力模型估算轨迹，采样间隔 0.2 秒，最长 3 秒。这些样本明确标记为估算：名称通常为 `Countermeasure (estimated)`，并带 `WT_Estimated=1`、`WT_TrajectorySource=EstimatedFromRelease` 等属性。

当前样例得到 1,022 条最长 3 秒的估算干扰物轨迹，共 16,166 个合成位置点，同时保留 1,022 条释放事件。统一模型不能区分热焰弹与箔条，也不能用于判断真实干扰效果。`-countermeasures=false` 可完全关闭干扰物对象和事件。

## 已知坐标问题

**当前版本尚未修复地理经纬度叠加问题。** 导出器先用局部 `X/Z` 与人工 `-lat`/`-lon` 做近似平面换算，把结果写入各对象的 `T`；文件头又写入同样的 `ReferenceLongitude`/`ReferenceLatitude`。支持参考点的查看器会把这两部分再次叠加，导致轨迹出现重复偏移。

默认 `-lat 0 -lon 0` 能避免参考点二次叠加，但得到的仍是以 `(0, 0)` 为人工原点的相对地理形状，**不是地图中的真实定位**。换算还使用固定的 111,320 米/度近似，未处理地图投影、地形或真实基准点。在修复前，不应把输出坐标称为真实地图坐标，也不应把任意一组人工参考纬度/经度示例当作“真实地图位置”。

## 资源边界与发布包

源码对 ECS 分配数量、字符串和压缩块大小、组件嵌套深度、击杀记录数量及坏包做了显式限界，目的是降低畸形回放造成的分配放大和 OOM 风险。普通 `wrpl-acmi.exe` **没有内置强制 Job Object 2 GiB 硬限制**；Release 验证若另用 2 GiB 包装器，那是验证环境的限额，不代表每个用户运行时都有同样硬限。大回放仍可能占用较多内存或因限界报错退出。

发布包含三个 zip：source（源码与构建文件）、runtime（Windows 可执行文件、`run.bat`、`data/ecshashes.json`、说明和许可证）以及 examples（ACMI 回放与 CSV 数据样例）。本地发布目录保留已解压的 runtime 及其 EXE，便于直接运行和复核。
