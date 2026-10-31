//! 到各台服务器的 HTTPS。local-ca 模式下只信任这台服务器自己的 CA，从不关闭证书校验。

use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use futures_util::StreamExt;
use rustls::pki_types::pem::PemObject;
use rustls::pki_types::CertificateDer;
use serde::Serialize;
use sha2::{Digest, Sha256};
use tauri::ipc::Channel;
use tauri::State;
use tokio::sync::oneshot;

use crate::servers::{self, Server, Servers};

#[derive(Clone)]
struct Conn {
    client: reqwest::Client,
    base: String,
    token: String,
}

#[derive(Default)]
pub struct Http {
    conns: Mutex<HashMap<String, Conn>>,
    /// 正在进行的 SSE 流：关闭时给对应的任务发信号
    streams: Arc<Mutex<HashMap<u64, oneshot::Sender<()>>>>,
    next_stream: AtomicU64,
}

impl Http {
    fn conn(&self, servers: &Servers, name: &str) -> Result<Conn, String> {
        if let Some(c) = self.conns.lock().unwrap().get(name) {
            return Ok(c.clone());
        }
        let s = servers.get(name).ok_or_else(|| format!("没有 {name} 这台服务器"))?;
        let c = Conn { client: build_client(&s)?, base: s.base_url.clone(), token: servers::load_token(name)? };
        self.conns.lock().unwrap().insert(name.to_string(), c.clone());
        Ok(c)
    }

    pub fn forget(&self, name: &str) {
        self.conns.lock().unwrap().remove(name);
    }

    /// 发一个 GET 并解析 JSON，给轮询和运维用。HTTP 错误码也当作失败，带上服务器给的原因
    pub async fn get_json(&self, servers: &Servers, name: &str, path: &str) -> Result<serde_json::Value, String> {
        let c = self.conn(servers, name)?;
        let resp = c
            .client
            .get(format!("{}{}", c.base, path))
            .bearer_auth(&c.token)
            .timeout(Duration::from_secs(10))
            .send()
            .await
            .map_err(|e| describe(&e))?;
        let status = resp.status();
        let body = resp.text().await.map_err(|e| describe(&e))?;
        if status.as_u16() == 404 && body.is_empty() {
            return Err("服务器不认这台电脑的令牌了（可能已被吊销），请删除后重新添加".into());
        }
        let v: serde_json::Value = serde_json::from_str(&body).map_err(|_| format!("HTTP {}", status.as_u16()))?;
        if !status.is_success() {
            return Err(v["error"].as_str().map(String::from).unwrap_or_else(|| format!("HTTP {}", status.as_u16())));
        }
        Ok(v)
    }
}

/// CA 证书的 SHA-256 指纹，格式和服务器上 danlu info 显示的一样（大写、冒号分隔）
fn fingerprint(pem: &str) -> Result<String, String> {
    let der = CertificateDer::from_pem_slice(pem.as_bytes()).map_err(|e| format!("CA 证书格式错误：{e}"))?;
    let sum = Sha256::digest(der.as_ref());
    Ok(sum.iter().map(|b| format!("{b:02X}")).collect::<Vec<_>>().join(":"))
}

pub fn check_fingerprint(pem: &str, want: &str) -> Result<(), String> {
    if fingerprint(pem)? != want {
        return Err("CA 证书和它的指纹对不上，拒绝连接".into());
    }
    Ok(())
}

fn build_client(s: &Server) -> Result<reqwest::Client, String> {
    let b = reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(10))
        .user_agent(concat!("danlu-desktop/", env!("CARGO_PKG_VERSION")));
    let b = match s.tls_mode.as_str() {
        "local-ca" => {
            // 每次建连接前都核对一遍：servers.json 被人改过也不会信任别的 CA
            check_fingerprint(&s.ca_pem, &s.ca_fingerprint)?;
            let ca = reqwest::Certificate::from_pem(s.ca_pem.as_bytes()).map_err(|e| e.to_string())?;
            b.tls_certs_only([ca])
        }
        // file：服务器用的是正式证书，按系统的根证书校验；off：只用于本地测试的 HTTP
        _ => b,
    };
    b.build().map_err(|e| format!("创建 HTTPS 客户端失败：{e}"))
}

/// 把 reqwest 的错误说成人话，带上底层原因（证书错误、连接被拒等都在 source 链里）
fn describe(e: &reqwest::Error) -> String {
    let mut msg = if e.is_timeout() {
        "超时".to_string()
    } else if e.is_connect() {
        "连接失败".to_string()
    } else {
        e.to_string()
    };
    let mut src = std::error::Error::source(e);
    while let Some(s) = src {
        msg.push('：');
        msg.push_str(&s.to_string());
        src = s.source();
    }
    msg
}

