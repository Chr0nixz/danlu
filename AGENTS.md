# AGENTS.md

Guidance for coding agents working in this repository.

## What this is

Danlu (丹炉) is a control panel for GPU training jobs that runs on each GPU server. It is one Go binary with the React UI embedded, installed as a systemd user service. You reach it over the public internet; access is protected by a random port, a secret entrance path, a password, and HTTPS from a local CA. Through it you submit, queue, run, cancel and rerun jobs, and for every job it records which code version ran.

Direction:

- **Current phase: backend and web panel first.** The web panel is the main interface for now, on desktop and phone alike, so design it as the primary tool, not a fallback. `docs/ROADMAP.md` lists the work items in order and says what counts as done.
- **Desktop client paused.** Controlling several servers at once is the job of the desktop client: a Tauri app in `desktop/`, built for Windows first with macOS planned. It is designed in `docs/CLIENT.md`. Milestones C1 (skeleton) and C2 (first usable version) are built; C3 waits until the backend and panel reach a stable release. Until then add no client features, but keep `desktop/` compiling and working whenever `/api` changes. Once the client resumes, it becomes the main tool on the desktop and the panel stays as the per-server fallback for phones.
- **One server per panel.** The panel manages only the server it runs on: no server switcher and no multi-server views. Servers never talk to each other; multi-server aggregation happens in the client. Phone notifications come from the server itself via ntfy (implemented in S2; see `docs/ROADMAP.md` and `docs/PHASES.md`); desktop notifications stay with the client.
- **Obsolete parts of `docs/DESIGN.md`.** The §4 peer federation (peers, request forwarding, cross-node auto-assignment, the "move to another node" feature) the §5.1 folder-picker submit with snapshot upload, and the §5.7 Let's Encrypt IP certificates are dropped (certificates: `docs/ROADMAP.md` §7.1). Code still reaches the servers through the user's own rsync/scp. The rest of that doc is still a valid backlog; `docs/CLIENT.md` §10 has the mapping.
- **`/api` is not frozen.** Change it freely, but in the same change update the types and request functions in `packages/core/src` and keep both `web/` and `desktop/` compiling. For an incompatible change, bump `APIVersion` in `internal/api` and `API_VERSION` in `packages/core` together. The client compares it with `/api/me`'s `api_version` to tell which servers need upgrading.
- **Status.** Everything in the demo scope (`docs/DEMO.md`) is built and passes in WSL with fake GPUs. As of 2026-11-02 the user is using it day to day without reported issues; a complete real-GPU acceptance record is still pending. S0 checks are filled in during use and do not block later development; keep unverified scenarios explicit and fix reported problems promptly. S1 has completed unified job transitions, first-pass failure classification, environment-package recording, machine metadata and output manifests. In the desktop client, the HTTPS path (pinned CA, device token, log streaming), the tray popover and the notification logic were exercised against a WSL server. The SSH flows were not: adding a server, upgrades and service management have never run end to end, because the WSL test box has no sshd. Toast display and the tray icon states were also never confirmed visually.

- **S2 状态（2026-11-02）**：队列手动/快速失败暂停、磁盘保护、保留策略清理和 ntfy 手机推送均已完成代码与自动化；2026-11-03 验收后修复快速失败重复推送、网络存储卡住拖住调度、收尾时扫描整份日志和 137 误判内存不足，184 项 WSL e2e 通过。手机 App 实际接收和真实 GPU 告警随日常使用补验。推送通过持久化事件异步发送，服务从不修改通知配置。
- **S3 状态（2026-11-03）**：剩余时间、网页终端、手机二维码与 CA 安装指引已实现。手机输入法、后台重连与实际安装 CA 随使用补验。
- **S4 状态（2026-11-03）**：路径反查、任务对比和用快照代码重跑已实现。快照重跑保留原项目路径用于记录，实际在 `run/<id>/code/` 执行。源快照在子任务取得自己的硬链接前受清理保护。真实训练项目随使用补验。

Other docs:

- `README.md`: deployment and operations.
- `docs/ROADMAP.md`: the current phase: goal and acceptance scenarios, the must-have list for the release (定版线) and the later list (理想线), what is out of scope, design notes for certificates and the web terminal, and what happens when the client resumes.
- `docs/PHASES.md`: the 定版线 split into stages S0–S5, each with its code changes, migrations, config keys, API additions and acceptance checks. Work through it stage by stage and tick ROADMAP §4's status column as items land.
- `docs/FEATURES.md` and `docs/PLAN.md`: feature specs and release order. F1 ships with `v0.2.0`. R1–R4 cover the real-server checklist, candidate tag and two-week trial on `release/0.2`. PLAN.md also defines F2–F4 and the migration order.
- `docs/DEMO.md`: current scope, API list, config format.
- `PRODUCT.md`: users and product principles.
- `web/DESIGN.md`: the web panel's visual system.
- `docs/CLIENT.md`: desktop client design, the server-side changes it needs (§8), milestones C1–C3 (§11).

## Commands

Toolchain: Go 1.26+, Node and pnpm. The server only runs on Linux. Development happens on Windows, and Linux-only behaviour is exercised in WSL. The desktop client also needs Rust (stable). The pnpm workspace lives at the repo root (`pnpm-workspace.yaml` lists `web`, `desktop` and `packages/*`), so run `pnpm install` from the root.

```bash
go test ./cmd/... ./internal/...                   # unit tests (also run on Windows)
go test ./internal/scheduler -run TestAllocate -v  # a single test
GOOS=linux go vet ./cmd/... ./internal/...         # also type-checks *_linux.go from Windows
cd web && pnpm typecheck                           # frontend type check (no linter is configured)
cd web && pnpm build                               # tsc + vite build into internal/webui/dist
cd packages/core && pnpm typecheck                 # shared TS code
cd desktop && pnpm typecheck                       # desktop client UI
cd desktop && pnpm tauri dev                       # run the desktop client (vite on port 17610; Rust changes restart it)
cd desktop/src-tauri && cargo test                 # Rust layer; cargo clippy and cargo fmt --check are kept clean
```

CI (`.github/workflows/ci.yml`) runs these on every push to main and every PR: Go tests and vet on Linux (so `*_linux_test.go` run too), the three typechecks, the build and `scripts/e2e.sh` on ubuntu; `cargo fmt --check`, `clippy -D warnings` and `cargo test` on Windows.

`cargo test -- --ignored` also runs `http::tests::live_server` against a real local-CA danlu: pinned CA accepted, foreign CA rejected, log SSE read to `end`. The test's doc comment lists the env vars it needs. In WSL's NAT networking, `127.0.0.1` from Windows may not reach WSL, so regenerate the test server's cert for the WSL IP with `danlu cert --regen --public-ip <ip>`.

To build the shipping binary (frontend first, then a cross-compile), run `.\scripts\build.ps1 [-Arch arm64]` on Windows or `bash scripts/build.sh [arm64]` elsewhere. The output is `dist/danlu-linux-<arch>`.

These two scripts run in WSL or Linux against the built binary:

```bash
bash scripts/e2e.sh      # end-to-end suite with fake GPUs, on port 17699
bash scripts/dev-wsl.sh  # demo server on 127.0.0.1:17600 (TLS off, fake GPUs, sample git project)
```

