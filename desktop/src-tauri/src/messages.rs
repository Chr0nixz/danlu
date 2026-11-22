//! 双语文案目录：通知和命令错误在构造处按当前语言合成。
//! zh-CN 是源语言（与历史文字一致），en 镜像，键集对齐由单测保证。模板用 {name} 占位。
//! 语言由 set_prefs / 启动时经 set_lang 更新（和前端 i18n 运行时同一套模块级状态思路）。

use std::sync::Mutex;

static LANG: Mutex<Option<String>> = Mutex::new(None);

/// 设置当前语言（"zh-CN" 或 "en"）；非法值按中文。
pub fn set_lang(lang: &str) {
    *LANG.lock().unwrap() = Some(if lang == "en" { "en".into() } else { "zh-CN".into() });
}

pub fn lang() -> String {
    LANG.lock().unwrap().clone().unwrap_or_else(|| "zh-CN".into())
}

/// 取当前语言的文案并插值；缺键回退中文，再缺返回键名（单测会抓）。
pub fn t(key: &str, params: &[(&str, &str)]) -> String {
    let idx = if lang() == "en" { 2 } else { 1 };
    let tpl = MESSAGES.iter().find(|(k, _, _)| *k == key).map(|m| if idx == 2 { m.2 } else { m.1 }).unwrap_or(key);
    let mut s = tpl.to_string();
    for (k, v) in params {
        s = s.replace(&format!("{{{k}}}"), v);
    }
    s
}