/// 添加服务器时用新领的令牌试连一次
pub async fn probe(s: &Server, token: &str) -> Result<(), String> {
    let client = build_client(s)?;
    let resp = client
        .get(format!("{}/api/me", s.base_url))
        .bearer_auth(token)
        .timeout(Duration::from_secs(15))
        .send()
        .await
        .map_err(|e| describe(&e))?;
    if !resp.status().is_success() {
        return Err(format!("服务器返回 HTTP {}", resp.status().as_u16()));
    }
    Ok(())
}

fn check_path(path: &str) -> Result<(), String> {
    if !path.starts_with("/api/") || path.contains("..") {
        return Err(format!("不允许的路径 {path}"));
    }
    Ok(())
}

#[derive(Serialize)]
pub struct ApiResponse {
    status: u16,
    body: String,
}

/// 转发一个 API 请求。网络层的错误（连不上、证书不对）作为 Err 返回，HTTP 错误码照常放在 status 里。
#[tauri::command]
pub async fn api_request(
    servers: State<'_, Servers>,
    http: State<'_, Http>,
    server: String,
    method: String,
    path: String,
    body: Option<String>,
) -> Result<ApiResponse, String> {
    check_path(&path)?;
    let c = http.conn(&servers, &server)?;
    let url = format!("{}{}", c.base, path);
    let mut req = match method.as_str() {
        "GET" => c.client.get(url),
        "POST" => c.client.post(url),
        m => return Err(format!("不支持的方法 {m}")),
    };
    req = req.bearer_auth(&c.token).timeout(Duration::from_secs(20));
    if let Some(b) = body {
        req = req.header("Content-Type", "application/json").body(b);
    }
    let resp = req.send().await.map_err(|e| describe(&e))?;
    let status = resp.status().as_u16();
    let body = resp.text().await.map_err(|e| describe(&e))?;
    Ok(ApiResponse { status, body })
}

/// 下载完整日志、代码快照：存进系统的下载目录，返回保存的路径。同名文件已存在时加序号
#[tauri::command]
pub async fn save_file(
    app: tauri::AppHandle,
    servers: State<'_, Servers>,
    http: State<'_, Http>,
    server: String,
    path: String,
    filename: String,
) -> Result<String, String> {
    use tauri::Manager;
    check_path(&path)?;
    if filename.contains(['/', '\\', ':']) || filename.starts_with('.') {
        return Err(format!("文件名 {filename} 不合法"));
    }
    let c = http.conn(&servers, &server)?;
    let resp =
        c.client.get(format!("{}{}", c.base, path)).bearer_auth(&c.token).send().await.map_err(|e| describe(&e))?;
    if !resp.status().is_success() {
        return Err(format!("下载失败：HTTP {}", resp.status().as_u16()));
    }
    let dir = app.path().download_dir().map_err(|e| e.to_string())?;
    let (stem, ext) = filename
        .rsplit_once('.')
        .map(|(a, b)| (a.to_string(), format!(".{b}")))
        .unwrap_or((filename.clone(), String::new()));
    let mut target = dir.join(&filename);
    for i in 1.. {
        if !target.exists() {
            break;
        }
        target = dir.join(format!("{stem} ({i}){ext}"));
    }
    let bytes = resp.bytes().await.map_err(|e| describe(&e))?;
    std::fs::write(&target, &bytes).map_err(|e| format!("保存到 {} 失败：{e}", target.display()))?;
    Ok(target.display().to_string())
}

#[derive(Clone, Serialize)]
#[serde(tag = "kind", rename_all = "lowercase")]
pub enum StreamMsg {
    Event {
        event: String,
        data: String,
    },
    /// 连接断开了：服务器关闭、网络错误或 HTTP 错误码
    Closed {
        error: Option<String>,
    },
}

/// 打开一个 SSE 流，事件通过 channel 推给界面。返回流的编号，用 api_stream_close 关闭。
#[tauri::command]
pub async fn api_stream(
    servers: State<'_, Servers>,
    http: State<'_, Http>,
    server: String,
    path: String,
    on_event: Channel<StreamMsg>,
) -> Result<u64, String> {
    check_path(&path)?;
    let c = http.conn(&servers, &server)?;
    let id = http.next_stream.fetch_add(1, Ordering::Relaxed);
    let (stop_tx, stop_rx) = oneshot::channel();
    http.streams.lock().unwrap().insert(id, stop_tx);
    let streams = http.streams.clone();
    let url = format!("{}{}", c.base, path);
    tauri::async_runtime::spawn(async move {
        let send = |event, data| on_event.send(StreamMsg::Event { event, data }).map_err(|e| e.to_string());
        tokio::select! {
            _ = stop_rx => {}
            r = read_sse(&c, &url, send) => {
                let _ = on_event.send(StreamMsg::Closed { error: r.err() });
            }
        }
        streams.lock().unwrap().remove(&id);
    });
    Ok(id)
}

#[tauri::command]
pub fn api_stream_close(http: State<'_, Http>, id: u64) {
    if let Some(tx) = http.streams.lock().unwrap().remove(&id) {
        let _ = tx.send(());
    }
}

