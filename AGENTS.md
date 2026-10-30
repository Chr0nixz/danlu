# AGENTS.md

Guidance for coding agents working in this repository.

## What this is

Danlu (丹炉) is a control panel for GPU training jobs that runs on each GPU server. It is one Go binary with the React UI embedded, installed as a systemd user service. You reach it over the public internet; access is protected by a random port, a secret entrance path, a password, and HTTPS from a local CA. Through it you submit, queue, run, cancel and rerun jobs, and for every job it records which code version ran.

Direction:

- **Fallback panel.** This web panel is the fallback that exists on every server, for phones and for when the desktop client is unavailable. Controlling several servers at once is the job of the desktop client: a Tauri app that will live in `desktop/` in this repo, built for Windows first with macOS planned. It is designed in `docs/CLIENT.md` and not built yet (see "Desktop client" under Architecture). Servers never talk to each other; multi-server aggregation happens in the client.
- **Obsolete parts of `docs/DESIGN.md`.** The §4 peer federation (peers, request forwarding, cross-node auto-assignment, the "move to another node" feature) and the §5.1 folder-picker submit with snapshot upload are dropped. Code still reaches the servers through the user's own rsync/scp. The rest of that doc is still a valid backlog; `docs/CLIENT.md` §10 has the mapping.
- **`/api` is not frozen.** Change it freely, but update `web/src/lib/api.ts` and its callers in the same change. When the desktop client starts (milestone C1), the API types and request functions move to `packages/core` and `/api/me` gains an `api_version`; from then on an API change must keep both `web/` and `desktop/` compiling.
- **Status.** Everything in the demo scope (`docs/DEMO.md`) is built and passes in WSL with fake GPUs. None of it has been verified on a real GPU server yet.

Other docs:

- `README.md`: deployment and operations.
- `docs/DEMO.md`: current scope, API list, config format.
- `PRODUCT.md`: users and product principles.
- `web/DESIGN.md`: the web panel's visual system.
- `docs/CLIENT.md`: desktop client design, the server-side changes it needs (§8), milestones C1–C3 (§11).

## Commands

Toolchain: Go 1.26+, Node and pnpm. The server only runs on Linux. Development happens on Windows, and Linux-only behaviour is exercised in WSL. The pnpm workspace lives at the repo root (`pnpm-workspace.yaml` lists `web`, `desktop` and `packages/*`; only `web` exists so far), so run `pnpm install` from the root.

```bash
go test ./cmd/... ./internal/...                   # unit tests (also run on Windows)
go test ./internal/scheduler -run TestAllocate -v  # a single test
GOOS=linux go vet ./cmd/... ./internal/...         # also type-checks *_linux.go from Windows
cd web && pnpm typecheck                           # frontend type check (no linter is configured)
cd web && pnpm build                               # tsc + vite build into internal/webui/dist
```

To build the shipping binary (frontend first, then a cross-compile), run `.\scripts\build.ps1 [-Arch arm64]` on Windows or `bash scripts/build.sh [arm64]` elsewhere. The output is `dist/danlu-linux-<arch>`.

These two scripts run in WSL or Linux against the built binary:

```bash
bash scripts/e2e.sh      # end-to-end suite with fake GPUs, on port 17699
bash scripts/dev-wsl.sh  # demo server on 127.0.0.1:17600 (TLS off, fake GPUs, sample git project)
```

- **Fake GPUs:** set `DANLU_FAKE_GPUS=N`. Use `DANLU_FAKE_BUSY=i,j` to mark cards as held by an external process.
- **Data directory:** `DANLU_HOME` replaces `~/.danlu`.
- **Frontend dev server:** `pnpm dev` proxies `/api` to the `dev-wsl.sh` server. Open that server's entrance URL once first to get the gate cookie.

## Architecture

`cmd/danlu/main.go` holds every subcommand (`init`, `serve`, `install-service`, `passwd`, …). `serve` wires the parts together:

1. `store` (SQLite)
2. `gpu.Monitor` (polls `nvidia-smi` every 2 s)
3. `runner`
4. `scheduler.Manager`
5. `auth`
6. `api.Server`, which also serves the embedded `internal/webui`

### Request gating (`internal/auth`)

