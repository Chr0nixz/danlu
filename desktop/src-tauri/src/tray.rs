//! 托盘图标和迷你总览浮窗。macOS 上对应菜单栏图标，这里的代码不区分平台。
//!
//! 图标三种状态：一切正常（灰）、有任务在跑（炉火点着，朱砂色）、有没看过的失败或异常（加红点）。
//! 单击图标或按全局快捷键，在托盘旁弹出迷你总览；它失去焦点就收起。

use std::sync::Mutex;
use std::time::{Duration, Instant};

use serde::Serialize;
use tauri::image::Image;
use tauri::menu::{Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Emitter, Manager, PhysicalPosition, Rect, WebviewUrl, WebviewWindowBuilder, WindowEvent};

use crate::fleet::Focus;

#[derive(Clone, Copy, PartialEq, Eq, Serialize)]
pub enum TrayState {
    Idle,
    Running,
    Alert,
}

const TRAY_ID: &str = "main";
const POPOVER: &str = "tray";
/// 迷你总览的大小（逻辑像素）
const POPOVER_SIZE: (f64, f64) = (340.0, 480.0);

#[derive(Default)]
pub struct Anchor {
    /// 最近一次点击托盘图标的位置（物理像素），全局快捷键呼出时也弹在这里
    point: Mutex<Option<(f64, f64)>>,
    /// 浮窗因为失去焦点收起的时刻。点托盘图标时浮窗先失焦收起，紧接着收到点击，不能又弹出来
    blurred: Mutex<Option<Instant>>,
}

fn icon(state: TrayState) -> Image<'static> {
    let bytes: &[u8] = match state {
        TrayState::Idle => include_bytes!("../icons/tray/idle.png"),
        TrayState::Running => include_bytes!("../icons/tray/running.png"),
        TrayState::Alert => include_bytes!("../icons/tray/alert.png"),
    };
    Image::from_bytes(bytes).expect("托盘图标损坏")
}

pub fn setup(app: &AppHandle) -> tauri::Result<()> {
    app.manage(Anchor::default());
    let menu = Menu::with_items(
        app,
        &[
            &MenuItem::with_id(app, "open", "打开主窗口", true, None::<&str>)?,
            &PredefinedMenuItem::separator(app)?,
            &MenuItem::with_id(app, "quit", "退出丹炉", true, None::<&str>)?,
        ],
    )?;
    TrayIconBuilder::with_id(TRAY_ID)
        .icon(icon(TrayState::Idle))
        .tooltip("丹炉")
        .menu(&menu)
        .show_menu_on_left_click(false)
        .on_menu_event(|app, e| match e.id().as_ref() {
            "open" => show_main(app, None),
            "quit" => app.exit(0),
            _ => {}
        })
        .on_tray_icon_event(|tray, e| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left, button_state: MouseButtonState::Up, rect, ..
            } = e
            {
                let app = tray.app_handle();
                *app.state::<Anchor>().point.lock().unwrap() = Some(rect_center(&rect));
                toggle_popover(app);
            }
        })
        .build(app)?;

    let popover = WebviewWindowBuilder::new(app, POPOVER, WebviewUrl::App("tray.html".into()))
        .title("丹炉")
        .inner_size(POPOVER_SIZE.0, POPOVER_SIZE.1)
        .resizable(false)
        .decorations(false)
        .skip_taskbar(true)
        .always_on_top(true)
        .visible(false)
        .focused(false)
        .shadow(true)
        .build()?;
    let w = popover.clone();
    let handle = app.clone();
    popover.on_window_event(move |e| {
        if let WindowEvent::Focused(false) = e {
            if w.is_visible().unwrap_or(false) {
                *handle.state::<Anchor>().blurred.lock().unwrap() = Some(Instant::now());
                let _ = w.hide();
            }
        }
    });
    Ok(())
}

fn rect_center(r: &Rect) -> (f64, f64) {
    let p = r.position.to_physical::<f64>(1.0);
    let s = r.size.to_physical::<f64>(1.0);
    (p.x + s.width / 2.0, p.y + s.height / 2.0)
}

pub fn set_state(app: &AppHandle, state: TrayState) {
    if let Some(tray) = app.tray_by_id(TRAY_ID) {
        let _ = tray.set_icon(Some(icon(state)));
        let tip = match state {
            TrayState::Idle => "丹炉",
            TrayState::Running => "丹炉 · 有任务在跑",
            TrayState::Alert => "丹炉 · 有没看过的失败或异常",
        };
        let _ = tray.set_tooltip(Some(tip));
    }
}

pub fn toggle_popover(app: &AppHandle) {
    let Some(w) = app.get_webview_window(POPOVER) else { return };
    if w.is_visible().unwrap_or(false) {
        let _ = w.hide();
        return;
    }
    let just_blurred =
        app.state::<Anchor>().blurred.lock().unwrap().is_some_and(|t| t.elapsed() < Duration::from_millis(300));
    if just_blurred {
        return;
    }
    place_popover(app, &w);
    let _ = w.show();
    let _ = w.set_focus();
    let _ = w.emit("popover-shown", ());
    app.state::<crate::fleet::Fleet>().poke();
}

/// 贴着托盘弹出：任务栏在下面就弹在图标上方，在上面（macOS 菜单栏）就弹在下方，左右不超出屏幕
fn place_popover(app: &AppHandle, w: &tauri::WebviewWindow) {
    let anchor = *app.state::<Anchor>().point.lock().unwrap();
    let monitor = match anchor {
        Some((x, y)) => app.monitor_from_point(x, y).ok().flatten(),
        None => None,
    }
    .or_else(|| app.primary_monitor().ok().flatten());
    let Some(m) = monitor else { return };
    let scale = m.scale_factor();
    let area = m.work_area();
    let (left, top) = (area.position.x as f64, area.position.y as f64);
    let (right, bottom) = (left + area.size.width as f64, top + area.size.height as f64);
    let (w_px, h_px) = (POPOVER_SIZE.0 * scale, POPOVER_SIZE.1 * scale);
    let gap = 8.0 * scale;
    // 没点过托盘图标时（全局快捷键），默认右下角
    let (ax, ay) = anchor.unwrap_or((right, bottom));
    let x = (ax - w_px / 2.0).clamp(left + gap, right - w_px - gap);
    let y = if ay < (top + bottom) / 2.0 { top + gap } else { bottom - h_px - gap };
    let _ = w.set_position(PhysicalPosition::new(x.round() as i32, y.round() as i32));
}

/// 打开主窗口；focus 不为空时让界面定位到那个任务或服务器
pub fn show_main(app: &AppHandle, focus: Option<Focus>) {
    if let Some(p) = app.get_webview_window(POPOVER) {
        let _ = p.hide();
    }
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.unminimize();
        let _ = w.show();
        let _ = w.set_focus();
        if let Some(f) = focus {
            let _ = w.emit("focus", f);
        }
    }
    app.state::<crate::fleet::Fleet>().poke();
}

#[tauri::command]
pub fn open_main(app: AppHandle, focus: Option<FocusArg>) {
    show_main(&app, focus.map(|f| Focus { kind: if f.kind == "server" { "server" } else { "job" }, key: f.key }));
}

#[derive(serde::Deserialize)]
pub struct FocusArg {
    kind: String,
    key: String,
}
