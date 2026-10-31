//! 只存在这台电脑上的状态：偏好设置、已读的失败和提醒、每台服务器已经通知到哪里。
//! 存在应用数据目录的 state.json。客户端只装在一台电脑上，不需要同步。

use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::sync::Mutex;

use serde::{Deserialize, Serialize};
use tauri::State;

#[derive(Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct Prefs {
    /// 任务失败、服务器异常时发系统通知
    pub notify: bool,
    /// 呼出迷你总览的全局快捷键，写法同 tauri-plugin-global-shortcut，例如 "Ctrl+Alt+D"
    pub shortcut: String,
    /// 开机自启
    pub autostart: bool,
}

impl Default for Prefs {
    fn default() -> Self {
        let shortcut = if cfg!(target_os = "macos") { "Cmd+Alt+D" } else { "Ctrl+Alt+D" };
        Prefs { notify: true, shortcut: shortcut.into(), autostart: true }
    }
}

#[derive(Default, Serialize, Deserialize)]
#[serde(default)]
struct Data {
    prefs: Prefs,
    /// 已读的失败任务（gpu1-42）和已确认的提醒，按加入顺序
    read: Vec<String>,
    /// 每台服务器已经处理过的失败任务结束时间（unix 秒），比它晚结束的失败才通知
    watermark: HashMap<String, i64>,
    /// 首次启动时的设置（例如开机自启）已经应用过
    initialized: bool,
}

pub struct LocalState {
    path: PathBuf,
    data: Mutex<Data>,
}

/// 已读记录只留最近这么多条，足够覆盖总览的 24 小时窗口
const READ_CAP: usize = 2000;

impl LocalState {
    pub fn load(path: PathBuf) -> Self {
        // 文件坏了就当作第一次启动：丢的只是已读记录和偏好
        let data = std::fs::read_to_string(&path).ok().and_then(|t| serde_json::from_str(&t).ok()).unwrap_or_default();
        LocalState { path, data: Mutex::new(data) }
    }

    fn save(&self, d: &Data) {
        if let Some(dir) = self.path.parent() {
            let _ = std::fs::create_dir_all(dir);
        }
        let tmp = self.path.with_extension("json.tmp");
        if std::fs::write(&tmp, serde_json::to_string_pretty(d).unwrap()).is_ok() {
            let _ = std::fs::rename(&tmp, &self.path);
        }
    }

    pub fn prefs(&self) -> Prefs {
        self.data.lock().unwrap().prefs.clone()
    }

    pub fn set_prefs(&self, p: Prefs) {
        let mut d = self.data.lock().unwrap();
        d.prefs = p;
        self.save(&d);
    }

    /// 第一次调用返回 true，之后都是 false
    pub fn first_run(&self) -> bool {
        let mut d = self.data.lock().unwrap();
        if d.initialized {
            return false;
        }
        d.initialized = true;
        self.save(&d);
        true
    }

    pub fn read_set(&self) -> HashSet<String> {
        self.data.lock().unwrap().read.iter().cloned().collect()
    }

    pub fn mark_read(&self, keys: &[String]) {
        let mut d = self.data.lock().unwrap();
        let mut changed = false;
        for k in keys {
            if !d.read.contains(k) {
                d.read.push(k.clone());
                changed = true;
            }
        }
        if changed {
            let n = d.read.len();
            if n > READ_CAP {
                d.read.drain(..n - READ_CAP);
            }
            self.save(&d);
        }
    }

    pub fn watermark(&self, server: &str) -> Option<i64> {
        self.data.lock().unwrap().watermark.get(server).copied()
    }

    pub fn set_watermark(&self, server: &str, v: i64) {
        let mut d = self.data.lock().unwrap();
        if d.watermark.get(server) != Some(&v) {
            d.watermark.insert(server.to_string(), v);
            self.save(&d);
        }
    }

    pub fn forget_server(&self, server: &str) {
        let mut d = self.data.lock().unwrap();
        d.watermark.remove(server);
        let prefix = format!("{server}-");
        d.read.retain(|k| !k.starts_with(&prefix));
        self.save(&d);
    }
}

#[tauri::command]
pub fn get_prefs(state: State<'_, LocalState>) -> Prefs {
    state.prefs()
}

/// 保存偏好，并立即应用：重新注册全局快捷键、打开或关闭开机自启
#[tauri::command]
pub fn set_prefs(app: tauri::AppHandle, state: State<'_, LocalState>, prefs: Prefs) -> Result<(), String> {
    crate::apply_shortcut(&app, &prefs.shortcut)?;
    crate::apply_autostart(&app, prefs.autostart)?;
    state.set_prefs(prefs);
    Ok(())
}
