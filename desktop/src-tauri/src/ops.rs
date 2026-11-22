//! 运维：升级 danlu、服务管理。每个动作都是通过 SSH 执行 danlu 子命令，执行了什么和输出都交给界面显示。

use std::path::PathBuf;

use serde::Serialize;
use tauri::ipc::Channel;
use tauri::{AppHandle, Manager, State};

use crate::fleet::Fleet;
use crate::http::Http;
use crate::servers::{self, Servers};
use crate::ssh::{self, quote, RemoteError};

/// 客户端自带的服务器程序。安装包把它们放在资源目录的 bin/ 下；开发时用仓库 dist/ 里刚构建的
fn find_binary(app: &AppHandle, arch: &str) -> Option<PathBuf> {
    let file = format!("danlu-linux-{arch}");
    let bundled = app.path().resource_dir().ok().map(|d| d.join("bin").join(&file));
    let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let dev = manifest.parent().and_then(|p| p.parent()).map(|repo| repo.join("dist").join(&file));
    bundled.into_iter().chain(dev).find(|p| p.is_file())
}

#[derive(Serialize)]
pub struct Source {
    arch: &'static str,
    path: String,
    size: u64,
    /// 文件修改时间（unix 秒）
    modified: i64,
}

/// 升级会用哪些文件：界面上显示出来，开发时一眼看出是不是刚构建的
#[tauri::command]
pub fn upgrade_sources(app: AppHandle) -> Vec<Source> {
    ["amd64", "arm64"]
        .into_iter()
        .filter_map(|arch| {
            let p = find_binary(&app, arch)?;
            let meta = std::fs::metadata(&p).ok()?;
            let modified = meta
                .modified()
                .ok()
                .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
                .map(|d| d.as_secs() as i64)
                .unwrap_or(0);
            Some(Source { arch, path: p.display().to_string(), size: meta.len(), modified })
        })
        .collect()
}

fn now_sec() -> i64 {
    std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_secs() as i64).unwrap_or(0)
}

/// 升级前在跑的任务（`before_pid` 是当时的 pid）有没有被这次重启打断。只看结束时间不够：
/// 重启期间正常跑完的训练也会在重启之后才记下结束时间。
fn interrupted(before_pid: i64, restarted_at: i64, job: &serde_json::Value) -> bool {
    if job["ended_at"].as_i64().is_some_and(|e| e < restarted_at) {
        return false; // 重启之前就结束了，和升级无关
    }
    match job["status"].as_str() {
        // 接管成功的是同一个进程；升级时还在准备（pid 为 0）的任务停服时放回队列，重新启动也正常
        Some("running") => before_pid > 0 && job["pid"].as_i64() != Some(before_pid),
        Some("queued") => before_pid > 0,
        // 留下了退出码（自己跑完或报错）、被取消、启动失败都不是重启造成的；没留下退出码就没了才是
        _ => matches!(job["fail_kind"].as_str(), Some("killed" | "reboot")),
    }
}

fn remote_err(e: RemoteError) -> String {
    match e {
        RemoteError::Exit(code, stderr) => {
            if stderr.is_empty() {
                crate::messages::t("fail.exit_code", &[("code", &code.to_string())])
            } else {
                stderr
            }
        }
        RemoteError::Other(msg) => msg,
    }
}

#[derive(Serialize)]
pub struct UpgradeResult {
    ok: bool,
    from: String,
    to: String,
    /// 没通过的检查
    problem: Option<String>,
    /// 没通过时服务日志的最后几十行
    journal: Option<String>,
}

