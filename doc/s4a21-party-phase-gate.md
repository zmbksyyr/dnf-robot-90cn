# S4A21 组队能力阶段门槛

本文记录 `ServerS4A21` 已确认的组队协议边界。dnf-robot 当前只实现显式启用的地下城被动 follower，不开放通用组队能力。

## 当前结论

S4A21 的组队能力不是一个单独的“发送邀请包”动作。服务端会维护队伍成员、槽位、队长、队伍设置、会话身份和 UDP/P2P 端点，并在副本选择与进入流程中继续使用这些状态。因此在完成完整状态闭环前，dnf-robot 必须继续返回稳定的 `backend_capability_unsupported`。

`CREATE_GROUP (0x01A3)` / `LEAVE_FROM_GROUP (0x01A5)` 属于双人聊天会话，不是游戏队伍；不能用来解除 `party` 能力置灰。

## 已从服务端源码确认的协议角色

| 方向 | 包 | 作用 |
| --- | --- | --- |
| 客户端 | `SET_PARTY_INFO (0x000C)` | 创建或修改队伍设置；服务端校验人数上限和 12 字节队伍信息块 |
| 客户端 | `REQUEST_PEER (0x000A)` | 普通组队邀请/申请，body 至少包含目标用户和请求类型 |
| 客户端 | `RESPONSE_PEER (0x000B)` | 接受或拒绝组队请求；服务端按请求时的双方会话与队伍状态重新校验 |
| 客户端 | `LEAVE_PARTY (0x000D)` | 当前成员退队 |
| 客户端 | `WALKOUT_PARTY_MEMBER (0x000E)` | 队长按成员槽位踢人 |
| 客户端 | `SET_UDP_IP_PORT (服务端处理)` | 登记成员的 UDP/P2P 端点并刷新队伍信息 |
| 服务端 | `PARTY_INFO (0x0009)` | 下发完整队伍名册/设置，供客户端刷新队伍窗口 |
| 服务端 | `PARTY_MEMBER_REALTIME_INFO (0x0099)` | 下发队伍成员实时信息 |
| 服务端 | `USER_UDP_IP_PORT (0x000B)` | 下发队友端点信息，配合 P2P/中继连接 |

协议枚举中的 `CREATE_GROUP (0x016A)`、`INVITE_MEMBER (0x016B)` 等服务端通知，以及 `USE_SKILL (0x0026)`、`SKILL_INIT (0x01EC)`，不能在缺少队伍状态和副本 run 状态时单独接入调度层。

## 开放组队前的必要证据

后续若实现 `party`，必须逐项取得协议级或真实整合包回归证据：

1. 单个 robot 创建单人队，收到可解释的 `PARTY_INFO` 和实时信息。
2. 两个独立 session 完成邀请、接受、双方名册广播；重连或会话替换不能把旧 session 当成成员。
3. 退队、踢人、队长变更和断线清理均能收敛到相同的公共队伍状态。
4. UDP 端点登记失败或变化时，不伪造 P2P 成功；队伍信息刷新必须与当前 session 绑定。
5. 组队进入副本时，队伍快照、成员槽位、队长和 dungeon selection/run generation 能够一致传递；否则不得开放地下城入口。
6. 区域喊话仍与组队能力独立：只有确认存在有效队伍接收者后，才解除 `party` 频道的置灰。

## 实现边界

- 不读取或修改 S4A21 的 SQLite、MySQL 或其他游戏数据库。
- 不把队伍状态写入原生端 repository；模拟端只通过协议改变服务端状态。
- 公共层只消费格式无关的队伍意图和能力结果；S4A21 的 opcode、body 和回包观察器留在适配层。
- 在上述证据全部满足前，调度、Actor、Web 和能力矩阵保持现状：`party`、地下城移动、技能、战斗、结算均为占位不支持。

## 最初最小实验（已完成）

只做协议探针，不接入调度：使用真实 S4A21 整合包建立两个临时 session，记录单人建队、邀请、接受、名册广播、退队和断线清理的双向包序列与失败回包。实验必须可重复，并在结束后销毁临时会话；探针不得修改服务端数据库。

## 已完成的真实探针（2026-09-23）

使用 `A21格蓝迪·风云再起终极版/DfoServer/DfoServer.exe`，只通过 TCP 协议建立两个临时角色，未访问服务端数据库：

- 两个角色成功选择进入城镇，CID 为 `3185`、`3186`（第二轮为 `3187`、`3188`）。
- `3185` 发送 12 字节 `SET_PARTY_INFO`，服务端创建单人队伍。
- `3185` 发送 7 字节 `REQUEST_PEER(target=3186,type=0)`；`3186` 收到服务端 `REQUEST_PEER (0x0007)` 邀请通知，body 为 13 字节。
- `3186` 发送精确 7 字节 type-0 `RESPONSE_PEER` 接受包；服务端日志确认 `RES_PEER accept`，队伍成员变为 2，队长仍为 `3185`。
- 服务端随后向队伍广播 `PARTY_MEMBER_REALTIME_INFO (0x0099)`、邀请者确认 `0x0008`、`USER_UDP_IP_PORT (0x000B)` 和 `PARTY_INFO (0x0009)`；日志确认广播 recipients=2。
- 探针结束时主动断开两个临时 session；服务端执行了队长转移、剩余成员队伍刷新和最终解散清理。

