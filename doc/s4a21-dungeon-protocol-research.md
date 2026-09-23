# S4A21 地下城协议只读研究

本文只记录对 `ServerS4A21.git` 的静态协议研究结果，不表示 dnf-robot 已经支持地下城。当前 `MoveDungeon` 仍必须返回 `backend_capability_unsupported`。

## 已确认的协议角色

S4A21 将地下城过程拆成多个阶段，不能把 `MOVE_MAP` 当作独立的移动能力：

- 客户端请求：`ENTER_SELECT_DUNGEON=0x000F`、`SELECT_DUNGEON=0x0010`、`FINISH_LOADING=0x0025`、`MOVE_MAP=0x002D`、`SET_PLAY_RESULT=0x002E`、`GET_ITEM=0x002B`、`USE_SKILL=0x0026` 等。
- 服务端通知：`ENTER_SELECT_DUNGEON=0x001B`、`DUNGEON_INFO=0x001C`、`START_MAP=0x001D`、`FINISH_LOADING=0x001E`、`ENABLE_CLEAR_DUNGEON=0x001F`、`PLAY_RESULT=0x0022`、`CLEAR_DUNGEON_REWARD=0x0023`、`MOVE_MAP=0x00F5`。
- 队伍/战斗相关通知还包括 `CREATE_GROUP=0x016A`、`SKILL_INIT=0x01AA`；它们不是进入地下城前可以省略的通用替代包。

### 静态确认的请求体边界

这些边界来自 S4A21 服务端的 parser，而不是 dnf-robot 的猜测：

- `ENTER_SELECT_DUNGEON`：至少 4 字节，小端 `uint32 dungeonId`；后续字节允许存在，但只被记录为尾部，不能据此推断语义。
- `SELECT_DUNGEON`：至少 9 字节，小端 `uint32 dungeonId`，随后为 `difficulty`、`flag1`、`flag2`、小端 `uint16 A21Sentinel`；当前 parser 默认 sentinel 为 `0xFFFF`，尾部仍可能存在。
- `MOVE_MAP`：固定至少/实际解析 64 字节：`nextX`、`nextY`、`pathPositionX(uint32)`、`pathPositionY(uint32)`、`moveMode`、`trapBits(uint16)`、8 个 `uint16 memberMapClearValues`、8 个 `uint32 memberMapElapsedValues`、`clientTimingToken(uint16)`、`clientStateFlag`。服务端还会校验当前 run、迷宫拓扑、死亡/清除/机制状态。

因此，未来适配层即使实现地下城，也不能复用城镇移动的坐标请求结构；必须单独定义 `DungeonMoveRequest` 和 run/loading 状态。

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

源码可以确认普通首张地图的 `START_MAP` body 会包含房间坐标、随机种子、模式/房间状态、地图 id、怪物/对象投影和接收者队伍 slot；但其动态对象列表依赖具体 PVF、地下城和运行时种子，不能用固定样例伪造。

整合包现存 `DfoServer/packet_log.txt` 只证明服务器具备抓包日志能力；当前文件没有可验证的完整普通地下城回合，因此不作为地下城开放依据。

## 真实整合包单角色探针（2026-09-23）

使用用户提供的 `A21格蓝迪·风云再起终极版/DfoServer/DfoServer.exe`，通过网络协议创建临时角色并完成登录、选角、握手。探针没有访问任何服务端数据库。

目标 dungeon `144` 走到了首次教程分支，真实观察到：

1. `CMD 0x000F ENTER_SELECT_DUNGEON`，4 字节 body（`uint32 dungeonId=144`）。
2. 服务端先返回 `NOTI 0x0002 USERINFO`，再返回 `CMD 0x000F` 成功 ACK、`NOTI 0x0003 USER_STATE`、`NOTI 0x001A UDP_HOST`，随后还有该端的初始化通知；此时没有 `START_MAP`。
3. `CMD 0x0010 SELECT_DUNGEON` 使用 15 字节 body：`uint32 dungeonId=144`、difficulty/flags 为 0、`A21Sentinel=0xFFFF`、6 字节零尾部。服务端日志确认已创建 run，但首次教程会等待教程标记。
4. `CMD 0x008F CHANGE_TUTORIAL_FLAG` 使用紧凑 6 字节 body `00 1E 00 00 00 01`（flag 30、reward 1）。之后真实收到：
   - `NOTI 0x001B ENTER_SELECT_DUNGEON`，body 37B；
   - `NOTI 0x019F TAG_CHARACTER_INFO`，body 2B；
   - `NOTI 0x001C DUNGEON_INFO`，body 32B；
   - `NOTI 0x001D START_MAP`，body 86B；
   - `CMD 0x008F` 成功 ACK，body 2B。
