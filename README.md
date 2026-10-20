# wrpl-inspector v3

本仓发布为 [3794432/wrpl-inspector 的 v3 分支](https://github.com/3794432/wrpl-inspector/tree/v3)，基于 [原 wrpl-inspector](https://github.com/maxsupermanhd/wrpl-inspector/tree/v3)。原项目是用于查看《战争雷霆》回放文件格式的 Go 库与 dear-imgui 界面，可打开回放、检查包头与解析结果，并提供击杀记录和地图等视图；原有组件与构建说明见 [docs/README.md](docs/README.md)。

本仓保持在 `v3` 分支并沿用现有 Go module 路径，新增无界面的 `cmd/acmi`：把 `.wrpl` 中的飞机、地面目标、导弹和干扰措施导出为 Tacview ACMI。飞机轨迹带机型、队伍、飞行员关联及从飞行模型包解出的姿态；导弹轨迹保留实际位置，并附加由位置序列估计的速度、加速度和轨迹参数。

当前 `v3` 已补充飞机击毁删除、导弹型号/制导证据和默认 Atlas 地图布局。历史标签 `acmi-2026.10.20` 的发布包保留旧行为；要使用这些新增功能，请从当前源码构建。

## 快速运行

使用包含当前源码改动的 Windows 运行包时，把 `.wrpl` 文件拖到包内的 `run.bat`，再用 Tacview 打开新生成的 `.acmi`。也可在包目录执行：

```powershell
.\wrpl-acmi.exe -in "replay.wrpl" -out "replay.acmi"
```

请保留运行包中的 `run.bat`、`wrpl-acmi.exe` 和 `data\ecshashes.json`。拖拽模式把 ACMI 写到运行目录；旧 ACMI 不会自动更新，修改回放或导出逻辑后需重新生成。

## 从源码构建

使用 Go 1.25.6，关闭 CGO：

```powershell
go version
$env:CGO_ENABLED = "0"
go build -o .\wrpl-acmi.exe .\cmd\acmi
```

在支持行内环境变量的 shell 中，等价命令为 `CGO_ENABLED=0 go build ./cmd/acmi`。运行仍需要 `data/ecshashes.json`；源码打包和构建不会修改仓库的 module 路径。

导出说明见 [docs/acmi-export.md](docs/acmi-export.md)，型号与制导证据分别见 [ACMI_MODELS.md](docs/ACMI_MODELS.md) 和 [ACMI_GUIDANCE.md](docs/ACMI_GUIDANCE.md)。默认 Atlas 布局会把不同游戏地图放在互不重叠的技术坐标，安装地形的说明见 [terrain-atlas.md](docs/terrain-atlas.md)。

发布物分为源码、Windows runtime 和 examples 三个 zip。runtime 包应包含可执行文件、`run.bat`、数据表与说明文档；本地发布目录保留已解压的 EXE，examples 包提供可直接查看的回放导出样例。

本项目及 `wrpl` 库采用 GNU Affero General Public License v3，详见 [LICENSE](LICENSE)。项目与 Gaijin Entertainment 无隶属或背书关系。