这证明了普通两人组队的基础 wire 闭环可以复现，但尚未证明组队进入地下城、组队重连、UDP/P2P 实际连通、组队喊话或技能/战斗状态。因此 `party` 和 `dungeon_move` 继续保持占位不支持，不能据此开放调度能力。

随后追加的真实探针还分别验证了：

- 队长用槽位 `1` 踢出成员后，剩余队员收到新的 `PARTY_INFO`。
- 成员主动 `LEAVE_PARTY` 后，队长收到新的 `PARTY_INFO`。
- 队长直接断线后，剩余成员收到队伍刷新；服务端完成队长转移和最终清理。

这些结果只证明队伍城镇状态的创建、接受和清理可以复现；它们不等同于副本队伍 selection、run generation、重连或战斗能力已经可用。

## 组队副本选择的早期失败样本

在同一真实整合包中，队伍建立后由队长发送 `ENTER_SELECT_DUNGEON(144)`：

- 队长收到成功 ACK，随后只收到城镇/初始化相关通知；由于是首次教程路径，服务端日志明确记录 `defer A21 tutorial NOTI 27 until CHANGE_TUTORIAL_FLAG`。
- 成员没有收到可证明 selection 投影完成的 `ENTER_SELECT_DUNGEON` 或 `START_MAP`；只观察到队伍名册、实时信息和 UDP 端点类通知。
- 探针刻意不发送 `SELECT_DUNGEON`、`CHANGE_TUTORIAL_FLAG` 或 `FINISH_LOADING`，因此没有创建未经验证的 dungeon run。

这说明“组队成功”与“组队副本 selection 已完成”之间仍存在未验证的教程标记、成员投影和 selection cohort 条件。当前 `party`、`dungeon_move` 及所有地下城能力继续保持 `backend_capability_unsupported`。后续若要推进，必须先取得成员投影完成和队伍 `SELECT_DUNGEON` 的真实回包，再研究 run/loading；不能只凭队长 ACK 开放入口。

教程标记探针随后补发了队长的 `SELECT_DUNGEON(144)` 和 `CHANGE_TUTORIAL_FLAG(30,1)`：

- 服务端创建了队长的 dungeon instance/run，并发送队长的 `ENTER_SELECT_DUNGEON`、`DUNGEON_INFO`、`START_MAP`。
- 成员没有收到对应的 `START_MAP`；服务端日志记录的是 `SELECT_DUNGEON: defer A21 tutorial projection`，随后只对队长建立房间实例。
- 探针在 `FINISH_LOADING` 前断开，未发送技能、移动、战斗或结算包。

该失败样本发生在成员尚未完成教程门禁时，不能据此绕过 selection projection，也不能单独作为最终结论。后续探针先通过 `CHANGE_TUTORIAL_FLAG(31,0)` 为两端完成门禁，再重新建立队伍，已取得成员投影、双端 loading 和 run 房间一致性的成功样本，见下一节。

## 最小地下城 follower 已验证边界

后续真实整合包回归已经验证以下最小闭环：

- follower 通过 `CHANGE_TUTORIAL_FLAG(31,0)` 完成首次教程门禁；
- follower 自动接受普通 type-0 邀请，且只有自身 wire UID 出现在 `PARTY_INFO` 八槽名册后才进入 party-active；
- 队长选图后，服务端向队长和 follower 投送同一 run 的 `START_MAP`；
- follower 不发送 `MOVE_MAP`，只对自己的 `START_MAP` 回复 `FINISH_LOADING`；
- 队长换房后，服务端再次向 follower 投送目标房间，follower 完成下一房间加载；
- 生产 `SessionFactory` 路径已完成上述自动接邀、入场和连续换房的真实回归；普通满编队测试中，1 名队长和 3 名独立 follower 均到达相同目标房间。

该闭环只支持被动队员投影。通用 `party` 和主动 `dungeon_move` 能力继续关闭；创建队伍、主动邀请、技能、战斗、结算、奖励、回城和 rejoin 均未因此开放。

## 邀请者身份限制

`REQUEST_PEER` 邀请通知只包含 inviter wire UID、请求类型和协议附加值。服务端 `GET_USERINFO` 的他人查询同样以同频道 UID 为索引，返回角色资料而不返回账号身份；跨区域邀请上下文最多补充角色记录，也不能证明其所属账号。

因此，当前 S4A21 的 `follow_account` 只作为显式启用 follower 的开关，不能按账号文本验证邀请者。补齐这一能力需要新的、可复现的协议身份映射证据；不得查询 SQLite、MySQL 或其他模拟端数据库，也不得把角色名猜测成账号。现阶段接受普通邀请这一限制必须在配置和阶段说明中保持可见。
