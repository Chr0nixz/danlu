//! 调用系统自带的 OpenSSH（Windows 上是 ssh.exe），这样 ~/.ssh/config、密钥、ssh-agent、跳板机都直接可用。
//! 每个运维动作都是在服务器上执行一条 danlu 子命令。

use std::path::PathBuf;
use std::process::Stdio;
use std::time::Duration;

use tauri::Manager;
use tokio::io::AsyncWriteExt;
use tokio::process::Command;

fn ssh_program() -> PathBuf {
    openssh_tool("ssh")
}

fn scp_program() -> PathBuf {
    openssh_tool("scp")
}

fn openssh_tool(name: &str) -> PathBuf {
    #[cfg(windows)]
    {
        // 优先用系统的 OpenSSH：它能用 Windows 的 ssh-agent 服务。PATH 里可能先排着 Git 自带的 ssh
        let root = std::env::var_os("SystemRoot").unwrap_or_else(|| "C:\\Windows".into());
        let p = PathBuf::from(root).join("System32\\OpenSSH").join(format!("{name}.exe"));
        if p.exists() {
            return p;
        }
    }
    PathBuf::from(name)
}

/// 给 POSIX sh 用的单引号转义
pub fn quote(s: &str) -> String {
    format!("'{}'", s.replace('\'', "'\\''"))
}

/// SSH 目标只能是主机别名或 user@host，不能以 - 开头（否则会被当成 ssh 的参数）
pub fn check_target(target: &str) -> Result<(), String> {
    if target.is_empty() || target.starts_with('-') || target.chars().any(|c| c.is_whitespace() || c.is_control()) {
        return Err(format!("SSH 目标 {target:?} 无效，应该是 ~/.ssh/config 里的主机名或 user@host"));
    }
    Ok(())
}

/// 在服务器上执行 `danlu <args>`，返回标准输出。
pub async fn run_danlu(target: &str, args: &str) -> Result<String, String> {
    run_danlu_input(target, args, None).await
}

/// 同 run_danlu，input 不为空时作为 danlu 的标准输入（例如新密码），不会出现在命令行里。
///
/// 脚本从标准输入传给远端的 `sh -s`：远端登录 shell 只需要解析 `sh -s`，所以 bash、zsh、fish 都行。
/// 非交互的 SSH 会话通常不读 ~/.profile，所以先把 README 里建议的安装位置加进 PATH。
pub async fn run_danlu_input(target: &str, args: &str, input: Option<&str>) -> Result<String, String> {
    let mut script = String::from(
        "PATH=\"$HOME/bin:$HOME/.local/bin:$PATH\"; export PATH\n\
         command -v danlu >/dev/null 2>&1 || exit 127\n",
    );
    match input {
        // 带引号的 heredoc 里什么都不展开；结束标记带随机数，内容里不会恰好有这一行
        Some(text) => {
            let tag = format!("DANLU_INPUT_{}", nonce());
            script.push_str(&format!("exec danlu {args} <<'{tag}'\n{text}\n{tag}\n"));
        }
        None => script.push_str(&format!("exec danlu {args}\n")),
    }
    run_script(target, &script).await.map_err(|e| match e {
        RemoteError::Exit(127, _) => {
            format!("{target} 上没有找到 danlu（PATH、~/bin、~/.local/bin 里都没有）。请先按 README 在服务器上安装")
        }
        RemoteError::Exit(code, stderr) => {
            if stderr.is_empty() {
                format!("danlu {args} 执行失败（退出码 {code}）")
            } else {
                stderr
            }
        }
        RemoteError::Other(msg) => msg,
    })
}

pub enum RemoteError {
    /// 远端脚本以非 0 退出：退出码和标准错误
    Exit(i32, String),
    /// ssh 本身失败、超时等
    Other(String),
}

fn nonce() -> u64 {
    std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_nanos() as u64).unwrap_or(0)
}

/// 在服务器上用 `sh -s` 执行一段 POSIX 脚本，返回标准输出
pub async fn run_script(target: &str, script: &str) -> Result<String, RemoteError> {
    check_target(target).map_err(RemoteError::Other)?;
    let mut cmd = Command::new(ssh_program());
    // BatchMode：需要输密码或口令时直接失败，不会卡住等输入。
    // 不自动接受陌生的主机密钥：CA 证书和令牌都经这条连接传过来，第一次连接要用户自己确认指纹
    cmd.args(["-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-T", target, "sh", "-s"]);
    let out = run(cmd, Some(script), Duration::from_secs(60), &format!("ssh {target}")).await?;
    Ok(out)
}

