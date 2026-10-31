//! 丹炉桌面客户端的 Rust 层。
//!
//! 界面不直接访问服务器：所有 HTTP 请求都在这里发出，每台服务器只信任它自己的本地 CA，
//! 带上存在系统凭据库里的设备令牌。轮询、通知、托盘也在这里，窗口隐藏时照常工作。
//! 添加服务器、升级、服务管理等运维操作调用系统自带的 OpenSSH。

mod fleet;
mod http;
mod ops;
mod servers;
mod ssh;
mod state;
mod tray;

use tauri::{AppHandle, Manager, WindowEvent};
use tauri_plugin_autostart::{MacosLauncher, ManagerExt as _};
use tauri_plugin_global_shortcut::{GlobalShortcutExt, ShortcutState};

/// 换全局快捷键：新的注册不上就恢复旧的
pub(crate) fn apply_shortcut(app: &AppHandle, shortcut: &str) -> Result<(), String> {
    let gs = app.global_shortcut();
    let old = app.state::<state::LocalState>().prefs().shortcut;
    let _ = gs.unregister_all();
    if let Err(e) = gs.register(shortcut) {
        let _ = gs.register(old.as_str());
        return Err(format!("快捷键 {shortcut} 用不了：{e}"));
    }
    Ok(())
}

pub(crate) fn apply_autostart(app: &AppHandle, on: bool) -> Result<(), String> {
    let al = app.autolaunch();
    if al.is_enabled().unwrap_or(false) == on {
        return Ok(());
    }
    let r = if on { al.enable() } else { al.disable() };
    r.map_err(|e| format!("设置开机自启失败：{e}"))
}

#[tauri::command]
fn app_version() -> &'static str {
    env!("CARGO_PKG_VERSION")
}

/// 在系统浏览器里打开 W&B / SwanLab 等链接，只允许 http(s)
#[tauri::command]
fn open_external(app: AppHandle, url: String) -> Result<(), String> {
    use tauri_plugin_opener::OpenerExt;
    if !(url.starts_with("https://") || url.starts_with("http://")) {
        return Err(format!("不打开 {url}"));
    }
    app.opener().open_url(url, None::<&str>).map_err(|e| e.to_string())
}

/// 在文件管理器里显示刚下载的文件
#[tauri::command]
fn reveal_file(app: AppHandle, path: String) -> Result<(), String> {
    use tauri_plugin_opener::OpenerExt;
    app.opener().reveal_item_in_dir(path).map_err(|e| e.to_string())
}

pub fn run() {
    // reqwest 用的 rustls 不自带加密实现，这里装上 ring
    let _ = rustls::crypto::ring::default_provider().install_default();

    tauri::Builder::default()
        // 必须第一个注册：再次启动（包括点系统通知）时只唤起已经在运行的那个，并定位到最近一条通知
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            let focus = app.state::<fleet::Fleet>().recent_target();
            tray::show_main(app, focus);
        }))
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_autostart::init(MacosLauncher::LaunchAgent, None))
        .plugin(
            tauri_plugin_global_shortcut::Builder::new()
                .with_handler(|app, _shortcut, e| {
                    if e.state == ShortcutState::Pressed {
                        tray::toggle_popover(app);
                    }
                })
                .build(),
        )
        .setup(|app| {
            let config = app.path().app_config_dir()?;
            let data = app.path().app_data_dir()?;
            app.manage(servers::Servers::load(config.join("servers.json"))?);
            app.manage(state::LocalState::load(data.join("state.json")));
            app.manage(http::Http::default());
            app.manage(fleet::Fleet::default());

            let handle = app.handle().clone();
            tray::setup(&handle)?;
            let prefs = handle.state::<state::LocalState>().prefs();
            if let Err(e) = handle.global_shortcut().register(prefs.shortcut.as_str()) {
                eprintln!("注册全局快捷键 {} 失败：{e}", prefs.shortcut);
            }
            // 默认开机自启。开发版不自动打开，免得把 target/debug 里的程序写进启动项
            if !cfg!(debug_assertions) && handle.state::<state::LocalState>().first_run() {
                let _ = apply_autostart(&handle, prefs.autostart);
            }

            // 关闭主窗口时缩回托盘；退出走托盘菜单
            let main = app.get_webview_window("main").expect("缺少主窗口");
            let w = main.clone();
            let h = handle.clone();
            main.on_window_event(move |e| match e {
                WindowEvent::CloseRequested { api, .. } => {
                    api.prevent_close();
                    let _ = w.hide();
                }
                // 窗口回到前台时马上刷新，不用等到下一轮
                WindowEvent::Focused(true) => h.state::<fleet::Fleet>().poke(),
                _ => {}
            });

            fleet::spawn(handle);
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            app_version,
            open_external,
            reveal_file,
            http::save_file,
            servers::list_servers,
            servers::add_server,
            servers::remove_server,
            ssh::ssh_hosts,
            http::api_request,
            http::api_stream,
            http::api_stream_close,
            fleet::get_fleet,
            fleet::refresh_fleet,
            fleet::mark_read,
            state::get_prefs,
            state::set_prefs,
            tray::open_main,
            ops::upgrade_sources,
            ops::upgrade_server,
            ops::service_action,
        ])
        .run(tauri::generate_context!())
        .expect("丹炉客户端启动失败");
}
