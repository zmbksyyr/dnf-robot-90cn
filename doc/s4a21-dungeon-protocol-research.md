# S4A21 地下城协议只读研究

本文只记录对 `ServerS4A21.git` 的静态协议研究结果，不表示 dnf-robot 已经支持地下城。当前 `MoveDungeon` 仍必须返回 `backend_capability_unsupported`。

## 已确认的协议角色

S4A21 将地下城过程拆成多个阶段，不能把 `MOVE_MAP` 当作独立的移动能力：

- 客户端请求：`ENTER_SELECT_DUNGEON=0x000F`、`SELECT_DUNGEON=0x0010`、`FINISH_LOADING=0x0025`、`MOVE_MAP=0x002D`、`SET_PLAY_RESULT=0x002E`、`GET_ITEM=0x002B`、`USE_SKILL=0x0026` 等。
- 服务端通知：`ENTER_SELECT_DUNGEON=0x001B`、`DUNGEON_INFO=0x001C`、`START_MAP=0x001D`、`FINISH_LOADING=0x001E`、`ENABLE_CLEAR_DUNGEON=0x001F`、`PLAY_RESULT=0x0022`、`CLEAR_DUNGEON_REWARD=0x0023`、`MOVE_MAP=0x00F5`。
- 队伍/战斗相关通知还包括 `CREATE_GROUP=0x016A`、`SKILL_INIT=0x01AA`；它们不是进入地下城前可以省略的通用替代包。

## 当前可以确认的最小顺序

从 `DungeonEntryHandler`、`DungeonMapHandler`、`DungeonLoadingCoordinator`、`DungeonSettlementHandler`、`DungeonTownReturnCoordinator` 及 A21 自测代码可以确认以下状态顺序：

1. 角色必须先处于可进入的城镇/频道状态，并由服务端建立 dungeon selection。
2. 客户端发送 `ENTER_SELECT_DUNGEON`。服务端会校验选择状态、队伍 leader、队伍快照和重试代次，然后投影一组 `USERINFO`。
3. 服务端依次投影成功响应、`USER_STATE`、`UDP_HOST`，并发送 `ENTER_SELECT_DUNGEON` 状态通知。A21 自测明确要求所有 dungeon `USERINFO` 先于这些包；该前缀不包含 `START_MAP`。
4. 角色发送 `SELECT_DUNGEON`。请求至少包含 dungeon id、difficulty、两个 flag、A21 sentinel/尾部形状；服务端会再次校验等级、许可、疲劳/入场费用、队伍 cohort，并可能为队伍预创建和激活共享 run。
5. 服务端为 run 建立加载投影，发送 dungeon 选择/地图信息，随后才进入 `START_MAP`、客户端 `FINISH_LOADING`、服务端 `FINISH_LOADING` 的加载闭环。`START_MAP` 对多人 run 还携带固定 party slot 身份。
6. 地图内的 `MOVE_MAP` 受当前 run、房间/地图身份和加载状态约束；它不是城镇移动的同一语义。战斗阶段还涉及技能初始化、技能/怪物/拾取等状态。
7. 通关后客户端发送 `SET_PLAY_RESULT`，服务端投影 `PLAY_RESULT`、奖励/卡牌等结算状态；不同地下城可能有额外协议。
8. 结算、放弃、超时或断线恢复后，`DungeonTownReturnCoordinator.ReturnAsync` 将角色投影回城镇，恢复用户区域、队伍投影和必要的 `USERINFO`。

## 已确认的服务端状态前置

- `ENTER_SELECT_DUNGEON` 必须绑定当前 selection；队伍成员变更会使旧 selection 失效。
- 队伍进入由 leader 的冻结 cohort 驱动，成员 slot 和 UDP host slot 不能随意重排。
- `SELECT_DUNGEON` 可能消耗入场物品/金币并更新库存；失败时需要回滚已准备的 party run。
- 加载和回城均带 run identity/generation 防护，旧 session 或旧异步 watcher 不得覆盖新 run。
- 结算返回城镇不是简单发一个成功 ACK，还要恢复城镇区域、队伍和角色状态。

## 尚未确认、不能凭静态代码推断的内容

- 普通格兰迪等目标地下城各字段的真实请求体长度、坐标/房间字段和不同难度的差异。
- `START_MAP`、`DUNGEON_INFO`、`FINISH_LOADING` 在真实客户端上的完整包序，尤其是不同队伍规模、频道和特殊地下城的分支。
- `CREATE_GROUP`、`SKILL_INIT` 是否对目标整合包的普通地下城必需，以及其动态 body 的最小合法值。
- 怪物死亡、技能释放、拾取、结算卡牌与奖励的最小可运行闭环。
- 断线重连、放弃、超时、重新加入地下城的真实恢复包序。

## 当前阶段结论

当前只开放城镇移动。地下城移动、组队、技能、战斗、结算和地下城回城继续保持占位接口，并返回稳定的 `backend_capability_unsupported`。即使代码中已经存在 `MOVE_MAP` opcode，也不能提前开放调度或 Web 按钮。

## 后续最小实验顺序

在不改 dnf-robot 运行逻辑的前提下，下一轮应使用用户提供的 S4A21 整合包做单角色抓包/日志实验：

1. 记录从城镇打开普通地下城选择到 `ENTER_SELECT_DUNGEON` 的完整双向包序。
2. 只验证单角色 `SELECT_DUNGEON`、入场费用和 `START_MAP`/加载闭环，不接入战斗。
3. 确认 `MOVE_MAP` 的目标房间身份和最小合法请求体，并验证服务端拒绝非法状态。
4. 再单独研究队伍投影、`CREATE_GROUP`/`SKILL_INIT` 和结算恢复；每一步都有协议级回归后，才考虑适配层接口实现。

所有实验仍必须通过网络协议完成，不能读写 S4A21 的 SQLite、MySQL 或其他服务端数据库。