/// 升级一台：上传为 <路径>.new → 由新程序把自己换上去并重启服务 → 确认新版本在服务、升级前在跑的任务都还在跑。
/// 界面先对一台执行，通过了才让升级其余的。重启不会中断训练。
#[tauri::command]
pub async fn upgrade_server(
    app: AppHandle,
    servers: State<'_, Servers>,
    http: State<'_, Http>,
    fleet: State<'_, Fleet>,
    name: String,
    on_progress: Channel<String>,
) -> Result<UpgradeResult, String> {
    let say = |t: String| {
        let _ = on_progress.send(t);
    };
    let s = servers.get(&name).ok_or_else(|| crate::messages::t("http.no_server", &[("name", &name)]))?;

    say("$ uname -m".into());
    let machine = ssh::run_script(&s.ssh, "uname -m\n").await.map_err(remote_err)?;
    let arch = match machine.trim() {
        "x86_64" | "amd64" => "amd64",
        "aarch64" | "arm64" => "arm64",
        other => return Err(crate::messages::t("ops.arch_unsupported", &[("other", other)])),
    };
    let local = find_binary(&app, arch).ok_or_else(|| crate::messages::t("ops.binary_missing", &[("arch", arch)]))?;
    say(crate::messages::t("ops.using_local", &[("arch", arch), ("path", &local.display().to_string())]));

    let me = http.get_json(&servers, &name, "/api/me").await?;
    let from = me["version"].as_str().unwrap_or("").to_string();
    let running = http.get_json(&servers, &name, "/api/jobs?status=running&size=200").await?;
    // (任务编号, 升级前的 pid)；pid 为 0 的还在准备，没有进程
    let running: Vec<(i64, i64)> = running["jobs"]
        .as_array()
        .into_iter()
        .flatten()
        .filter_map(|j| Some((j["id"].as_i64()?, j["pid"].as_i64().unwrap_or(0))))
        .collect();
    say(crate::messages::t("ops.current_version", &[("from", &from), ("n", &running.len().to_string())]));

    let exe = if s.exe.is_empty() {
        let out = ssh::run_script(&s.ssh, "PATH=\"$HOME/bin:$HOME/.local/bin:$PATH\"; command -v danlu\n")
            .await
            .map_err(remote_err)?;
        out.trim().to_string()
    } else {
        s.exe.clone()
    };
    if !exe.starts_with('/') {
        return Err(crate::messages::t("ops.exe_path_unknown", &[("exe", &format!("{exe:?}"))]));
    }
    let staged = format!("{exe}.new");
    say(format!("$ scp {} {}:{staged}", local.display(), s.ssh));
    ssh::upload(&s.ssh, &local, &staged).await?;

    let cmd = format!("chmod +x {q} && exec {q} replace --json {e}", q = quote(&staged), e = quote(&exe));
    say(format!("$ {cmd}"));
    let restarted_at = now_sec();
    let out = ssh::run_script(&s.ssh, &format!("{cmd}\n")).await.map_err(remote_err)?;
    let replaced: serde_json::Value = serde_json::from_str(&out)
        .map_err(|_| crate::messages::t("ops.replace_unsupported", &[("out", out.trim())]))?;
    let to = replaced["version"].as_str().unwrap_or("").to_string();
    say(crate::messages::t("ops.replaced", &[("to", &to)]));
    fleet.poke();

    // 检查一：/api/me 返回的是新版本
    let mut serving = false;
    for _ in 0..30 {
        tokio::time::sleep(std::time::Duration::from_secs(1)).await;
        if let Ok(me) = http.get_json(&servers, &name, "/api/me").await {
            if me["version"].as_str() == Some(to.as_str()) {
                serving = true;
                break;
            }
        }
    }
    let mut problem = (!serving).then(|| crate::messages::t("ops.not_serving", &[("to", &to)]));

    // 检查二：升级前在跑的任务没有被重启打断（新进程接管成功，或者期间自己正常结束）
    if serving {
        let mut lost = vec![];
        for (id, pid) in &running {
            let j = http.get_json(&servers, &name, &format!("/api/jobs/{id}")).await?;
            if interrupted(*pid, restarted_at, &j) {
                lost.push(format!("{name}-{id}"));
            }
        }
        if !lost.is_empty() {
            problem = Some(crate::messages::t(
                "ops.jobs_lost",
                &[("jobs", &lost.join(&crate::messages::t("list_sep", &[])))],
            ));
        } else {
            say(crate::messages::t("ops.jobs_reattached", &[("n", &running.len().to_string())]));
        }
    }
    let journal = match problem {
        Some(_) => ssh::run_danlu(&s.ssh, "logs -n 60").await.ok(),
        None => None,
    };
    fleet.poke();
    Ok(UpgradeResult { ok: problem.is_none(), from, to, problem, journal })
}

