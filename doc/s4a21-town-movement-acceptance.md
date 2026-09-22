# S4A21 城镇移动阶段验收清单

本清单用于收束当前阶段。它只覆盖城镇移动、城镇 PVF 投影和相关 Web/运行时边界，不代表地下城能力已经实现。

## 已通过项目

- [x] S4A21 通过独立 adapter 读取自己的 PVF，并投影为公共 `MapCatalogItem`。
- [x] 城镇区域只在存在可移动几何时标记为可用；缺失几何不会伪造坐标。
- [x] 公共移动策略复用随机目标、移动步数、速度、间隔和跟随逻辑。
- [x] 跨区域跟随目标没有可用几何时拒绝执行，不发送区域 ID 与坐标不一致的请求。
- [x] S4A21 仅通过协议发送城镇移动包，不访问模拟端 SQLite、MySQL 或其他游戏数据库。
- [x] S4A21 移动字段在适配层做坐标、方向和速度范围校验，避免静默截断。
- [x] 发包失败不会提交运行时坐标；断线回收后重连使用新的坐标快照，旧 watcher 不会覆盖新会话。
- [x] S4A21 协议心跳保持会话，真实 600 会话回归已通过。
- [x] Web 明确显示 `Town move / 城镇移动`，能力不支持时按钮置灰。
- [x] Web 区分区域喊话与世界喊话；批量动作失败时展示具体假人错误。
- [x] 真实整合包回归通过：登录、选角、心跳、城镇移动、区域喊话，测试后游戏端口已回收。

## 真实验证证据

```text
go test ./...
git diff --check
```

PVF 只读解析结果：

```text
S4A21 DfoServer/Script.pvf: 159 areas, 143 usable geometries
S4A21 DNF/Script.pvf:       159 areas, 143 usable geometries
Native DNFClient/Script.pvf: 124 areas, 124 usable geometries
```

真实端到端回归使用用户提供的 `DfoServer.exe`，2 个会话完成登录、选角、心跳、城镇移动和区域喊话；结束后服务停止，端口 `10011` 已确认回收。

## 明确未纳入本阶段

- 地下城入口、地下城移动、房间拓扑和结算；
- 组队、技能释放、摆摊/市场；
- 任何模拟端游戏数据库读写；
- 第二个模拟端的接入实现。

地下城入口仍受 [s4a21-dungeon-phase-gate.md](s4a21-dungeon-phase-gate.md) 约束，`MoveDungeon` 继续返回稳定的 `backend_capability_unsupported` 占位结果。

