//! 轮询所有服务器：汇总 GPU 和任务、积累利用率曲线、决定发哪些通知、更新托盘图标。
//!
//! 放在 Rust 里而不是界面里：窗口隐藏后网页的定时器会被浏览器降频，常驻托盘时也要按时刷新和通知。
//! 每轮结束把整份汇总通过 "fleet" 事件推给所有窗口，界面只负责展示。

use std::collections::{HashMap, HashSet};
use std::sync::Mutex;
use std::time::Duration;

use futures_util::future::join_all;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use tauri::{AppHandle, Emitter, Manager, State};
use tauri_plugin_notification::NotificationExt;
use tokio::sync::Notify;

use crate::http::Http;
use crate::servers::Servers;
use crate::state::LocalState;
use crate::tray::{self, TrayState};

/// 主窗口或迷你总览开着时的刷新间隔
const ACTIVE: Duration = Duration::from_secs(3);
/// 只在托盘里时的刷新间隔，通知最多晚这么久
const IDLE: Duration = Duration::from_secs(10);
/// 连续连不上这么久才算离线并通知
const OFFLINE_AFTER_MS: i64 = 30_000;
/// 利用率小曲线保留的时长
const HISTORY_MS: i64 = 5 * 60_000;
/// 总览显示多久之内结束的任务
const RECENT_SEC: i64 = 86_400;
/// 两轮成功之间隔了这么久，说明电脑睡过：这期间的失败合成一条通知补报
const GAP_MS: i64 = 60_000;

#[derive(Clone, Default, Serialize)]
pub struct ServerSnap {
    pub name: String,
    /// 最近一轮是否连上了
    pub online: bool,
    pub error: Option<String>,
    /// 最后一次成功拿到数据的时间（毫秒），0 表示从没连上过
    pub updated_at: i64,
    /// 开始连不上的时间（毫秒）
    pub failing_since: Option<i64>,
    /// /api/gpus 的 gpus，连不上时保留最后一次的
    pub gpus: Value,
    /// nvidia-smi 报错
    pub gpu_error: Option<String>,
    /// 运行中、排队和 24 小时内结束的任务
    pub jobs: Vec<Value>,
    /// /api/me：版本和接口版本
    pub me: Option<Value>,
    /// 每张卡最近 5 分钟的利用率：[时间（毫秒）, 利用率]
    pub util: Vec<GpuHistory>,
}

#[derive(Clone, Serialize)]
pub struct GpuHistory {
    index: i64,
    points: Vec<(i64, u8)>,
}

/// 服务器异常。offline 和 gpu_error 随状态恢复自动消失；external、blocked 可以在界面上确认（标为已读）
#[derive(Clone, Serialize)]
pub struct Alert {
    pub key: String,
    pub server: String,
    pub kind: &'static str,
    pub text: String,
}

#[derive(Clone, Default, Serialize)]
pub struct FleetView {
    pub servers: Vec<ServerSnap>,
    pub alerts: Vec<Alert>,
    /// 已读的失败任务和已确认的提醒
    pub read: Vec<String>,
    pub polled_at: i64,
}

#[derive(Default)]
pub struct Fleet {
    view: Mutex<FleetView>,
    wake: Notify,
    track: Mutex<Tracker>,
}

/// 通知的去重记录：同一件事只报一次
#[derive(Default)]
struct Tracker {
    offline_notified: HashSet<String>,
    gpu_error_notified: HashMap<String, String>,
    external_notified: HashSet<String>,
    blocked_notified: HashSet<String>,
    tray: Option<TrayState>,
    /// 最近一条通知指向的任务或服务器：点通知重新激活客户端时定位到它
    last_target: Option<(Focus, i64)>,
}

#[derive(Clone, Serialize)]
pub struct Focus {
    /// "job"（key 是 gpu1-42）或 "server"（key 是服务器名）
    pub kind: &'static str,
    pub key: String,
}

impl Fleet {
    /// 立即开始下一轮（添加服务器、提交任务之后）
    pub fn poke(&self) {
        self.wake.notify_one();
    }

    pub fn snapshot(&self) -> FleetView {
        self.view.lock().unwrap().clone()
    }

    /// 最近 10 分钟内发过的通知指向哪里
    pub fn recent_target(&self) -> Option<Focus> {
        let t = self.track.lock().unwrap();
        t.last_target.as_ref().filter(|(_, at)| now_ms() - at < 10 * 60_000).map(|(f, _)| f.clone())
    }
}

