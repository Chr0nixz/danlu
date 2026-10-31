//! 服务器列表（存在应用配置目录的 servers.json）和设备令牌（存在系统凭据库）。

use std::path::PathBuf;
use std::sync::Mutex;

use serde::{Deserialize, Serialize};
use tauri::State;

use crate::fleet::Fleet;
use crate::http::{self, Http};
use crate::ssh;
use crate::state::LocalState;

const KEYRING_SERVICE: &str = "danlu";

#[derive(Clone, Serialize, Deserialize)]
pub struct Server {
    /// 客户端里的名字，默认是 SSH 主机别名；任务编号显示为 <name>-<id>
    pub name: String,
    /// ssh 的目标：~/.ssh/config 里的别名或 user@host
    pub ssh: String,
    /// 不带安全入口的地址，例如 https://203.0.113.10:28457
    pub base_url: String,
    /// local-ca | file | off，和服务器配置里的 [tls] mode 一致
    pub tls_mode: String,
    /// local-ca 时这台服务器的 CA 证书，只信任它
    #[serde(default)]
    pub ca_pem: String,
    #[serde(default)]
    pub ca_fingerprint: String,
    /// 服务器上这个令牌的编号，删除服务器时用它吊销
    pub token_id: i64,
    /// 添加时服务器上 danlu 的路径，以后升级用
    #[serde(default)]
    pub exe: String,
}

/// 给界面看的部分，不含证书
#[derive(Serialize)]
pub struct ServerView {
    name: String,
    ssh: String,
    base_url: String,
    tls_mode: String,
    ca_fingerprint: String,
    /// 这台电脑在服务器上的令牌编号，设置页据此标出"这台电脑"
    token_id: i64,
}

impl From<&Server> for ServerView {
    fn from(s: &Server) -> Self {
        ServerView {
            name: s.name.clone(),
            ssh: s.ssh.clone(),
            base_url: s.base_url.clone(),
            tls_mode: s.tls_mode.clone(),
            ca_fingerprint: s.ca_fingerprint.clone(),
            token_id: s.token_id,
        }
    }
}

pub struct Servers {
    path: PathBuf,
    list: Mutex<Vec<Server>>,
}

impl Servers {
    pub fn load(path: PathBuf) -> Result<Self, String> {
        let list = match std::fs::read_to_string(&path) {
            Ok(text) => serde_json::from_str(&text).map_err(|e| format!("{} 格式错误：{e}", path.display()))?,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => vec![],
            Err(e) => return Err(format!("读取 {} 失败：{e}", path.display())),
        };
        Ok(Servers { path, list: Mutex::new(list) })
    }

    pub fn names(&self) -> Vec<String> {
        self.list.lock().unwrap().iter().map(|s| s.name.clone()).collect()
    }

    pub fn get(&self, name: &str) -> Option<Server> {
        self.list.lock().unwrap().iter().find(|s| s.name == name).cloned()
    }

    pub(crate) fn update(&self, f: impl FnOnce(&mut Vec<Server>)) -> Result<(), String> {
        let mut list = self.list.lock().unwrap();
        let mut next = list.clone();
        f(&mut next);
        if let Some(dir) = self.path.parent() {
            std::fs::create_dir_all(dir).map_err(|e| e.to_string())?;
        }
        // 先写临时文件再改名，写到一半断电也不会丢掉整个列表
        let tmp = self.path.with_extension("json.tmp");
        std::fs::write(&tmp, serde_json::to_string_pretty(&next).unwrap()).map_err(|e| e.to_string())?;
        std::fs::rename(&tmp, &self.path).map_err(|e| e.to_string())?;
        *list = next;
        Ok(())
    }
}

fn keyring_entry(name: &str) -> Result<keyring::Entry, String> {
    keyring::Entry::new(KEYRING_SERVICE, name).map_err(|e| format!("打开系统凭据库失败：{e}"))
}

pub fn load_token(name: &str) -> Result<String, String> {
    keyring_entry(name)?
        .get_password()
        .map_err(|e| format!("系统凭据库里没有 {name} 的令牌（{e}），请删除这台服务器后重新添加"))
}

/// 服务器名会出现在任务编号和凭据库里，只允许简单字符
fn check_name(name: &str) -> Result<(), String> {
    let ok = !name.is_empty()
        && name.len() <= 32
        && name.chars().all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_' || c == '.');
    if ok {
        Ok(())
    } else {
        Err("名字只能用字母、数字和 - _ .，最长 32 个字符".into())
    }
}

/// 这台电脑的名字，写进令牌名里，在面板上吊销时认得出是哪台
fn device_name() -> String {
    std::env::var("COMPUTERNAME").or_else(|_| std::env::var("HOSTNAME")).unwrap_or_else(|_| "desktop".into())
}

#[derive(Deserialize)]
struct Info {
    base_url: String,
    tls_mode: String,
    #[serde(default)]
    ca_pem: String,
    #[serde(default)]
    ca_fingerprint: String,
    #[serde(default)]
    exe: String,
}

