# Project Rules

- Keep strict layering: public contracts/core, capability orchestration, adapter implementation, and entrypoints must remain separate.
- Version-specific protocol, PVF, persistence, configuration, and capability behavior belongs in the adapter layer. Do not scatter version-name conditionals through shared code, scheduler, Actor, or Web.
- Preserve the lifecycle order: initialization → dispatch → shutdown. Shutdown must stop new work, finish bounded in-flight work, persist required state, close transports, and close logs last.
- Use the shared lockhub with named resources. Do not add raw `sync.Mutex`/`sync.RWMutex` or parallel locks for the same resource.
- Use the unified runtime state table/snapshot for Actor, online, operation, social, trade, error, and Web/API state. Do not create competing status sources.
- Prefer adapter protocol operations over direct database access. Database drivers, schemas, table names, and writes stay inside the adapter persistence boundary.
- Unsupported adapter capabilities must return explicit unsupported results and be disabled or hidden by Web.
- Do not reintroduce removed native-server code, configuration, keys, service scripts, market/mail entrypoints, or generated artifacts.
- Do not use PowerShell to read or write source files. Use `rg`, `cmd /c type` or other repository-aware read commands; use `apply_patch` for manual edits.
- Read detailed design rules from `doc/`; keep this file limited to execution constraints.
- Keep the version capability matrix current in [`doc/90CN能力状态.md`](doc/90CN能力状态.md). Update it together with the 90CN adapter descriptor whenever an ability is added, removed, verified, or found unsupported; do not overstate compile-only support as runtime verification.
- Preserve unrelated worktree changes. Make focused staged commits and run relevant tests before committing.