fn now_ms() -> i64 {
    std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_millis() as i64).unwrap_or(0)
}

// ---------- 用来判断的字段（其余原样转给界面） ----------

#[derive(Deserialize)]
struct JobLite {
    id: i64,
    name: String,
    status: String,
    #[serde(default)]
    exit_code: Option<i64>,
    #[serde(default)]
    fail_reason: String,
    #[serde(default)]
    fail_kind: String,
    #[serde(default)]
    fail_detail: String,
    #[serde(default)]
    ext_seen: bool,
    #[serde(default)]
    ended_at: Option<i64>,
    #[serde(default)]
    gpus_req: i64,
    #[serde(default)]
    gpus_pinned: Vec<i64>,
    #[serde(default)]
    queue_pos: Option<i64>,
    /// 暂缓的排队任务留在原位，但不算队首（服务器的调度和告警都跳过它）
    #[serde(default)]
    held: bool,
    #[serde(default)]
    error_line: Option<String>,
}

#[derive(Deserialize)]
struct GpuLite {
    index: i64,
    util: i64,
    job: Option<JobRefLite>,
    #[serde(default)]
    external: Vec<ProcLite>,
    #[serde(default)]
    unknown_mem_mb: i64,
    /// 收下这张卡的 GPU 预约：丹炉不分配它，卡上用户自己的进程也不算挡住队首。旧版服务器没有这个字段
    #[serde(default)]
    reservation: Option<Value>,
}

#[derive(Deserialize)]
struct JobRefLite {
    id: i64,
}

#[derive(Deserialize)]
struct ProcLite {
    pid: i64,
    #[serde(default)]
    user: String,
    #[serde(default)]
    used_mem_mb: i64,
    /// 不为 0 时是这台服务器上另一个任务跑过来的进程
    #[serde(default)]
    job_id: i64,
}

fn parse<T: for<'de> Deserialize<'de>>(v: &[Value]) -> Vec<T> {
    v.iter().filter_map(|x| serde_json::from_value(x.clone()).ok()).collect()
}

// ---------- 轮询 ----------

struct Polled {
    gpus: Value,
    jobs: Vec<Value>,
    me: Option<Value>,
}

async fn poll_one(servers: &Servers, http: &Http, name: &str, with_me: bool) -> Result<Polled, String> {
    let since = now_ms() / 1000 - RECENT_SEC;
    let ended_path = format!("/api/jobs?order=ended&ended_after={since}&size=100");
    let (gpus, active, ended) = tokio::join!(
        http.get_json(servers, name, "/api/gpus"),
        http.get_json(servers, name, "/api/jobs?status=running,queued&size=200"),
        http.get_json(servers, name, &ended_path),
    );
    let (gpus, active, ended) = (gpus?, active?, ended?);
    let me = if with_me { http.get_json(servers, name, "/api/me").await.ok() } else { None };
    let mut jobs = active["jobs"].as_array().cloned().unwrap_or_default();
    jobs.extend(ended["jobs"].as_array().cloned().unwrap_or_default());
    Ok(Polled { gpus, jobs, me })
}

fn foreground(app: &AppHandle) -> bool {
    let shown = |label: &str| {
        app.get_webview_window(label)
            .map(|w| w.is_visible().unwrap_or(false) && !w.is_minimized().unwrap_or(false))
            .unwrap_or(false)
    };
    shown("main") || shown("tray")
}

pub fn spawn(app: AppHandle) {
    tauri::async_runtime::spawn(async move {
        let mut round: u64 = 0;
        loop {
            poll_round(&app, round).await;
            round += 1;
            let wait = if foreground(&app) { ACTIVE } else { IDLE };
            let fleet = app.state::<Fleet>();
            tokio::select! {
                _ = tokio::time::sleep(wait) => {}
                _ = fleet.wake.notified() => {}
            }
        }
    });
}

