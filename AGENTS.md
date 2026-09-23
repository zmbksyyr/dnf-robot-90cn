# AI Rules

## Project Scope

`dnf-robot` is a multi-backend robot platform. The scheduler, actor lifecycle,
common robot behavior, runtime management, and Web administration are shared.
Server-specific protocol, persistence, service, and capability differences are
implemented behind backend adapters.

The current backends are the native Linux server and the S4A21 simulated
server. Future simulated servers must be addable as independent backends
without branching shared scheduler behavior. Removing one backend must not
break the common platform.

## Layering

Keep dependencies flowing through the existing layers. Shared contracts and
protocol primitives may not depend on a concrete backend, scheduler, or Web
entrypoint. Schedulers and Web handlers depend on contracts and capability
snapshots, never on a concrete backend package.

Backend-specific code may be split by layer when useful, but it must remain
inside that backend boundary. Extract only behavior proven common; do not add
an abstraction merely for a possible future backend.

Every backend must declare its capabilities and provide the smallest adapter
surface needed by the scheduler. Unsupported operations must return an explicit
unsupported result and be disabled in the Web UI. Do not scatter backend-name
conditionals through shared code.

## Environment Selection

The active server environment is selected explicitly by the Web administration
surface and persisted as runtime state. Startup must not silently infer or
switch between native and simulated servers from paths, processes, file
extensions, ports, or operating system.

If the persisted selection is unavailable on the current platform, startup
may enter Web-only recovery mode so the operator can choose a valid backend.
Recovery mode must not initialize a backend, actors, sessions, or simulated
server state; the selected backend takes effect only after an explicit restart.

Switching environments is a destructive runtime transition: stop robot actors,
sessions, and schedulers; preserve an auditable backup; rebuild the robot
runtime/config directories for the selected backend; then initialize and start
the new backend. The active environment and configuration generation must be
visible in diagnostics.

Operating-system restrictions are backend capabilities. The native backend is
currently Linux-only and retains its native service, patch, key, MySQL, and
deployment requirements. Simulated backends may support Windows and/or Linux
according to their own adapter declaration. Do not make native-only rules
global.

## Simulated Server Persistence Boundary

Prefer verified network protocol operations for simulated-server game-state
changes. A simulated backend may use direct persistence only when the operator
has explicitly enabled that backend-specific mode and no suitable protocol
workflow is available. Do not make direct persistence a silent fallback.

Database engines, schemas, transactions, and compatibility checks belong
inside the concrete backend adapter. Shared contracts and schedulers must not
import database drivers or assume SQLite, MySQL, table names, or storage paths.
An adapter that supports direct persistence must declare the capability,
validate the target schema, mutate characters only while their game sessions
are offline, use transactions, and report unsupported schemas explicitly.
Future simulated backends may omit or replace this adapter without changing
shared scheduling behavior.

Robot-owned configuration, runtime state, queues, logs, and audit data remain
under the robot runtime layout and are separate from simulated server data.

## Verification and Changes

Before VM, deployment, or debug work, read `doc/vm.md`. Read repository and
domain documentation as UTF-8. Use Python `paramiko` for VM SSH, upload, and
remote commands; never use PowerShell `ssh` or `scp` for VM work.

Preserve unrelated worktree changes. Use `apply_patch` for manual edits. Keep
changes scoped, add focused tests for adapter contracts and capability behavior,
and run relevant Go tests before committing.

Backend work is staged and committed by phase. Each commit should represent a
coherent architectural or functional step, with no unrelated formatting or
metadata churn.

Do not use PowerShell to read or write source files. Use repository-aware
search tools, ordinary cross-platform commands, and `apply_patch` for manual
edits.

Robot is a single-process application. Web administration, bounded log
rotation, schedulers, and backend runtimes share one coordinated lifecycle;
do not launch the Robot executable again as a Web or logging child. Shutdown
must stop new work, wait a bounded time for in-flight operations, persist
Robot-owned state and database transactions, close transports, and close logs
last.

## Native Linux Deployment Card

The following rules apply only to the current native Linux deployment:

- VM: `192.168.200.131`
- SSH: `root / 123456`
- Web: `http://192.168.200.131:8112`
- Web password: `twadmin`
- robot API: `8111`
- game: `10011`
- auction: `30803`
- point: `30603`
- deployment root: `/root` only
- robot: `/root/robot`
- config: `/root/config`
- main config: `/root/config/conf/config.ini`
- runtime logs: `/root/config/logs/`
- generated files: `/root/config/{conf,templates,keys,pvf,state,logs,tmp}/`

Every native deployment moves the complete `/root/config` to
`/root/config.bak.<timestamp>`, keeps only the newest three config backups,
creates a fresh `/root/config`, and lets robot regenerate all files. Do not
migrate individual files automatically; users recover anything needed from the
backup.

Game RSA files and Auction/Point `iteminfo.dat` are external integration
files/copies, not alternate deployment roots. A normal Restart only restarts
`/root/robot` and must not move, delete, or recreate `/root/config`.

Start the native robot with the bounded stdout sink:

```sh
mkdir -p /root/config/logs
nohup sh -c '/root/robot 2>&1 | /root/robot --bounded-log-sink /root/config/logs/stdout.log' >/dev/null 2>/root/config/logs/start_error.log &
```