#[derive(Serialize)]
pub struct ActionResult {
    /// 实际执行的命令，显示给用户看
    command: String,
    output: String,
}

/// 服务管理：重启、看日志、换安全入口、改端口、重新生成证书、改密码。
/// 改端口和重新生成证书之后，地址或 CA 变了，重新读一遍 info --json 更新本地记录。
#[tauri::command]
pub async fn service_action(
    servers: State<'_, Servers>,
    http: State<'_, Http>,
    fleet: State<'_, Fleet>,
    name: String,
    action: String,
    value: Option<String>,
) -> Result<ActionResult, String> {
    let s = servers.get(&name).ok_or_else(|| crate::messages::t("http.no_server", &[("name", &name)]))?;
    let t = s.ssh.as_str();
    let mut log = vec![];
    let mut run = async |args: &str, input: Option<&str>| -> Result<(), String> {
        log.push(format!("$ danlu {args}"));
        let out = ssh::run_danlu_input(t, args, input).await?;
        log.push(out.trim_end().to_string());
        Ok(())
    };
    let reread = match action.as_str() {
        "restart" => {
            run("restart", None).await?;
            false
        }
        "logs" => {
            run("logs -n 200", None).await?;
            false
        }
        "entrance" => {
            run("entrance --reset", None).await?;
            run("restart", None).await?;
            false
        }
        "port" => {
            let port: u16 = value
                .as_deref()
                .unwrap_or("")
                .trim()
                .parse()
                .map_err(|_| crate::messages::t("ops.port_invalid", &[]))?;
            run(&format!("port {port}"), None).await?;
            run("restart", None).await?;
            true
        }
        "cert" => {
            run("cert --regen", None).await?;
            run("restart", None).await?;
            true
        }
        "password" => {
            let pw = value.unwrap_or_default();
            if pw.chars().count() < 8 || pw.contains('\n') {
                return Err(crate::messages::t("ops.password_invalid", &[]));
            }
            run("passwd --password-stdin", Some(&pw)).await?;
            run("restart", None).await?;
            false
        }
        a => return Err(crate::messages::t("ops.action_unknown", &[("a", a)])),
    };
    if reread {
        servers::refresh_info(&servers, &name).await?;
        http.forget(&name);
        log.push(crate::messages::t("ops.reread_done", &[]));
    }
    fleet.poke();
    let command = log.iter().filter(|l| l.starts_with("$ ")).map(|l| &l[2..]).collect::<Vec<_>>().join(" && ");
    Ok(ActionResult { command, output: log.join("\n") })
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn finished_during_restart_is_not_interrupted() {
        let ok = json!({"status": "succeeded", "ended_at": 200, "exit_code": 0, "fail_kind": ""});
        assert!(!interrupted(42, 100, &ok));
        let failed = json!({"status": "failed", "ended_at": 200, "exit_code": 1, "fail_kind": "exception"});
        assert!(!interrupted(42, 100, &failed));
        let cancelled = json!({"status": "cancelled", "ended_at": 200, "fail_kind": ""});
        assert!(!interrupted(42, 100, &cancelled));
    }

    #[test]
    fn vanished_without_exit_code_is_interrupted() {
        let killed = json!({"status": "failed", "ended_at": 200, "exit_code": null, "fail_kind": "killed"});
        assert!(interrupted(42, 100, &killed));
        // 重启之前就没了的不算这次升级
        assert!(!interrupted(42, 300, &killed));
    }

    #[test]
    fn running_must_be_the_same_process() {
        assert!(!interrupted(42, 100, &json!({"status": "running", "pid": 42})));
        assert!(interrupted(42, 100, &json!({"status": "running", "pid": 77})));
        assert!(interrupted(42, 100, &json!({"status": "queued", "pid": 0})));
        // 升级时还在准备的任务被放回队列或重新启动都正常
        assert!(!interrupted(0, 100, &json!({"status": "queued", "pid": 0})));
        assert!(!interrupted(0, 100, &json!({"status": "running", "pid": 77})));
    }
}