async fn poll_round(app: &AppHandle, round: u64) {
    let servers = app.state::<Servers>();
    let http = app.state::<Http>();
    let fleet = app.state::<Fleet>();
    let names = servers.names();
    let prev: HashMap<String, ServerSnap> =
        fleet.view.lock().unwrap().servers.iter().map(|s| (s.name.clone(), s.clone())).collect();
    // /api/me 不常变：每 10 轮或者还没有时才取
    let results = join_all(names.iter().map(|n| {
        let with_me = round.is_multiple_of(10) || prev.get(n).is_none_or(|s| s.me.is_none());
        poll_one(&servers, &http, n, with_me)
    }))
    .await;

    let now = now_ms();
    let mut snaps = vec![];
    for (name, res) in names.iter().zip(results) {
        let mut s = prev.get(name).cloned().unwrap_or_else(|| ServerSnap { name: name.clone(), ..Default::default() });
        match res {
            Ok(p) => {
                s.online = true;
                s.error = None;
                s.failing_since = None;
                s.updated_at = now;
                s.gpu_error = p.gpus["error"].as_str().filter(|e| !e.is_empty()).map(String::from);
                s.gpus = p.gpus["gpus"].clone();
                s.jobs = p.jobs;
                if p.me.is_some() {
                    s.me = p.me;
                }
                push_history(&mut s, now);
            }
            Err(e) => {
                s.online = false;
                s.error = Some(e);
                s.failing_since.get_or_insert(now);
            }
        }
        snaps.push(s);
    }

    let local = app.state::<LocalState>();
    let read = local.read_set();
    let alerts = evaluate(app, &fleet, &local, &snaps, &prev, &read, now);
    let tray_state = tray_state(&snaps, &alerts, &read);
    {
        let mut t = fleet.track.lock().unwrap();
        if t.tray != Some(tray_state) {
            t.tray = Some(tray_state);
            tray::set_state(app, tray_state);
        }
    }
    let view = FleetView { servers: snaps, alerts, read: read.into_iter().collect(), polled_at: now };
    *fleet.view.lock().unwrap() = view.clone();
    let _ = app.emit("fleet", view);
}

fn push_history(s: &mut ServerSnap, now: i64) {
    let gpus: Vec<GpuLite> = parse(s.gpus.as_array().map(|v| v.as_slice()).unwrap_or(&[]));
    for g in gpus {
        let h = match s.util.iter_mut().find(|h| h.index == g.index) {
            Some(h) => h,
            None => {
                s.util.push(GpuHistory { index: g.index, points: vec![] });
                s.util.last_mut().unwrap()
            }
        };
        h.points.push((now, g.util.clamp(0, 100) as u8));
        h.points.retain(|(t, _)| now - t <= HISTORY_MS);
    }
}

// ---------- 通知和提醒 ----------

/// 失败原因按机器码合成当前语言（和 core 的 failReasonText 同一套逻辑）；
/// 旧服务器没有分类时回退 fail_reason 原文。
fn fail_reason_text(j: &JobLite) -> String {
    use crate::messages::t;
    let fallback = || {
        if !j.fail_reason.is_empty() {
            j.fail_reason.clone()
        } else if let Some(c) = j.exit_code {
            t("fail.exit_code", &[("code", &c.to_string())])
        } else {
            t("fail.failed", &[])
        }
    };
    match j.fail_kind.as_str() {
        "cuda_oom" => t(if j.ext_seen { "fail.cuda_oom_ext" } else { "fail.cuda_oom" }, &[]),
        "host_oom" => match j.fail_detail.as_str() {
            "worker" => t("fail.host_oom_worker", &[]),
            "guess137" => t("fail.host_oom_guess", &[]),
            "confirmed" => t("fail.host_oom", &[]),
            _ => fallback(),
        },
        "nccl" => t("fail.nccl", &[]),
        "exception" => t("fail.exception", &[]),
        "reboot" => t("fail.reboot", &[]),
        "killed" => t("fail.killed", &[]),
        "signal" => {
            let sig = if !j.fail_detail.is_empty() {
                j.fail_detail.clone()
            } else if let Some(c) = j.exit_code.filter(|c| *c > 128) {
                signal_name(c - 128)
            } else {
                String::new()
            };
            if sig.is_empty() {
                // 与 core failReasonText 一致：有原文用原文，没有就说"被信号终止"，别落到笼统的"失败"
                if j.fail_reason.is_empty() {
                    t("fail.signal_generic", &[])
                } else {
                    j.fail_reason.clone()
                }
            } else {
                t("fail.signal", &[("sig", &sig)])
            }
        }
        "exit" => match j.exit_code {
            Some(c) => t("fail.exit_code", &[("code", &c.to_string())]),
            None => fallback(),
        },
        "start" => {
            // 与 core failReasonText 一致：fail_detail 是原始启动错误，译前缀后附原文；旧服务器回退原文
            if !j.fail_detail.is_empty() {
                t("fail.start_detail", &[("detail", &j.fail_detail)])
            } else if j.fail_reason.is_empty() {
                t("fail.start", &[])
            } else {
                j.fail_reason.clone()
            }
        }
        _ => fallback(),
    }
}