/// 读一个 SSE 流直到服务器关闭，每个事件调用一次 emit；emit 出错（界面已经关掉）时停止。
async fn read_sse(
    c: &Conn,
    url: &str,
    mut emit: impl FnMut(String, String) -> Result<(), String>,
) -> Result<(), String> {
    let resp = c
        .client
        .get(url)
        .bearer_auth(&c.token)
        .header("Accept", "text/event-stream")
        .send()
        .await
        .map_err(|e| describe(&e))?;
    if !resp.status().is_success() {
        return Err(format!("HTTP {}", resp.status().as_u16()));
    }
    let mut body = resp.bytes_stream();
    let mut buf: Vec<u8> = Vec::new();
    while let Some(chunk) = body.next().await {
        buf.extend_from_slice(&chunk.map_err(|e| describe(&e))?);
        // 按空行切出一个个事件；按字节切，避免把一个汉字切成两半
        while let Some(pos) = buf.windows(2).position(|w| w == b"\n\n") {
            let block: Vec<u8> = buf.drain(..pos + 2).collect();
            if let Some((event, data)) = parse_event(&String::from_utf8_lossy(&block[..pos])) {
                emit(event, data)?;
            }
        }
    }
    Ok(())
}

/// 解析一个 SSE 事件块；只有注释（心跳）时返回 None
fn parse_event(block: &str) -> Option<(String, String)> {
    let mut event = "message".to_string();
    let mut data: Vec<&str> = vec![];
    for line in block.split('\n') {
        let line = line.strip_suffix('\r').unwrap_or(line);
        if line.is_empty() || line.starts_with(':') {
            continue;
        }
        let (field, value) = line.split_once(':').unwrap_or((line, ""));
        let value = value.strip_prefix(' ').unwrap_or(value);
        match field {
            "event" => event = value.to_string(),
            "data" => data.push(value),
            _ => {}
        }
    }
    (!data.is_empty()).then(|| (event, data.join("\n")))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_sse_events() {
        assert_eq!(parse_event("data: {\"o\":0}"), Some(("message".into(), "{\"o\":0}".into())));
        assert_eq!(parse_event("event: end\ndata: {}"), Some(("end".into(), "{}".into())));
        assert_eq!(parse_event(": ping"), None);
    }

    #[test]
    fn rejects_paths_outside_api() {
        assert!(check_path("/api/jobs?status=running").is_ok());
        assert!(check_path("/k3x9p2qa7m4z").is_err());
        assert!(check_path("/api/../x").is_err());
    }

    /// 连一台真实的 danlu（local-ca 模式）：固定 CA 能连上，换一张 CA 必须连不上，日志流能读到 end 事件。
    /// 需要先在 WSL 里启动服务器，再设置 DANLU_TEST_INFO（danlu info --json 的输出文件）、
    /// DANLU_TEST_TOKEN、DANLU_TEST_JOB（一个已结束的任务 id），然后：cargo test -- --ignored
    #[tokio::test]
    #[ignore]
    async fn live_server() {
        let _ = rustls::crypto::ring::default_provider().install_default();
        let info: serde_json::Value =
            serde_json::from_str(&std::fs::read_to_string(std::env::var("DANLU_TEST_INFO").unwrap()).unwrap()).unwrap();
        let token = std::env::var("DANLU_TEST_TOKEN").unwrap();
        let s = Server {
            name: "t".into(),
            ssh: "t".into(),
            base_url: info["base_url"].as_str().unwrap().into(),
            tls_mode: info["tls_mode"].as_str().unwrap().into(),
            ca_pem: info["ca_pem"].as_str().unwrap().into(),
            ca_fingerprint: info["ca_fingerprint"].as_str().unwrap().into(),
            token_id: 0,
            exe: String::new(),
        };
        assert_eq!(s.tls_mode, "local-ca");
        probe(&s, &token).await.expect("固定 CA 应该能连上");
        assert!(probe(&s, "dl_wrong").await.is_err(), "错误的令牌应该被拒绝");

        // 另一张 CA（指纹也对得上）：证书校验必须失败
        let other = include_str!("../tests/other-ca.pem");
        let bad = Server { ca_pem: other.into(), ca_fingerprint: fingerprint(other).unwrap(), ..s.clone() };
        let err = probe(&bad, &token).await.expect_err("别的 CA 签的证书不应该被信任");
        assert!(err.contains("certificate") || err.contains("证书"), "{err}");

        let c = Conn { client: build_client(&s).unwrap(), base: s.base_url.clone(), token };
        let job = std::env::var("DANLU_TEST_JOB").unwrap();
        let mut events = vec![];
        read_sse(&c, &format!("{}/api/jobs/{job}/log/stream", c.base), |e, d| {
            events.push((e, d));
            Ok(())
        })
        .await
        .unwrap();
        assert!(events.iter().any(|(e, _)| e == "message"), "应该收到日志内容: {events:?}");
        assert_eq!(events.last().unwrap().0, "end");
    }
}
