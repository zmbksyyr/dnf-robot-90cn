# 模拟端镜像复用盘点

本阶段只做只读盘点，不接入第二个模拟端，也不开放地下城移动。

## S4A21 相关

`S4A21GmTool.git/PvfLib/Models/TownFile.cs` 提供了 S4A21 城镇文件的直接参考：

- 城镇区域由 `id + map path` 组成；
- 区域可带 `normal`、`gate`、`dungeon gate` 类型及连接 ID；
- 城镇和区域还可能带等级、任务等进入权限；
- 解析入口是脚本树解析后再投影为类型化对象。

当前 Go 适配层已经将可移动几何投影为统一 `MapCatalogItem`，只复用数据语义，不复制 C# 工具实现。权限、连接关系在城镇移动阶段暂不作为放行条件，避免把地下城入口语义误当成普通城镇移动。

## 原生/其他版本参考

本轮对后续镜像做了只读确认：

- `86JP.git/Server/DfoServer` 明确包含 `Sqlite*Repository`、`inventory.db` 和 SQLite 角色/账号持久化；它不能成为 Robot 直接读写模拟端数据库的先例，后续若接入仍必须先建立协议建号、状态查询和动作回包证据。
- `90.git/go-server` 同时包含独立的 MySQL 打包/控制面和大量 PVF、地下城分析工具；这些资料可用于协议/PVF 研究，但不能把其 MySQL schema 或控制程序依赖引入共享 scheduler。
- 上述两个镜像与 S4A21 的 PVF、协议和存储形态均不同，新增 adapter 的最小准入条件仍是：自己的 PVF 解析器、自己的协议 DTO/封包、公共 `MapCatalogItem`/能力契约投影，以及明确的 unsupported 能力矩阵。

`90.git/go-server` 的 `worldmap` 与 `dnfbridge` 实现展示了完整地下城拓扑、房间访问、清理状态、分层地图和结算约束；这说明地下城移动不是单个 `MOVE_MAP` 包即可完成的动作。该实现只作为后续协议研究资料，不进入当前 S4A21 城镇移动路径。

`86JPGMTool.git` 与 `S4A21GmTool.git` 都包含 PVF archive、脚本解析和 `TownFile`/`MapFile` 模型，但版本字段与解析扩展不同。因此后续新增模拟端时，应在各自 adapter 内完成 PVF 读取，再投影到公共地图目录；公共调度、随机目标、移动间隔和运行时状态不重复实现。

## 当前边界

- 城镇移动与地下城移动保持两个独立能力接口；
- 模拟端只通过协议发包，不读取或修改 SQLite、MySQL 或游戏数据库；
- 不能确认完整地下城工作流前，`MoveDungeon` 保持稳定的 `backend_capability_unsupported` 占位；
- 不支持的能力继续由 Web 能力矩阵置灰，并保留稳定错误，便于以后替换 adapter 实现。

## 真实 PVF 回归记录

使用当前工作区可访问的整合包做只读解析：

- S4A21 `DfoServer/Script.pvf`：159 个城镇区域，其中 143 个有可移动几何；
- S4A21 `DNF/Script.pvf`：159 个城镇区域，其中 143 个有可移动几何；
- 原生 `DNFClient/Script.pvf`：124 个城镇区域，其中 124 个有可移动几何。

两份 S4A21 PVF 均通过同一 S4A21 adapter 入口解析；原生 PVF 仍走公共 PVF 能力包的原生路径。

随后启动该整合包的 `DfoServer.exe` 做端到端回归：2 个会话均完成登录、选角、协议心跳、城镇移动和区域喊话；测试通过后服务已停止，游戏端口 `10011` 已确认回收。该回归没有执行地下城动作，也没有由 Robot 直接访问服务端数据库。
