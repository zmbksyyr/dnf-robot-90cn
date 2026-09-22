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
