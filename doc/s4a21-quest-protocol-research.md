# S4A21 quest protocol research

Date: 2026-09-23

The S4A21 protocol adapter exposes `AcceptQuest`, `SetQuestTrigger`, and
`FinishQuest` as wire-only client primitives. These methods do not alter shared
capabilities, scheduler behavior, robot-owned state, or any server database.

## Live evidence

The probe used a newly provisioned level-1 character and quest `1016`, selected
only from the server's advertised `ACCEPTABLE_QUEST_LIST`:

- `ACCEPT_QUEST` succeeded and returned the normal success ACK.
- `SET_QUEST_TRIGGER` with trigger types `0` and `1` was accepted; the server
  log recorded trigger values `1 -> 2 -> 3`.
- Trigger types `2`, `3`, and `4` were rejected with the normal error ACK.
- `FINISH_QUEST` was rejected because the task was not yet complete.

This proves the command body widths and the server-side legality boundary, but
not the complete NPC/objective chain or a dungeon clear condition. Accordingly,
task automation, dungeon settlement, and the public
`backend_capability_unsupported` matrix remain unchanged.

The next research step is to identify a legal objective transition for an
advertised task using only protocol notifications and a disposable character.
No database mutation or guessed quest ID is permitted.

## PVF parser boundary

The S4A21 mirror already contains the corresponding implementation in
`Tool/PvfLib` (`PvfArchive`, `PvfDecryptor`, `QuestFile`, and related model
parsers). Its encrypted header/body layout is not compatible with the native
parser in `internal/capability/pvf`: opening the S4A21 `Script.pvf` with the
native reader fails at the header boundary. The robot therefore keeps the
S4A21 archive reader in `internal/composition/backend/s4a21` and projects only
the shared town-map model upward. The live S4A21 archive currently parses 159
town entries, 143 with usable movement geometry.

This same adapter-owned reader is the correct starting point for future quest
and dungeon projections; the shared scheduler must not learn S4A21 PVF tokens or
reuse the native parser by assumption.

## Quest-chain evidence from the S4A21 PVF

Using the adapter-owned reader against the real S4A21 `Script.pvf` produced the
following definitions:

- Quest `1841` (`epic_23_old_town_9`) requires quest `2608` and is a meet-NPC
  task; it does not itself select a dungeon.
- Quest `1842` (`epic_25_chessboard_1`) requires `1841`, references dungeon
  `160`, and is a `condition under clear` task with `int data = 160 -1`.
- Quest `1843` requires `1842` and is another meet-NPC transition.
- Quest `1844` (`epic_25_chessboard_3`) requires `1843`, references dungeon
  `160`, and is a `hunt monster` task for monster `63717` (five) and `63718`
  (six).
- Quest `1845` requires `1844`, references dungeon `160`, and is a `hunt
  enemy` task for monster `13099` (three).

This explains why entering dungeon `160` with a new level-1 character did not
produce a clear condition: the character has neither the required quest chain
nor the level-17 prerequisite. It also gives a concrete legal verification
path, but does not authorize fabricating quest state or reporting dungeon
settlement as supported. The next live experiment must first establish whether
the server can advance the prerequisite chain through the normal NPC/task
protocol, then enter dungeon `160` only after the server advertises the active
quest.

The preceding PVF chain is also explicit: `1833 -> 1834 -> 1835 -> 1836 ->
1837 -> 1838 -> 1840 -> 2608 -> 1841 -> 1842`. Quests `1835`, `1836`, `1837`,
`1840`, and `2608` reference dungeon `159`, while `1834`, `1838`, and `1841`
are NPC transitions. The server therefore cannot legitimately advertise `1842`
for a new level-1 character, and a generic “decrement every trigger” probe
would be a state bypass rather than a valid workflow test.

The server-side maze selector confirms the same boundary: it first looks for a
maze whose quest connection is type `0` and whose quest ID is in the active
quest set; only then does it fall back to cleared-quest connections or an
ordinary maze. For a condition-under-clear run, the dungeon mechanism waits for
the active quest trigger to reach zero and then checks the configured passive
object at the boss map before producing a clear request. Entering the dungeon
and moving rooms alone is therefore insufficient evidence of settlement.

## Follow-up completion evidence

A second disposable character accepted the same advertised quest, sent
`SET_QUEST_TRIGGER(quest=1016, type=0, increment=false)`, and observed the
server log transition `1 -> 0`. `FINISH_QUEST(quest=1016, reward=-1,
completionCount=1)` then succeeded and returned a non-error reward ACK. The
server removed the quest from the acceptable list afterward.

This establishes the smallest legal task progression used by the current A21
server: accept the advertised quest, apply the server-defined trigger mutation,
then finish with count `1`. It still does not establish a general-purpose task
engine: trigger types and directions are PVF/task-specific, and no dungeon
settlement or public scheduler integration follows from this probe.
