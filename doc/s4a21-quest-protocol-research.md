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