- **Gate cookie.** Visiting `/<entrance>` sets it. Without a valid gate cookie, every path returns an empty 404.
- **Sessions.** `/api/login` needs only the gate. Every other `/api/*` route also needs a session; on a 401 the frontend redirects to `/login`.
- **CSRF.** Non-GET API calls must send `Content-Type: application/json`, which together with `SameSite=Strict` cookies is the CSRF defence.
- **Device tokens (planned, C1).** For the desktop client. A request with a valid `Authorization: Bearer <token>` skips the gate and session checks; an invalid token still gets the empty 404, so the server stays unrecognisable. Tokens are long-lived, created with `danlu token create` over SSH, and revocable from the CLI and the Settings page.

### Job lifecycle

The job lifecycle spans `scheduler`, `runner`, `procfs`, `snapshot` and `store`.

- **State changes.** All of them go through `scheduler.Manager` under a single lock.
- **Scheduling.** Strict FIFO: when the job at the head of the queue doesn't fit, nothing behind it starts. Jobs get whole GPUs only, and a card with any external compute process is never allocated.
- **Snapshot on start.** Before a job starts, its working directory is snapshotted: the code files plus git commit, branch and diff. The snapshot is size-capped and has a 10 s timeout. If it fails, the failure is recorded on the job, which starts anyway.
- **Launch.** `runner` writes two scripts into `~/.danlu/run/<id>/`: a fixed wrapper, `run.sh`, and the user command, `cmd.sh`. It starts them with `setsid`.
  - Parameters reach the wrapper only through env vars; nothing user-supplied is interpolated into a script.
  - The wrapper activates conda and writes an `exitcode` file when the job ends.
  - stdout and stderr go straight to `~/.danlu/logs/<id>.log`, so jobs survive a danlu restart.