#[derive(Deserialize)]
struct CreatedToken {
    id: i64,
    token: String,
}

fn check_info(info: &Info) -> Result<(), String> {
    match info.tls_mode.as_str() {
        "local-ca" => http::check_fingerprint(&info.ca_pem, &info.ca_fingerprint)?,
        "file" | "off" => {}
        m => return Err(format!("不认识的 HTTPS 模式 {m}")),
    }
    let want_scheme = if info.tls_mode == "off" { "http://" } else { "https://" };
    if !info.base_url.starts_with(want_scheme) {
        return Err(format!("服务器给出的地址 {} 和 HTTPS 模式 {} 对不上", info.base_url, info.tls_mode));
    }
    Ok(())
}

async fn read_info(target: &str) -> Result<Info, String> {
    let out = ssh::run_danlu(target, "info --json").await?;
    let info: Info = serde_json::from_str(&out)
        .map_err(|_| format!("{target} 上的 danlu 版本太旧，不支持 info --json，请先升级"))?;
    check_info(&info)?;
    Ok(info)
}

/// 改端口、重新生成证书之后，重新读取地址和 CA
pub async fn refresh_info(servers: &Servers, name: &str) -> Result<(), String> {
    let s = servers.get(name).ok_or_else(|| format!("没有 {name} 这台服务器"))?;
    let info = read_info(&s.ssh).await?;
    servers.update(|list| {
        if let Some(x) = list.iter_mut().find(|x| x.name == name) {
            x.base_url = info.base_url;
            x.tls_mode = info.tls_mode;
            x.ca_pem = info.ca_pem;
            x.ca_fingerprint = info.ca_fingerprint;
            if !info.exe.is_empty() {
                x.exe = info.exe;
            }
        }
    })
}

#[tauri::command]
pub fn list_servers(servers: State<'_, Servers>) -> Vec<ServerView> {
    servers.list.lock().unwrap().iter().map(ServerView::from).collect()
}

/// 通过 SSH 添加一台已经装好 danlu 的服务器：读取地址和 CA → 领一个设备令牌 → 用令牌连一次 → 保存。
#[tauri::command]
pub async fn add_server(
    servers: State<'_, Servers>,
    fleet: State<'_, Fleet>,
    ssh_target: String,
    name: String,
) -> Result<ServerView, String> {
    let (target, name) = (ssh_target.trim().to_string(), name.trim().to_string());
    ssh::check_target(&target)?;
    check_name(&name)?;
    if servers.get(&name).is_some() {
        return Err(format!("已经有一台叫 {name} 的服务器了"));
    }

    let info = read_info(&target).await?;

    let out = ssh::run_danlu(&target, &format!("token create --name {} --json", ssh::quote(&device_name()))).await?;
    let created: CreatedToken = serde_json::from_str(&out).map_err(|e| format!("解析 token create 的输出失败：{e}"))?;
    let server = Server {
        name: name.clone(),
        ssh: target.clone(),
        base_url: info.base_url,
        tls_mode: info.tls_mode,
        ca_pem: info.ca_pem,
        ca_fingerprint: info.ca_fingerprint,
        token_id: created.id,
        exe: info.exe,
    };

    // 先用令牌连一次再保存。SSH 通了但 HTTPS 不通，多半是端口没放行，或者公网 IP 配错了
    if let Err(e) = http::probe(&server, &created.token).await {
        let _ = ssh::run_danlu(&target, &format!("token revoke {}", created.id)).await;
        return Err(format!(
            "SSH 正常，但连不上 {}：{e}。请检查防火墙或云安全组是否放行了这个端口，以及服务是否在运行（systemctl --user status danlu）",
            server.base_url
        ));
    }
    keyring_entry(&name)?.set_password(&created.token).map_err(|e| format!("令牌存入系统凭据库失败：{e}"))?;
    let view = ServerView::from(&server);
    servers.update(|list| list.push(server))?;
    fleet.poke();
    Ok(view)
}

/// 删除服务器。revoke 为真时顺便通过 SSH 吊销服务器上的令牌；吊销失败不影响删除，返回提示文字。
#[tauri::command]
pub async fn remove_server(
    servers: State<'_, Servers>,
    http: State<'_, Http>,
    local: State<'_, LocalState>,
    fleet: State<'_, Fleet>,
    name: String,
    revoke: bool,
) -> Result<Option<String>, String> {
    let server = servers.get(&name).ok_or_else(|| format!("没有 {name} 这台服务器"))?;
    let mut warning = None;
    if revoke {
        if let Err(e) = ssh::run_danlu(&server.ssh, &format!("token revoke {}", server.token_id)).await {
            warning =
                Some(format!("服务器上的令牌没有吊销成功：{e}。可以以后在面板「设置」页手动吊销 #{}", server.token_id));
        }
    }
    servers.update(|list| list.retain(|s| s.name != name))?;
    http.forget(&name);
    local.forget_server(&name);
    if let Ok(entry) = keyring_entry(&name) {
        let _ = entry.delete_credential();
    }
    fleet.poke();
    Ok(warning)
}