fn signal_name(n: i64) -> String {
    let name = match n {
        2 => "SIGINT",
        3 => "SIGQUIT",
        4 => "SIGILL",
        6 => "SIGABRT",
        7 => "SIGBUS",
        8 => "SIGFPE",
        9 => "SIGKILL",
        11 => "SIGSEGV",
        13 => "SIGPIPE",
        14 => "SIGALRM",
        15 => "SIGTERM",
        _ => "",
    };
    if name.is_empty() {
        crate::messages::t("fail.signal_num", &[("n", &n.to_string())])
    } else {
        name.into()
    }
}

fn notify(app: &AppHandle, fleet: &Fleet, title: &str, body: &str, target: Focus) {
    if !app.state::<LocalState>().prefs().notify {
        return;
    }
    // 开发时从终端能看到发了哪些通知；发布版没有控制台，这行不输出
    eprintln!("[通知] {title}：{body}");
    if let Err(e) = app.notification().builder().title(title).body(body).show() {
        eprintln!("[通知] 发送失败：{e}");
    }
    fleet.track.lock().unwrap().last_target = Some((target, now_ms()));
}

fn job_key(server: &str, id: i64) -> String {
    format!("{server}-{id}")
}

/// 算出当前的提醒，顺便发该发的通知
fn evaluate(
    app: &AppHandle,
    fleet: &Fleet,
    local: &LocalState,
    snaps: &[ServerSnap],
    prev: &HashMap<String, ServerSnap>,
    read: &HashSet<String>,
    now: i64,
) -> Vec<Alert> {
    let mut alerts = vec![];
    let mut seen_external = HashSet::new();
    let mut seen_blocked = HashSet::new();
    for s in snaps {
        let name = s.name.as_str();
        let server_focus = || Focus { kind: "server", key: name.to_string() };

        // 连不上：连续 30 秒才算，只报一次，恢复后再报一条
        let offline = s.failing_since.is_some_and(|t| now - t >= OFFLINE_AFTER_MS);
        let notified_offline = fleet.track.lock().unwrap().offline_notified.contains(name);
        if offline {
            alerts.push(Alert {
                key: format!("offline:{name}"),
                server: name.into(),
                kind: "offline",
                text: crate::messages::t(
                    "offline.alert",
                    &[("name", name), ("error", &s.error.clone().unwrap_or_default())],
                ),
            });
            if !notified_offline {
                fleet.track.lock().unwrap().offline_notified.insert(name.into());
                notify(
                    app,
                    fleet,
                    &crate::messages::t("offline.title", &[("name", name)]),
                    s.error.as_deref().unwrap_or(""),
                    server_focus(),
                );
            }
        } else if s.online && notified_offline {
            fleet.track.lock().unwrap().offline_notified.remove(name);
            notify(
                app,
                fleet,
                &crate::messages::t("recovered.title", &[("name", name)]),
                &crate::messages::t("recovered.body", &[]),
                server_focus(),
            );
        }
        if !s.online {
            continue; // 下面的判断都要新数据
        }

        // nvidia-smi 报错：同一个错误只报一次
        match &s.gpu_error {
            Some(err) => {
                alerts.push(Alert {
                    key: format!("gpu_error:{name}"),
                    server: name.into(),
                    kind: "gpu_error",
                    text: crate::messages::t("gpu_error.alert", &[("name", name), ("error", err)]),
                });
                let last = fleet.track.lock().unwrap().gpu_error_notified.insert(name.into(), err.clone());
                if last.as_ref() != Some(err) {
                    notify(app, fleet, &crate::messages::t("gpu_error.title", &[("name", name)]), err, server_focus());
                }
            }
            None => {
                fleet.track.lock().unwrap().gpu_error_notified.remove(name);
            }
        }

        let gpus: Vec<GpuLite> = parse(s.gpus.as_array().map(|v| v.as_slice()).unwrap_or(&[]));
        let jobs: Vec<JobLite> = parse(&s.jobs);

        // 外部进程只在影响你时才报：出现在你的任务正在用的卡上。
        // 别的任务跑过来的进程按任务合成一条：EGL 渲染一个任务在一张卡上就能有几十个进程
        for g in &gpus {
            let Some(job) = &g.job else { continue };
            for p in &g.external {
                let (key, title, text) = if p.job_id > 0 {
                    let key = format!("external:{name}:{}:job{}", g.index, p.job_id);
                    if seen_external.contains(&key) {
                        continue;
                    }
                    let mem: i64 = g.external.iter().filter(|q| q.job_id == p.job_id).map(|q| q.used_mem_mb).sum();
                    let text = crate::messages::t(
                        "external.job_text",
                        &[
                            ("name", name),
                            ("gpu", &g.index.to_string()),
                            ("who", &job_key(name, p.job_id)),
                            ("mem", &mem.to_string()),
                            ("job", &job_key(name, job.id)),
                        ],
                    );
                    (key, crate::messages::t("external.job_title", &[]), text)
                } else {
                    let who = if p.user.is_empty() {
                        format!("pid {}", p.pid)
                    } else {
                        crate::messages::t("external.who_user", &[("user", &p.user), ("pid", &p.pid.to_string())])
                    };
                    let text = crate::messages::t(
                        "external.text",
                        &[
                            ("name", name),
                            ("gpu", &g.index.to_string()),
                            ("who", &who),
                            ("mem", &p.used_mem_mb.to_string()),
                            ("job", &job_key(name, job.id)),
                        ],
                    );
                    (format!("external:{name}:{}:{}", g.index, p.pid), crate::messages::t("external.title", &[]), text)
                };
                seen_external.insert(key.clone());
                if !read.contains(&key) {
                    alerts.push(Alert { key: key.clone(), server: name.into(), kind: "external", text: text.clone() });
                }
                if fleet.track.lock().unwrap().external_notified.insert(key) {
                    notify(app, fleet, &title, &text, Focus { kind: "job", key: job_key(name, job.id) });
                }
            }
        }

        // 外部进程或未知显存占用挡住队首：没有这些占用时本来能跑。预约的卡和任务的卡一样算自有占用
        if let Some(head) =
            jobs.iter().filter(|j| j.status == "queued" && !j.held).min_by_key(|j| j.queue_pos.unwrap_or(i64::MAX))
        {
            let owned = |g: &GpuLite| g.job.is_some() || g.reservation.is_some();
            let busy_external: HashSet<i64> = gpus
                .iter()
                .filter(|g| !owned(g) && (!g.external.is_empty() || g.unknown_mem_mb > 0))
                .map(|g| g.index)
                .collect();
            let free =
                gpus.iter().filter(|g| !owned(g) && g.external.is_empty() && g.unknown_mem_mb <= 0).count() as i64;
            let blocked = if head.gpus_pinned.is_empty() {
                head.gpus_req > free && head.gpus_req <= free + busy_external.len() as i64
            } else {
                head.gpus_pinned.iter().any(|i| busy_external.contains(i))
            };
            if blocked {
                let key = format!("blocked:{name}:{}", head.id);
                seen_blocked.insert(key.clone());
                let unknown = if head.gpus_pinned.is_empty() {
                    busy_external.iter().any(|index| gpus.iter().any(|g| g.index == *index && g.unknown_mem_mb > 0))
                } else {
                    head.gpus_pinned.iter().any(|index| gpus.iter().any(|g| g.index == *index && g.unknown_mem_mb > 0))
                };
                let text = crate::messages::t(
                    if unknown { "blocked.unknown_text" } else { "blocked.text" },
                    &[("key", &job_key(name, head.id)), ("name", &head.name)],
                );
                if !read.contains(&key) {
                    alerts.push(Alert { key: key.clone(), server: name.into(), kind: "blocked", text: text.clone() });
                }
                if fleet.track.lock().unwrap().blocked_notified.insert(key) {
                    let title =
                        crate::messages::t(if unknown { "blocked.unknown_title" } else { "blocked.title" }, &[]);
                    notify(app, fleet, &title, &text, Focus { kind: "job", key: job_key(name, head.id) });
                }
            }
        }

        // 任务失败：比上次处理到的结束时间晚、还没读过的失败
        let failed: Vec<&JobLite> = jobs.iter().filter(|j| j.status == "failed").collect();
        let newest = failed.iter().filter_map(|j| j.ended_at).max();
        match local.watermark(name) {
            // 第一次连上这台服务器：以前的失败不补报
            None => local.set_watermark(name, now / 1000),
            Some(mark) => {
                let fresh: Vec<&&JobLite> = failed
                    .iter()
                    .filter(|j| j.ended_at.is_some_and(|e| e > mark) && !read.contains(&job_key(name, j.id)))
                    .collect();
                if let Some(n) = newest.filter(|n| *n > mark) {
                    local.set_watermark(name, n);
                }
                let slept = prev.get(name).is_some_and(|p| p.updated_at > 0 && now - p.updated_at > GAP_MS);
                if fresh.len() >= 3 || (slept && fresh.len() > 1) {
                    let names: Vec<String> = fresh.iter().take(3).map(|j| job_key(name, j.id)).collect();
                    notify(
                        app,
                        fleet,
                        &crate::messages::t("failed.many_title", &[("name", name), ("n", &fresh.len().to_string())]),
                        &names.join(&crate::messages::t("list_sep", &[])),
                        Focus { kind: "job", key: job_key(name, fresh[0].id) },
                    );
                } else {
                    for j in fresh {
                        let why = fail_reason_text(j);
                        let body = match &j.error_line {
                            Some(l) if !l.is_empty() => format!("{why} · {l}"),
                            _ => why,
                        };
                        notify(
                            app,
                            fleet,
                            &crate::messages::t("failed.title", &[("key", &job_key(name, j.id)), ("name", &j.name)]),
                            &body,
                            Focus { kind: "job", key: job_key(name, j.id) },
                        );
                    }
                }
            }
        }
    }
    // 外部进程走了、任务不再被挡：下次再出现时重新通知
    let mut t = fleet.track.lock().unwrap();
    t.external_notified.retain(|k| seen_external.contains(k));
    t.blocked_notified.retain(|k| seen_blocked.contains(k));
    alerts
}