5. `CMD 0x0025 FINISH_LOADING` 空 body 被接受，随后收到 `NOTI 0x001E FINISH_LOADING`，body 5B（`00 00 00 00 00`）。

该证据证明了“选择/教程标记/地图加载/加载释放”是连续状态机，也证明 `START_MAP` 不是 `ENTER_SELECT_DUNGEON` 的直接结果。它尚未证明普通非教程角色、`MOVE_MAP` 的合法房间目标、战斗或结算流程，因此仍不足以开放地下城能力。

随后通过同一服务端仅使用协议发送教程完成标记 `CHANGE_TUTORIAL_FLAG(flag=31)`，断开并重新登录同一临时角色，取得了普通路径证据：

- 普通 `ENTER_SELECT_DUNGEON` 的顺序为 `USERINFO → success(0x000F) → USER_STATE → UDP_HOST → ENTER_SELECT_DUNGEON(0x001B) → 初始化通知`。
- 普通 `SELECT_DUNGEON` 随后返回 `TAG_CHARACTER_INFO(0x019F)`、`DUNGEON_INFO(0x001C, 32B)`、`START_MAP(0x001D, 86B)`。
- 空 body 的 `FINISH_LOADING(0x0025)` 随后得到 `FINISH_LOADING(0x001E, 5B)`。
- 教程完成回城本身还返回 `USER_STATE(0x0003)`、`USER_AREA(0x0017)`、`AREA_USERS(0x0018)` 和 `0x00CA`，说明教程结束与普通回城共享一部分城镇恢复投影。

这已经满足阶段 7.0 的单角色入口/加载观察目标，但仍没有发送或验证合法 `MOVE_MAP` 房间目标，也没有验证战斗、结算、断线重连。因此阶段 7.1 仍不得开始，`dungeon_move` 继续保持占位不支持。

### `MOVE_MAP` 单角色真实样本

在普通角色首图 `(0,3)` 完成 `FINISH_LOADING` 后，使用 64 字节 body（仅设置前两个目标坐标，其余字段为 0）做了有限候选探针：

- 目标 `(0,2)`：服务端无 `START_MAP`，保持当前房间；这是一个真实拒绝/忽略样本。
- 目标 `(1,3)`：服务端返回 `START_MAP(0x001D)`，body 147B；随后发送空 body `FINISH_LOADING(0x0025)`，收到 `FINISH_LOADING(0x001E)` 5B 释放通知。

这确认了 `MOVE_MAP` 必须绑定服务端的迷宫拓扑和当前房间状态，不能仅凭坐标范围判断成功。该实验没有清怪、拾取、技能、组队或结算，也没有把成功移动接入 dnf-robot；正式地下城能力仍保持关闭。

### 断线观察

在完成普通首图和一次房间切换后直接关闭 TCP，再用同一账号重新登录/选角，服务端日志记录 `disconnect_detached`，并出现 `DungeonRejoin candidate not projected`；重新登录没有自动恢复到副本，也没有收到可继续副本的主动通知。由此确认：地下城断线恢复不是现有通用重连的自然结果，后续必须单独研究显式 `REJOIN_DUNGEON`/恢复协议；当前不实现、不伪造恢复成功。

## 当前阶段结论

当前开放城镇移动和真人队长 follower：组队邀请接受、队长城镇位置/区域跟随，以及服务端投影的地下城加载跟随。地下城主动移动、技能、战斗、结算和地下城回城继续保持占位接口，并返回稳定的 `backend_capability_unsupported`。即使代码中已经存在 `MOVE_MAP` opcode，也不能提前开放主动调度或 Web 按钮。

## 后续最小实验顺序

在不改 dnf-robot 运行逻辑的前提下，下一轮应使用用户提供的 S4A21 整合包做单角色抓包/日志实验：

1. 记录从城镇打开普通地下城选择到 `ENTER_SELECT_DUNGEON` 的完整双向包序。
2. 只验证单角色 `SELECT_DUNGEON`、入场费用和 `START_MAP`/加载闭环，不接入战斗。
3. 确认 `MOVE_MAP` 的目标房间身份和最小合法请求体，并验证服务端拒绝非法状态。
4. 再单独研究队伍投影、`CREATE_GROUP`/`SKILL_INIT` 和结算恢复；每一步都有协议级回归后，才考虑适配层接口实现。

所有实验仍必须通过网络协议完成，不能读写 S4A21 的 SQLite、MySQL 或其他服务端数据库。