- **Injected env.** `CUDA_VISIBLE_DEVICES`, `PET_MASTER_PORT` (unique per job; torchrun's default master port), `WANDB_TAGS=danlu-<id>`, and the `DANLU_*` variables. Users cannot override the reserved keys (`runner.ReservedEnv`).
- **Liveness and restart.** A job is identified by its pid plus the process start time from `/proc`, which guards against pid reuse. `Manager.Recover()` re-attaches to running jobs at startup. The systemd unit must keep `KillMode=process`; without it, restarting danlu kills the training processes.
- **Cancel.** SIGTERM goes to the job's process group and to every process whose `/proc/<pid>/environ` contains `DANLU_JOB_ID=<id>`. The env scan is needed because torchrun workers start new sessions, so the process group alone misses them. SIGKILL follows after `kill_grace`.

### Invariants that span files

- **Migrations.** `store` migrations are an append-only list indexed by `PRAGMA user_version`. Never edit an existing entry; append a new one.
- **Log banner.** `logs.BannerPrefix` must match the `[danlu] 任务…` lines that the wrapper in `runner.go` echoes. `LastLine` skips those lines so job rows show training output.
- **Linux-only code.** It lives in `*_linux.go`, with `*_other.go` stubs, so everything compiles and unit-tests on Windows.
- **Static binary.** Everything is pure Go (`modernc.org/sqlite`, `CGO_ENABLED=0`), producing one static binary.
- **Embed placeholder.** `internal/webui/dist/.gitkeep` keeps `go:embed` compiling before the frontend is built. The build scripts recreate it after vite empties the directory.

### Frontend (`web/`)

- **Stack.** React 19, Vite, Tailwind v4, react-router, and TanStack Query. Data is polled; live logs come over SSE.
- **Code layout.**
  - Pages: `src/pages`
  - Shared primitives: `src/components/ui.tsx`
  - API client: `src/lib/api.ts`
  - Design tokens: CSS variables in `src/index.css`, with a light set and a dark set

### Desktop client (`desktop/`, planned)

None of this exists yet; `docs/CLIENT.md` is the spec. Keep these constraints when building it:

- **Layout.** `desktop/src` is React + TS + Tailwind + TanStack Query; `desktop/src-tauri` is Rust (the toolchain adds Rust and the Tauri CLI). The root pnpm workspace already lists `desktop` and `packages/*`.
- **Sharing with `web/`.** Only `packages/core` is shared: API types, request functions with an injected transport, log processing (tqdm `\r`, ANSI), log tail and stream hooks, and formatting. It contains no components and no styles; the two UIs are designed independently.
- **Talking to servers.** Every HTTP request goes through the Rust layer, which trusts only that server's local CA, pinned by fingerprint. The webview never calls a server directly, and certificate verification is never disabled. The device token lives in the OS credential store via the `keyring` crate (Windows Credential Manager; Keychain on macOS).
- **Operations over SSH.** Adding a server, upgrades and service management run the system OpenSSH (`ssh.exe` and `scp.exe` on Windows), so `~/.ssh/config` and ssh-agent apply. Each operation is one `danlu` subcommand, with `--json` output where the client parses it (`danlu info --json`, later `danlu doctor --json`). Add a subcommand on the server instead of scripting shell logic in the client.
- **Versions.** The installer bundles `danlu-linux-amd64` and `danlu-linux-arm64`, and client and server share one version number. Upgrades go to one server first; the rest follow only after the client confirms the new version is serving and the running jobs were re-attached.
- **Not Windows-only.** macOS comes later, so keep platform specifics behind small seams: credential store, tray versus menu bar, `Ctrl` versus `⌘` shortcuts, window controls. Don't use Mica, acrylic or vibrancy.
- **Client-local state.** Read/unread failures, the project × server → workdir and conda env mapping, and UI preferences. A project is the last path component of the workdir. Command history is read from the servers' job records, not stored locally.

## Conventions

- **Language.** Code comments, UI copy, docs and commit messages are in Chinese. UI copy is plain, with alchemy (炼丹) flavour only in empty states and titles.
- **Web panel UI changes** follow `web/DESIGN.md` and `PRODUCT.md`:
  - Colour encodes job status only. The cinnabar brand colour appears only on the flame mark.
  - Never lower information density; a sparser screen counts as a regression.
  - Phone and desktop are equally important.
  - Support both themes. Log and terminal panes stay dark.
  - `web/.impeccable/` holds the panel's design context for the impeccable skill (see "Impeccable skill" below).
- **Desktop client UI** does not follow `web/DESIGN.md` and is unrelated to the panel's look. Its style is specified in `docs/CLIENT.md` §5.4: a platform-neutral modern developer tool, finished to the level of Linear or Raycast but more restrained.
  - Neutral greys; colour is reserved for job status and the cinnabar flame mark, so selection and the primary button are neutral too.
  - Solid backgrounds; no Mica, acrylic or vibrancy.
  - Bundled Geist and Geist Mono, with monospace for ids, numbers, commands, paths and logs.
  - 10px panels and 6px controls; depth comes from background steps and hairlines, with soft shadows only on the floating panels.
  - Light and dark themes follow the system; motion is restrained and honours reduced motion.
  - Keyboard-first, and never lower information density.
- **Impeccable skill.** Always name the target directory (`web/src` or `desktop/src`). Impeccable resolves context per app, and without a target it uses the repo root. The full layout and checklist are in `docs/CLIENT.md` §9.2.
  - **Monorepo mode.** Impeccable treats `web/` and `desktop/` as separate apps because the root `pnpm-workspace.yaml` lists them; this works even before `desktop/` exists. Don't add `projectRoots` to `.impeccable/config.json`, so the app list stays in one place. In this mode impeccable picks the nearest `PRODUCT.md` and `DESIGN.md` walking upward from the target, and reads surface briefs only from that app's own `.impeccable/surfaces/`, with `primary_target` relative to the app (for example `src`).
  - **Panel.** Context is the root `PRODUCT.md` (inherited), `web/DESIGN.md`, `web/.impeccable/design.json` and `web/.impeccable/surfaces/web-src.md`. Never put briefs or a `DESIGN.md` back at the repo root: root briefs are ignored, and a root `DESIGN.md` would be inherited by `desktop/`.
  - **Desktop client.** It gets its own `desktop/PRODUCT.md` (written with `/impeccable init` in C1) and its own `desktop/DESIGN.md`, written by impeccable's new-work flow in C2 using `docs/CLIENT.md` §5.4 as input. Until then `impeccable doctor --json --target desktop/src` resolves no `DESIGN.md`, which is expected. A design run on `desktop/` must never write to `web/DESIGN.md`.
- **Line endings.** LF everywhere (see `.gitattributes`), because the shell scripts run in WSL and Linux.