/// 用 scp 把本地文件传到服务器上的 remote 路径
pub async fn upload(target: &str, local: &std::path::Path, remote: &str) -> Result<(), String> {
    check_target(target)?;
    if remote.chars().any(|c| c.is_whitespace() || "'\"\\$`;&|".contains(c)) {
        return Err(format!("服务器上的路径 {remote} 含有特殊字符，请手动升级"));
    }
    let mut cmd = Command::new(scp_program());
    cmd.args(["-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-q"]).arg(local).arg(format!("{target}:{remote}"));
    run(cmd, None, Duration::from_secs(600), &format!("scp 到 {target}")).await.map(|_| ()).map_err(|e| match e {
        RemoteError::Exit(255, stderr) | RemoteError::Exit(1, stderr) => explain_ssh_error(target, &stderr),
        RemoteError::Exit(code, stderr) => format!("scp 失败（退出码 {code}）：{stderr}"),
        RemoteError::Other(msg) => msg,
    })
}

async fn run(mut cmd: Command, stdin: Option<&str>, limit: Duration, what: &str) -> Result<String, RemoteError> {
    cmd.stdin(if stdin.is_some() { Stdio::piped() } else { Stdio::null() })
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    #[cfg(windows)]
    cmd.creation_flags(0x0800_0000); // CREATE_NO_WINDOW：不闪出控制台窗口

    let mut child = cmd.spawn().map_err(|e| RemoteError::Other(format!("无法启动 {what}：{e}")))?;
    if let (Some(text), Some(mut pipe)) = (stdin, child.stdin.take()) {
        pipe.write_all(text.as_bytes()).await.map_err(|e| RemoteError::Other(e.to_string()))?;
    }
    let out = tokio::time::timeout(limit, child.wait_with_output())
        .await
        .map_err(|_| RemoteError::Other(format!("{what} 超过 {} 秒没有完成", limit.as_secs())))?
        .map_err(|e| RemoteError::Other(e.to_string()))?;
    let stderr = String::from_utf8_lossy(&out.stderr).trim().to_string();
    match out.status.code() {
        Some(0) => Ok(String::from_utf8_lossy(&out.stdout).into_owned()),
        Some(255) if what.starts_with("ssh ") => Err(RemoteError::Other(explain_ssh_error(&what[4..], &stderr))),
        Some(code) => Err(RemoteError::Exit(code, stderr)),
        None => Err(RemoteError::Other(format!("{what} 被中止"))),
    }
}

fn explain_ssh_error(target: &str, stderr: &str) -> String {
    let hint = if stderr.contains("Host key verification failed") || stderr.contains("No ED25519 host key") {
        format!("还没有确认过这台服务器的主机密钥：先在终端里执行一次 ssh {target}，核对指纹后再来添加。")
    } else if stderr.contains("REMOTE HOST IDENTIFICATION HAS CHANGED") {
        "服务器的主机密钥变了，可能是重装过系统，也可能有人在冒充它，请先在终端里确认。".to_string()
    } else if stderr.contains("Permission denied") {
        "客户端只能用密钥登录：请确认密钥已经加入 ssh-agent，或者在 ~/.ssh/config 里配置了 IdentityFile。".to_string()
    } else {
        return format!("ssh {target} 失败：{stderr}");
    };
    // ssh 的原始输出放在后面，界面按行显示
    format!("{hint}\nssh {target}：{stderr}")
}

/// ~/.ssh/config 里的主机别名（跳过带通配符的 Host 和 Include 进来的文件）
#[tauri::command]
pub fn ssh_hosts(app: tauri::AppHandle) -> Vec<String> {
    let Ok(home) = app.path().home_dir() else { return vec![] };
    let Ok(text) = std::fs::read_to_string(home.join(".ssh").join("config")) else { return vec![] };
    let mut hosts = vec![];
    for line in text.lines() {
        let line = line.trim();
        let Some((key, rest)) = line.split_once(|c: char| c.is_whitespace() || c == '=') else { continue };
        if !key.eq_ignore_ascii_case("host") {
            continue;
        }
        for h in rest.split_whitespace() {
            if !h.contains(['*', '?', '!']) && !hosts.iter().any(|x| x == h) {
                hosts.push(h.to_string());
            }
        }
    }
    hosts
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn quote_escapes_single_quotes() {
        assert_eq!(quote("it's"), "'it'\\''s'");
    }

    #[test]
    fn target_rejects_options() {
        assert!(check_target("-oProxyCommand=x").is_err());
        assert!(check_target("a b").is_err());
        assert!(check_target("me@gpu1").is_ok());
    }
}