`scripts/s0/` is the S0 real-machine kit, run on the GPU server itself: `collect.sh` gathers a redacted report (system, systemd, cgroup `memory.events`, raw `nvidia-smi` output, per-process ownership, danlu's own state and a few API responses) and calls `gpu-procs.sh` to list every GPU process (CUDA and graphics) with its CUDA/EGL environment, `CHECKLIST.md` walks DEMO.md §0 item by item, and `jobs/` holds the submit-time test scripts (a fake multi-GPU DDP run plus CUDA OOM, Python exception, NCCL timeout and killing one worker).

- **Fake GPUs:** set `DANLU_FAKE_GPUS=N`. Use `DANLU_FAKE_BUSY=i,j` to mark cards as held by an external process.
- **Failure-classification tests:** `DANLU_BOOT_ID_FILE` points to a test boot-id file; on Linux, `DANLU_MEMORY_EVENTS` points to a test cgroup `memory.events` file containing `oom_kill N`.
- **Data directory:** `DANLU_HOME` replaces `~/.danlu`.
- **Frontend dev server:** `pnpm dev` proxies `/api` to the `dev-wsl.sh` server. Open that server's entrance URL once first to get the gate cookie.

## Architecture

`cmd/danlu/main.go` holds every subcommand (`init`, `serve`, `install-service`, `passwd`, …). `serve` wires the parts together:

1. `store` (SQLite)
2. `gpu.Monitor` (polls `nvidia-smi` every 2 s; the same query also asks for fan/clocks/power-limit/P-State, and falls back to the basic query for ten minutes if a driver rejects the extra columns or the extended query fails slowly — the fallback is time-limited, so a temporary driver failure doesn't lose those metrics until the next restart, and the extended query can never keep the scheduler from getting card data; within one round the extended query and its fallback share one `nvidia-smi` timeout) and `system.Monitor` (CPU and memory), both a `sample.Sampler`: `Latest()` returns an error once no sample has completed for 20 s, so stale data is never trusted. `system.ResourceMonitor` adds the resource page's detail sampling (CPU per-core/load/frequency/temperature/RAPL or amd_energy power, memory/swap, network, physical-disk I/O, root disk, top processes) every 2 s, keeps one hour of history in memory, scans processes every 10 s, and has its own sampler so a stuck `/proc` read only stales the resource page. `system.SelfMonitor` adds danlu's own footprint every 30 s: the data directory's apparent size by category (db/logs/snapshots/run/envs/backup/other; hard links counted once, symlinks not followed) and the SQLite size (page count × page size, freelist, WAL/SHM files). Its scan has a 30 s timeout with the bounded-stat busy pattern and its own sampler, so stuck storage only stales this data; the resource monitor reads its cache to chart both sizes in the one-hour history, and `/api/resources` merges it as `self`.
3. `runner`, which implements `scheduler.Executor`
4. `scheduler.Manager`
5. `auth`
6. `api.Server`, which also serves the embedded `internal/webui`
7. `tlsutil.CertManager`: the certificate lives in memory and is served through `GetCertificate`; it reloads changed cert files every minute and renews the local-CA cert daily

Before opening the database, `serve` takes an exclusive `flock` on `~/.danlu/serve.lock` (Linux only), so a second instance on the same data directory exits before it migrates, recovers or schedules anything. It also binds the port before `Recover()`, so a port clash never touches jobs either.

On SIGTERM, `serve` cancels the requests' base context (log SSE exits), waits for in-flight requests (`Shutdown`, 5 s), then waits for `Manager.Run` to return, which itself waits for jobs being launched to reach the database. Only then does the database close. Background `danlu.json` writes get at most `ManifestTimeout` (10 s) on the way out, because an output directory on stuck network storage must not hold up shutdown. For the same reason `serve` waits at most 10 s for the retention pruner; a job it had already claimed is finished on the next start (`pruning = 1`).

### Request gating (`internal/auth`)

- **Gate cookie.** Visiting `/<entrance>` sets it. Without a valid gate cookie, every path returns an empty 404.
- **Sessions.** `/api/login` needs only the gate. Every other `/api/*` route also needs a session; on a 401 the frontend redirects to `/login`.
- **CSRF.** Non-GET API calls must send `Content-Type: application/json`, which together with `SameSite=Strict` cookies is the CSRF defence.
- **Long-lived connections.** The middleware only sees the handshake, so the terminal WebSocket re-checks the handshake's credentials with `Auth.Authorized` before every input and every 5 s. A password change (including `danlu passwd` from another process), logout, session expiry or revocation (Settings → 登录会话) or token revocation closes it with 4401, and the panel goes to `/login`. `Authorized` re-checks the session only, not the gate, so changing the entrance doesn't cut off the changer's own open terminals and log streams.
- **Login rate limit.** Logins from one IP run one at a time (`Auth.lockIP`), and `RecordFailure` bumps the counter and sets the lock in one statement, so concurrent guesses can't outrun `max_failures` or clear a lock. Across all IPs (`internal/auth/throttle.go`): once more than 30 logins have failed in the last minute, password checks are spaced 1 s apart, and a request that would wait more than 15 s gets a 429 (`ErrLoginBusy`). At most two bcrypt checks run at once, so a flood of logins can't fill the CPU.
- **CA distribution.** The first CA a device trusts comes over SSH (`danlu ca`), never from the panel over an unverified connection.
- **Device tokens.** These are for the desktop client.
  - A request with a valid `Authorization: Bearer <token>` skips the gate and session checks. An invalid token still gets the empty 404, so the server stays unrecognisable.
  - Tokens are long-lived and stored only as SHA-256 hashes (`device_tokens` table).
  - They can only be created with `danlu token create` on the server (over SSH, which proves identity). They can be listed and revoked from the CLI and from the panel's Settings page (`/api/tokens`).
  - `last_used_at` is written at most once a minute per token.

### Job lifecycle

The job lifecycle spans `scheduler`, `runner`, `procfs`, `snapshot` and `store`.

- **State changes.** All of them go through `scheduler.Manager` under a single lock. Only millisecond work happens under it.
- **OS seam.** The manager talks to the OS only through the `Executor` interface (`*runner.Runner` in production) and reads GPUs through `GPUSource`, so the state machine is unit-tested with fakes in `scheduler/manager_test.go`. Add a case there when you change scheduling, launch, cancel or recovery.
- **Scheduling.** Strict FIFO: when the job at the head of the queue doesn't fit, nothing behind it starts. Jobs get whole GPUs only, and a card with any external process, a stray process of another job, or unknown memory above the threshold is never allocated. While GPU data is untrusted (`Latest()` errors: stale, or `gpu.ErrProcsUnknown`), no GPU job starts.
- **GPU process sources.** `--query-compute-apps` misses graphics processes (EGL/OpenGL rendering, which picks cards by EGL index and ignores `CUDA_VISIBLE_DEVICES`). When a card's used memory exceeds its listed processes, `gpu.SMI` also parses `nvidia-smi -q -d PIDS` (best effort, 3 s) and matches cards by PCI bus id: every process of this instance's jobs is listed, external ones only above the unknown-memory threshold, so Xorg-style processes on every card don't block allocation.
- **Three-step start.** Write before you fork, so a job can never start twice:
  1. Reserve, under the lock: the job is saved as `running` with `pid = 0`, its GPUs, port and `boot_id`. If that write fails, nothing starts.
  2. Prepare, outside the lock (`launch`): the snapshot (size-capped, 10 s timeout; on failure it is noted on the job, which starts anyway), `LoadEnv` (`.bashrc`, 10 s) and the first `danlu.json` write (10 s; a timeout fails the job). `runner` serialises manifest writes per output directory, so one stuck directory only blocks its own job.
  3. Start, under the lock: cancelled meanwhile → `cancelled`; danlu stopping → back to the queue; otherwise fork/exec and save the pid. Jobs in this window are in `Manager.starting`.
- **Unsaved state.** When a save fails, the job goes into `Manager.pending` and the in-memory copy wins until a later round saves it.
- **Launch.** `runner` writes two scripts into `~/.danlu/run/<id>/`: a fixed wrapper, `run.sh`, and the user command, `cmd.sh`. It starts them with `setsid`.
  - Parameters reach the wrapper only through env vars; nothing user-supplied is interpolated into a script.
  - The wrapper first writes a `pid` file, activates conda and writes an `exitcode` file when the job ends. After `conda activate` it restores `CUDA_VISIBLE_DEVICES` and `CUDA_DEVICE_ORDER` from `DANLU_GPUS` if the env's own variables changed them, and logs a `[danlu]` line saying so.
  - stdout and stderr go straight to `~/.danlu/logs/<id>.log`, so jobs survive a danlu restart.
- **Injected env.** `CUDA_VISIBLE_DEVICES`, `PET_MASTER_PORT` (unique per job; torchrun's default master port), `WANDB_TAGS=danlu-<id>`, and the `DANLU_*` variables. Users cannot override the reserved keys (`runner.ReservedEnv`).
- **Liveness and restart.** A job is identified by its pid plus the process start time from `/proc`, which guards against pid reuse; with no start time, `DANLU_RUN_DIR` in its environ is checked instead. `Manager.Recover()` re-attaches to running jobs at startup. A `running` job with `pid = 0` that isn't being launched goes through `reconcile`: adopt it from the pid file, finalize it if it already ran, or requeue it if it never started. A job that vanished without an exit code is "服务器重启" if `boot_id` changed, else "进程被外部终止". The systemd unit must keep `KillMode=process`; without it, restarting danlu kills the training processes.
- **Cancel.** SIGTERM goes to the job's process group and to every process whose `/proc/<pid>/environ` contains `DANLU_RUN_DIR=<data dir>/run/<id>`. The env scan is needed because torchrun workers start new sessions, so the process group alone misses them. SIGKILL follows after `kill_grace`.
- **Process identity is the run dir, not the job id.** Job ids restart at 1 in every data directory, so two danlu instances under one Linux user would share `DANLU_JOB_ID` values. Cancel, cleanup, `Adopt`, `Alive` and GPU ownership (`procfs.JobOf`, which only accepts dirs under this instance's `run/`) all match `procfs.JobMarker` (`DANLU_RUN_DIR`). `DANLU_JOB_ID` is still injected for users' scripts but never used to find processes.

### Invariants that span files

- **Migrations.** `store` migrations are an append-only list indexed by `PRAGMA user_version`. Never edit an existing entry; append a new one. Each runs in a transaction together with its version bump; before upgrading an existing database, `store.Open` backs it up to `backup/` with `VACUUM INTO` (last 5 kept), and it refuses a database newer than the binary.
- **Config vs runtime state.** The server process never writes `config.toml`; only the CLI does (`init`, `port`, `cert`, `passwd`), and those changes need a restart. State that changes at runtime lives in the `settings` table. The password hash is there (`auth.password_hash`), so panel and `danlu passwd` changes apply at once; a legacy `[auth] password_hash` in the config is imported once at startup. `config.Load` records unknown keys in `Config.Unknown` (never written back) and logs a warning, so a misspelt key doesn't fail silently. Small state files (`config.toml`, `secret.key`, the CA and server certs and keys) are replaced through `internal/atomicfile` (temp file, fsync, rename). The server key is written before its cert, and `tlsutil.EnsureServer` reissues when the pair doesn't match.
- **Security settings.** The entrance, session TTL, max failures and lockout (`auth.Security`) also live in the `settings` table (`auth.entrance`, `auth.session_ttl`, `auth.max_failures`, `auth.lockout`; durations in seconds). The config values are only defaults for keys the database doesn't have. The panel (`POST /api/settings/security`) writes only the keys that changed and applies them at once; `danlu entrance --reset` writes the database and the running server picks it up within `securityRefresh` (2 s). `serve` refuses to start if it can't read them, `init --force` clears them, and `info`, `entrance` and `trace` print the effective address via `loadEffective`, which must never be passed to `config.Save`. Changing the entrance invalidates every gate cookie (the HMAC covers the entrance) except the one re-issued to the requesting browser; shortening the TTL clamps existing sessions in the same transaction as the settings write (`store.SaveSecurity`). The in-memory settings and the new gate cookie are applied only after that transaction commits.
- **Panel-editable runtime settings (`internal/settings`).** The scheduling and alert values (`runtime.fail_fast`, `kill_grace`, `unknown_gpu_memory_mb`, `disk_min_gb`, `disk_warn_gb`, `silence_alert`, `snapshot_max_file_mb`, `snapshot_max_total_mb`), `terminal.session_lifetime` and retention (`retention.keep_days`, `retention.max_total_gb`) follow the same rule: decimal integers in the API unit (seconds, GB, MB, days) in the `settings` table, config values only as defaults, panel ranges checked only on changed keys, `null` restores the config value. `settings.Runtime.Current()` re-reads the DB at most every 2 s. Consumers pull it through getters on every use; nothing is pushed: the scheduler takes `Options.Live` **before** locking `m.mu` in `step`/`Recover`/`ResumeQueue` and copies it into `m.opt`; snapshot limits are read in `reserve` and passed to `launch`; `sampleLogs` always runs and a zero silence floor means "never silent"; the disk/resource monitors, `gpu.SMI.Threshold`, `terminal.Manager.SetLifetime` and `retention.Pruner.Live` take funcs. Never mutate `s.Cfg` (the API's doctor uses a copy via `effectiveConfig`), and never call into a consumer while holding `settings.Runtime`'s lock (`Wake`/`Kick` run after `Update` returns). Saving retention kicks a prune at once; `POST /api/settings/retention/preview` is a dry run that shares the prune lock (409 `retention.ErrBusy`). It returns 504 at its 20 s deadline even when a filesystem call is stuck: `Pruner.Preview` stops waiting on the background scan, which checks ctx at every directory entry and releases the lock at its next checkpoint. Output directories are resolved off-thread with a 30 s limit. When deleting under a snapshot rerun's `code/`, the pruner checks every parent directory with `Lstat` (`realParents`): `os.Root` still follows relative links that stay inside the data dir, and a task-made link could otherwise lead the pruner into another job's files. `serve` refuses to start if it can't read them and logs which keys the DB overrides; `loadEffective`/`overlayPanelSettings` apply them for `info` (which prints the overrides), `doctor` and `prune`; `init --force` clears them (not `ui.*`). Keys are reset by explicit list, never by prefix: `retention.env_gc` is the pruner's own state.
- **Panel preferences.** Behaviour defaults shared by phone and desktop (`ui.*` keys, JSON values; spec table in `internal/settings/prefs.go`, mirrored by `PREF_DEFAULTS` in `web/src/lib/prefs.ts`) come back in `GET /api/settings` as `prefs` and are saved with `POST /api/settings/prefs`. Display-only preferences (theme, log and terminal font size) stay in `localStorage` (`web/src/lib/theme.ts`, `web/src/lib/localPrefs.ts`); `index.html` applies theme and log font size before first paint.
- **External commands.** Run helpers (`git`, `nvidia-smi`, `conda`, the `.bashrc` loader) through `internal/execx`: its own session, the whole group killed on timeout, and `WaitDelay` so a background child holding the pipe can't hang the caller. Training jobs themselves are started by `runner`.
- **HTTP timeouts.** The server has read, write and idle timeouts. A long-lived handler must renew its write deadline (log SSE does, per write) or clear it (downloads use `noWriteTimeout`).
- **Log banner.** `logs.BannerPrefix` must match the `[danlu] 任务…` lines that the wrapper in `runner.go` echoes. `LastLine` skips those lines so job rows show training output.
- **Linux-only code.** It lives in `*_linux.go`, with `*_other.go` stubs, so everything compiles and unit-tests on Windows.
- **Static binary.** Everything is pure Go (`modernc.org/sqlite`, `CGO_ENABLED=0`), producing one static binary.
- **Embed placeholder.** `internal/webui/dist/.gitkeep` keeps `go:embed` compiling before the frontend is built. The build scripts recreate it after vite empties the directory.

### Frontend (`web/`)

- **Stack.** React 19, Vite, Tailwind v4, react-router, and TanStack Query. Data is polled; live logs come over SSE.
- **Code layout.**
  - Pages: `src/pages`. Settings is split into `/settings/<group>` sub-pages under `src/pages/settings/` (side nav at `md+`, a select below). Editable sections use `useServerForm` (base snapshot, only changed keys submitted, re-sync from the server only while not dirty); runtime rows use `RuntimeForm`/`SettingRow`. Pages read shared preferences with `usePrefs()`.
  - Shared primitives: `src/components/ui.tsx`
  - API client: `src/lib/api.ts`, which holds only the fetch/EventSource transport. Types and request functions come from `@danlu/core`
  - Design tokens: CSS variables in `src/index.css`, with a light set and a dark set

### Shared code (`packages/core`)

`@danlu/core` is imported as TS source; there is no build step. It contains:

- API types and `createApi(transport, opts)`. The transport is injected: the panel's uses fetch, the client's goes through Tauri.
- Formatting, including `compactProgress` for tqdm lines.
- Log rendering: tqdm `\r` collapsing and ANSI via ansi_up.
- The `useJobLog` hook: tail, then SSE, with reconnect and byte-offset dedupe.

It has no components and no styles; the two UIs are designed independently. The ANSI class colours live in each app's CSS.

### Desktop client (`desktop/`)

C1 and C2 are built (`docs/CLIENT.md` §11) and C3 is paused (see Direction): a tray app with a main window (overview, history, servers, settings) and a popover beside the tray. Keep these constraints:

- **Layout.** `desktop/src` is React + TS + Tailwind + TanStack Query; `desktop/src-tauri` is Rust.
  - Two pages: `index.html` (main window) and `tray.html` (popover, `TrayApp.tsx`).
  - `src/lib/backend.ts` wraps every Tauri command and builds one `Api` per server (`apiFor`).
  - `src/lib/fleet.ts` subscribes to the Rust poller's `fleet` event. `src/lib/workspace.tsx` owns the right-hand pane (job detail or submit form) and `draftFrom` for copy/rerun.
  - Rust modules:
    - `servers.rs`: servers.json in the app config dir, tokens in the keyring.
    - `state.rs`: `state.json` in the app data dir, holding prefs, read failures and per-server notification watermarks.
    - `fleet.rs`: polls every server every 3 s (10 s when only the tray is up), keeps 5-minute GPU history, decides notifications and the tray state, and emits `fleet`.
    - `tray.rs`: tray icon, popover placement, close-to-tray.
    - `ops.rs`: upgrade and service management.
    - `ssh.rs` and `http.rs`.
- **Polling lives in Rust, not the webview.** Hidden webviews get their timers throttled, so notifications and the tray would go stale. The UI never polls the fleet itself. After a mutation, call `refresh_fleet` to start the next round at once. Per-job queries (`['job', server, id]`) are invalidated on every `fleet` event.
- **Notifications** fire once per fact: a failure whose `ended_at` is past the server's watermark and that hasn't been read (several in one round, or after the computer slept, are merged into one); offline for 30 s, then a recovery notice; an `nvidia-smi` error; an external process on a card one of your jobs is using; and an external process blocking the head of the queue. The last two are dismissable alerts (`mark_read` with the alert key). Each `notify` also writes `[通知] …` to stderr, which is visible in `pnpm tauri dev`.
- **Talking to servers.** Every HTTP request goes through the Rust commands `api_request` and `api_stream`/`api_stream_close`, which only accept `/api/` paths.
  - In local-ca mode the Rust layer trusts only that server's CA. It re-checks the CA's fingerprint before building a client, using reqwest `tls_certs_only` with rustls and ring.
  - The webview never calls a server directly, and certificate verification is never disabled.
  - The device token lives in the OS credential store via the `keyring` crate (service `danlu`, account = server name; Windows Credential Manager, Keychain on macOS).
- **Operations over SSH.** Adding a server, upgrades and service management run the system OpenSSH (`ssh.exe` and `scp.exe` on Windows), so `~/.ssh/config` and ssh-agent apply.
  - Each operation is one `danlu` subcommand, with `--json` output where the client parses it (`info --json`, `token create --json`, `replace --json`, later `doctor --json`). Service management uses `restart`, `logs`, `entrance --reset`, `port`, `cert --regen` and `passwd --password-stdin`. Add a subcommand on the server instead of scripting shell logic in the client.
  - Secrets go through `ssh::run_danlu_input`, which passes them as a quoted heredoc on stdin, never on a command line.
  - An upgrade scps the binary to `<exe>.new`, then runs `<exe>.new replace --json <exe>`: the new binary renames itself over the old one and restarts the service. The binary comes from the bundle's `bin/` resource dir, or during development from the repo's `dist/`.
  - `ssh::run_danlu` pipes a tiny POSIX script to `sh -s`, so the remote login shell doesn't matter. The script adds `~/bin` and `~/.local/bin` to PATH.
  - ssh runs with `BatchMode=yes` and never auto-accepts unknown host keys: the CA and the token travel over that connection.
- **Versions.** Upgrades go to one server first; the rest follow only after the client confirms the new version is serving and the running jobs were re-attached.
  - **Target (C3 release work):** the installer bundles `danlu-linux-amd64` and `danlu-linux-arm64` under `bin/`, and client and server share one version number.
  - **Today:** `tauri.conf.json` bundles no resources, so `ops::find_binary` falls back to the repo's `dist/`, a path compiled in from `CARGO_MANIFEST_DIR`. That fallback only works on the build machine.
  - **Version checks:** the client compares only `api_version`. Server builds are stamped `dev-<timestamp>` unless `VERSION` is set, and the client is still `0.1.0`.
- **Not Windows-only.** macOS comes later, so keep platform specifics behind small seams: credential store, tray versus menu bar, `Ctrl` versus `⌘` shortcuts, window controls. Don't use Mica, acrylic or vibrancy.
- **Client-local state.**
  - In Rust `state.json`, because the tray and notifications need it: read failures, prefs and notification watermarks.
  - In `localStorage`, shared by both windows: the project × server → workdir and conda env mapping (`src/lib/local.ts`).
  - A project is the last path component of the workdir. Command history is read from the servers' job records, not stored locally.
- **Debugging the real app.** Start it with `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=9222 pnpm tauri dev`. Both pages then show up at `http://127.0.0.1:9222/json` for CDP: evaluate JS, take screenshots, emulate dark mode.

## Conventions

- **Language.** Code comments, UI copy, docs and commit messages are in Chinese. UI copy is plain, with alchemy (炼丹) flavour only in empty states and titles.
- **Web panel UI changes** follow `web/DESIGN.md` and `PRODUCT.md`:
  - Colour encodes job status only. The cinnabar brand colour appears only on the flame mark.
  - Never lower information density; a sparser screen counts as a regression.
  - Phone and desktop are equally important.
  - Support both themes. Log and terminal panes stay dark.
  - `web/.impeccable/` holds the panel's design context for the impeccable skill (see "Impeccable skill" below).
- **Desktop client UI** follows `desktop/DESIGN.md` (generated from the C2 build; `docs/CLIENT.md` §5.4 is the original brief) and `desktop/PRODUCT.md`. It is unrelated to the panel's look: a platform-neutral modern developer tool, finished to the level of Linear or Raycast but more restrained.
  - Neutral greys; colour is reserved for job status and the cinnabar flame mark, so selection and the primary button are neutral too.
  - Solid backgrounds; no Mica, acrylic or vibrancy.
  - Bundled Geist and Geist Mono, with monospace for ids, numbers, commands, paths and logs.
  - 10px panels and 6px controls; depth comes from background steps and hairlines, with soft shadows only on the floating panels.
  - Light and dark themes follow the system; motion is restrained and honours reduced motion.
  - Keyboard-first, and never lower information density.
- **Impeccable skill.** Always name the target directory (`web/src` or `desktop/src`). Impeccable resolves context per app, and without a target it uses the repo root. The full layout and checklist are in `docs/CLIENT.md` §9.2.
  - **Monorepo mode.** Impeccable treats `web/` and `desktop/` as separate apps because the root `pnpm-workspace.yaml` lists them; this works even before `desktop/` exists. Don't add `projectRoots` to `.impeccable/config.json`, so the app list stays in one place. In this mode impeccable picks the nearest `PRODUCT.md` and `DESIGN.md` walking upward from the target, and reads surface briefs only from that app's own `.impeccable/surfaces/`, with `primary_target` relative to the app (for example `src`).
  - **Panel.** Context is the root `PRODUCT.md` (inherited), `web/DESIGN.md`, `web/.impeccable/design.json` and `web/.impeccable/surfaces/web-src.md`. Never put briefs or a `DESIGN.md` back at the repo root: root briefs are ignored, and a root `DESIGN.md` would be inherited by `desktop/`.
  - **Desktop client.** Context is `desktop/PRODUCT.md`, `desktop/DESIGN.md`, `desktop/.impeccable/design.json` and `desktop/.impeccable/surfaces/desktop-src.md` (`primary_target: "src"`; the surface-brief tool writes `desktop/src` there, so fix it by hand). A design run on `desktop/` must never write to `web/DESIGN.md`.
- **Line endings.** LF everywhere (see `.gitattributes`), because the shell scripts run in WSL and Linux.