const MESSAGES: &[(&str, &str, &str)] = &[
    ("offline.title", "{name} 连不上", "{name} is unreachable"),
    ("offline.alert", "{name} 连不上：{error}", "{name} is unreachable: {error}"),
    ("recovered.title", "{name} 已恢复", "{name} recovered"),
    ("recovered.body", "又能连上了", "Reachable again"),
    ("gpu_error.title", "{name} 的 nvidia-smi 报错", "nvidia-smi error on {name}"),
    ("gpu_error.alert", "{name} 的 nvidia-smi 报错：{error}", "nvidia-smi error on {name}: {error}"),
    ("external.job_title", "别的任务跑到了你的卡上", "Another job ran onto your card"),
    ("external.job_text", "{name} 的 GPU {gpu} 上有 {who} 的进程，占了 {mem} MB，可能和 {job} 抢显存", "A process of {who} is on GPU {gpu} of {name}, using {mem} MB — it may compete with {job} for memory"),
    ("external.title", "外部进程占用了你的卡", "An external process is on your card"),
    ("external.text", "{name} 的 GPU {gpu} 上有外部进程 {who}，占了 {mem} MB，可能和 {job} 抢显存", "External process {who} is on GPU {gpu} of {name}, using {mem} MB — it may compete with {job} for memory"),
    ("external.who_user", "{user}（pid {pid}）", "{user} (pid {pid})"),
    ("blocked.unknown_text", "{key} {name} 在排队，被未知显存占用的卡挡住了", "{key} {name} is queued, blocked by cards held by unexplained memory"),
    ("blocked.text", "{key} {name} 在排队，被外部进程占着的卡挡住了", "{key} {name} is queued, blocked by cards held by external processes"),
    ("blocked.unknown_title", "排队的任务被未知显存占用挡住", "Queued job blocked by unexplained GPU memory"),
    ("blocked.title", "排队的任务被外部进程挡住", "Queued job blocked by external processes"),
    ("failed.many_title", "{name} 上有 {n} 个任务失败", "{n} jobs failed on {name}"),
    ("failed.title", "{key} 失败 · {name}", "{key} failed · {name}"),
    ("list_sep", "、", ", "),
    ("fail.cuda_oom", "CUDA 显存不足", "CUDA out of memory"),
    ("fail.cuda_oom_ext", "CUDA 显存不足，运行期间卡上出现过外部进程", "CUDA out of memory; an external process appeared on the card while running"),
    ("fail.host_oom", "主机内存不足，被系统杀掉", "Host out of memory; killed by the system"),
    ("fail.host_oom_worker", "运行期间本机发生过 OOM kill", "An OOM kill occurred on this host while the job was running"),
    ("fail.host_oom_guess", "退出码 137，可能是主机内存不足", "Exit code 137; possibly host out of memory"),
    ("fail.nccl", "NCCL 通信超时或出错", "NCCL communication timeout or error"),
    ("fail.exception", "Python 异常", "Python exception"),
    ("fail.reboot", "服务器重启，任务中断", "Server rebooted; the job was interrupted"),
    ("fail.killed", "进程被外部终止（没有留下退出码）", "Process terminated externally (no exit code left)"),
    ("fail.signal", "被信号终止（{sig}）", "Terminated by signal ({sig})"),
    ("fail.signal_generic", "进程被信号终止", "Terminated by signal"),
    ("fail.signal_num", "信号 {n}", "signal {n}"),
    ("fail.start", "任务启动失败", "Job failed to start"),
    ("fail.start_detail", "启动失败: {detail}", "Failed to start: {detail}"),
    ("fail.exit_code", "退出码 {code}", "exit code {code}"),
    ("fail.failed", "失败", "failed"),
    ("tray.open", "打开主窗口", "Open main window"),
    ("tray.quit", "退出丹炉", "Quit Danlu"),
    ("tray.idle", "丹炉", "Danlu"),
    ("tray.running", "丹炉 · 有任务在跑", "Danlu · jobs running"),
    ("tray.alert", "丹炉 · 有没看过的失败或异常", "Danlu · unread failures or alerts"),
    ("http.no_server", "没有 {name} 这台服务器", "No server named {name}"),
    ("http.token_revoked", "服务器不认这台电脑的令牌了（可能已被吊销），请删除后重新添加", "The server no longer accepts this computer's token (it may have been revoked). Remove the server and add it again"),
    ("http.ca_format", "CA 证书格式错误：{e}", "Invalid CA certificate: {e}"),
    ("http.ca_fingerprint", "CA 证书和它的指纹对不上，拒绝连接", "The CA certificate does not match its fingerprint; refusing to connect"),
    ("http.client_build", "创建 HTTPS 客户端失败：{e}", "Failed to build the HTTPS client: {e}"),
    ("http.timeout", "超时", "Timed out"),
    ("http.connect_failed", "连接失败", "Connection failed"),
    ("http.http_status", "服务器返回 HTTP {status}", "Server returned HTTP {status}"),
    ("http.path_denied", "不允许的路径 {path}", "Path not allowed: {path}"),
    ("http.method_unsupported", "不支持的方法 {m}", "Unsupported method {m}"),
    ("http.filename_invalid", "文件名 {filename} 不合法", "Invalid file name: {filename}"),
    ("http.download_failed", "下载失败：HTTP {status}", "Download failed: HTTP {status}"),
    ("http.save_failed", "保存到 {path} 失败：{e}", "Failed to save to {path}: {e}"),
    ("ops.arch_unsupported", "不支持的 CPU 架构 {other}", "Unsupported CPU architecture {other}"),
    ("ops.binary_missing", "找不到 danlu-linux-{arch}：先运行 scripts/build.ps1 -Arch {arch} 构建", "danlu-linux-{arch} not found: build it first with scripts/build.ps1 -Arch {arch}"),
    ("ops.using_local", "{arch}，使用 {path}", "{arch}, using {path}"),
    ("ops.current_version", "当前版本 {from}，在跑的任务 {n} 个", "Current version {from}; running jobs: {n}"),
    ("ops.exe_path_unknown", "找不到服务器上 danlu 的绝对路径（得到的是 {exe}）", "Could not resolve the absolute path of danlu on the server (got {exe})"),
    ("ops.replace_unsupported", "新程序不支持 replace，输出：{out}", "The new binary does not support replace; output: {out}"),
    ("ops.replaced", "已替换为 {to}，等待服务重新启动…", "Replaced with {to}; waiting for the service to restart…"),
    ("ops.not_serving", "30 秒内 /api/me 仍然不是 {to}", "/api/me still does not report {to} after 30 seconds"),
    ("ops.jobs_lost", "这些任务在重启后没有被接管：{jobs}", "These jobs were not re-attached after the restart: {jobs}"),
    ("ops.jobs_reattached", "新版本已在服务，{n} 个在跑的任务都已接管", "The new version is serving; running jobs re-attached: {n}"),
    ("ops.port_invalid", "端口应该是 1–65535 的数字", "Port must be a number between 1 and 65535"),
    ("ops.password_invalid", "新密码至少 8 位，不能换行", "New password must be at least 8 characters, without newlines"),
    ("ops.action_unknown", "不认识的操作 {a}", "Unknown action {a}"),
    ("ops.reread_done", "已重新读取地址和证书", "Re-read the address and certificate"),
    ("servers.file_format", "{path} 格式错误：{e}", "Invalid format in {path}: {e}"),
    ("servers.file_read", "读取 {path} 失败：{e}", "Failed to read {path}: {e}"),
    ("servers.keyring_open", "打开系统凭据库失败：{e}", "Failed to open the system credential store: {e}"),
    ("servers.keyring_missing", "系统凭据库里没有 {name} 的令牌（{e}），请删除这台服务器后重新添加", "No token for {name} in the system credential store ({e}). Remove this server and add it again"),
    ("servers.name_invalid", "名字只能用字母、数字和 - _ .，最长 32 个字符", "Names may only contain letters, digits, - _ . (up to 32 characters)"),
    ("servers.tls_mode_unknown", "不认识的 HTTPS 模式 {m}", "Unknown HTTPS mode {m}"),
    ("servers.address_mismatch", "服务器给出的地址 {url} 和 HTTPS 模式 {mode} 对不上", "The server's address {url} does not match HTTPS mode {mode}"),
    ("servers.too_old", "{target} 上的 danlu 版本太旧，不支持 info --json，请先升级", "danlu on {target} is too old for info --json; upgrade it first"),
    ("servers.duplicate", "已经有一台叫 {name} 的服务器了", "A server named {name} already exists"),
    ("servers.token_parse", "解析 token create 的输出失败：{e}", "Failed to parse token create output: {e}"),
    ("servers.ssh_ok_http_fail", "SSH 正常，但连不上 {url}：{e}。请检查防火墙或云安全组是否放行了这个端口，以及服务是否在运行（systemctl --user status danlu）", "SSH works but {url} is unreachable: {e}. Check that the firewall/security group allows this port and that the service is running (systemctl --user status danlu)"),
    ("servers.keyring_save", "令牌存入系统凭据库失败：{e}", "Failed to store the token in the system credential store: {e}"),
    ("servers.revoke_failed", "服务器上的令牌没有吊销成功：{e}。可以以后在面板「设置」页手动吊销 #{id}", "The token on the server was not revoked: {e}. You can revoke #{id} later in the panel's Settings page"),
    ("ssh.target_invalid", "SSH 目标 {target} 无效，应该是 ~/.ssh/config 里的主机名或 user@host", "Invalid SSH target {target}: use a host name from ~/.ssh/config or user@host"),
    ("ssh.danlu_missing", "{target} 上没有找到 danlu（PATH、~/bin、~/.local/bin 里都没有）。请先按 README 在服务器上安装", "danlu not found on {target} (not in PATH, ~/bin or ~/.local/bin). Install it on the server per the README first"),
    ("ssh.cmd_failed", "danlu {args} 执行失败（退出码 {code}）", "danlu {args} failed (exit code {code})"),
    ("ssh.path_special", "服务器上的路径 {remote} 含有特殊字符，请手动升级", "The remote path {remote} contains special characters; upgrade manually"),
    ("ssh.scp_what", "scp 到 {target}", "scp to {target}"),
    ("ssh.scp_failed", "scp 失败（退出码 {code}）：{stderr}", "scp failed (exit code {code}): {stderr}"),
    ("ssh.spawn_failed", "无法启动 {what}：{e}", "Failed to start {what}: {e}"),
    ("ssh.timeout", "{what} 超过 {secs} 秒没有完成", "{what} did not finish within {secs} seconds"),
    ("ssh.aborted", "{what} 被中止", "{what} was aborted"),
    ("ssh.host_key_new", "还没有确认过这台服务器的主机密钥：先在终端里执行一次 ssh {target}，核对指纹后再来添加。", "This server's host key has not been verified yet: run ssh {target} in a terminal once, check the fingerprint, then add it again"),
    ("ssh.host_key_changed", "服务器的主机密钥变了，可能是重装过系统，也可能有人在冒充它，请先在终端里确认。", "The server host key changed — it may have been reinstalled, or someone may be impersonating it. Verify it in a terminal first"),
    ("ssh.key_only", "客户端只能用密钥登录：请确认密钥已经加入 ssh-agent，或者在 ~/.ssh/config 里配置了 IdentityFile。", "Only key-based login is allowed: make sure the key is in ssh-agent or IdentityFile is set in ~/.ssh/config"),
    ("ssh.failed", "ssh {target} 失败：{stderr}", "ssh {target} failed: {stderr}"),
    ("shortcut.failed", "快捷键 {shortcut} 用不了：{e}", "Shortcut {shortcut} is unavailable: {e}"),
    ("autostart.failed", "设置开机自启失败：{e}", "Failed to configure autostart: {e}"),
    ("open_url.denied", "不打开 {url}", "Refusing to open {url}")
];

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn catalog_parity_and_placeholders() {
        for (k, zh, en) in MESSAGES {
            assert!(!zh.is_empty() && !en.is_empty(), "键 {k} 文案为空");
            let ph = |s: &str| {
                let mut v: Vec<String> = Vec::new();
                let mut rest = s;
                while let Some(i) = rest.find('{') {
                    if let Some(j) = rest[i..].find('}') {
                        v.push(rest[i..i + j + 1].to_string());
                        rest = &rest[i + j + 1..];
                    } else {
                        break;
                    }
                }
                v.sort();
                v
            };
            assert_eq!(ph(zh), ph(en), "键 {k} 占位符不一致");
        }
        let mut ks: Vec<&str> = MESSAGES.iter().map(|(k, _, _)| *k).collect();
        ks.sort();
        let n = ks.len();
        ks.dedup();
        assert_eq!(ks.len(), n, "有重复键");
    }

    #[test]
    fn interpolation_and_fallback() {
        set_lang("zh-CN");
        assert_eq!(t("offline.title", &[("name", "gpu1")]), "gpu1 连不上");
        set_lang("en");
        assert_eq!(t("offline.title", &[("name", "gpu1")]), "gpu1 is unreachable");
        assert_eq!(t("no.such.key", &[]), "no.such.key");
        // 缺参数保留占位符
        assert_eq!(t("offline.title", &[]), "{name} is unreachable");
        set_lang("zh-CN");
    }
}