fn tray_state(snaps: &[ServerSnap], alerts: &[Alert], read: &HashSet<String>) -> TrayState {
    let mut running = false;
    for s in snaps {
        for j in parse::<JobLite>(&s.jobs) {
            if j.status == "failed" && !read.contains(&job_key(&s.name, j.id)) {
                return TrayState::Alert;
            }
            running |= j.status == "running";
        }
    }
    if !alerts.is_empty() {
        TrayState::Alert
    } else if running {
        TrayState::Running
    } else {
        TrayState::Idle
    }
}

// ---------- 命令 ----------

#[tauri::command]
pub fn get_fleet(fleet: State<'_, Fleet>) -> FleetView {
    fleet.snapshot()
}

#[tauri::command]
pub fn refresh_fleet(fleet: State<'_, Fleet>) {
    fleet.poke();
}

/// 标为已读：失败任务（gpu1-42）或提醒（external:… / blocked:…）。马上重算托盘图标
#[tauri::command]
pub fn mark_read(app: AppHandle, local: State<'_, LocalState>, fleet: State<'_, Fleet>, keys: Vec<String>) {
    local.mark_read(&keys);
    let read = local.read_set();
    let mut view = fleet.view.lock().unwrap();
    view.alerts.retain(|a| !read.contains(&a.key));
    view.read = read.iter().cloned().collect();
    let state = tray_state(&view.servers, &view.alerts, &read);
    let mut t = fleet.track.lock().unwrap();
    if t.tray != Some(state) {
        t.tray = Some(state);
        tray::set_state(&app, state);
    }
    let _ = app.emit("fleet", view.clone());
}

#[cfg(test)]
mod tests {
    use super::*;

    fn job(v: serde_json::Value) -> JobLite {
        serde_json::from_value(v).unwrap()
    }

    // 只断言与语言无关的部分：messages 的语言是全局状态，别的测试可能同时在切换
    #[test]
    fn start_failure_uses_detail_and_falls_back() {
        let with_detail = job(serde_json::json!({
            "id": 1, "name": "a", "status": "failed", "fail_kind": "start",
            "fail_detail": "fork 失败", "fail_reason": "启动失败: fork 失败"
        }));
        let s = fail_reason_text(&with_detail);
        assert!(s.ends_with(": fork 失败") && s != "fork 失败", "{s}");

        let old_server = job(serde_json::json!({
            "id": 2, "name": "b", "status": "failed", "fail_kind": "start", "fail_reason": "写入任务清单失败: x"
        }));
        assert_eq!(fail_reason_text(&old_server), "写入任务清单失败: x");
    }
}
