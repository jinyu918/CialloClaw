#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::sync::Mutex;

use tauri::{
    menu::{MenuBuilder, MenuItemBuilder, PredefinedMenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    AppHandle, Emitter, LogicalPosition, LogicalSize, Manager, Position, Size, State,
    WebviewWindow, WebviewWindowBuilder, WebviewUrl,
};

const WINDOW_LABEL: &str = "main";
const TRAY_ID: &str = "main-tray";
const STATUS_ID: &str = "status";
const TOGGLE_PAUSE_ID: &str = "toggle_pause";
const OPEN_PANEL_ID: &str = "open_panel";
const HIDE_ID: &str = "hide";
const EXIT_ID: &str = "exit";

struct RuntimeState {
    paused: bool,
    status: RuntimeStatus,
}

#[derive(Clone, Copy)]
enum DockSide {
    Left,
    Right,
}

impl DockSide {
    fn label(self) -> &'static str {
        match self {
            Self::Left => "left",
            Self::Right => "right",
        }
    }
}

#[derive(Clone, Copy)]
enum RuntimeStatus {
    Idle,
    Running,
    Paused,
    Dnd,
}

impl RuntimeStatus {
    fn from_str(value: &str) -> Self {
        match value {
            "running" => Self::Running,
            "paused" => Self::Paused,
            "dnd" => Self::Dnd,
            _ => Self::Idle,
        }
    }

    fn label(self) -> &'static str {
        match self {
            Self::Idle => "空闲",
            Self::Running => "运行中",
            Self::Paused => "暂停",
            Self::Dnd => "DND",
        }
    }
}

#[tauri::command]
fn sync_window_layout(
    app: tauri::AppHandle,
    width: f64,
    height: f64,
    mode: String,
) -> Result<String, String> {
    let window = app
        .get_webview_window(WINDOW_LABEL)
        .ok_or("window not found")?;
    apply_window_layout(&window, width, height, &mode)
}

#[tauri::command]
fn hide_to_tray(app: tauri::AppHandle) -> Result<(), String> {
    let window = app
        .get_webview_window(WINDOW_LABEL)
        .ok_or("window not found")?;
    window.hide().map_err(|error| error.to_string())?;
    Ok(())
}

#[tauri::command]
fn show_from_tray(app: tauri::AppHandle) -> Result<(), String> {
    show_main_window(&app)
}

#[tauri::command]
fn set_runtime_status(
    app: tauri::AppHandle,
    runtime: State<'_, Mutex<RuntimeState>>,
    status: String,
) -> Result<(), String> {
    {
        let mut runtime = runtime.lock().map_err(|_| "runtime lock poisoned")?;
        if runtime.paused && status != "paused" {
            return Ok(());
        }
        runtime.status = RuntimeStatus::from_str(&status);
    }

    refresh_tray_menu(&app, &runtime)?;
    Ok(())
}

fn apply_window_layout(
    window: &WebviewWindow,
    width: f64,
    height: f64,
    mode: &str,
) -> Result<String, String> {
    window
        .set_size(Size::Logical(LogicalSize { width, height }))
        .map_err(|error| error.to_string())?;

    if mode == "free" {
        return Ok(String::from("right"));
    }

    let monitor = window
        .current_monitor()
        .map_err(|error| error.to_string())?
        .ok_or("monitor not found")?;
    let scale_factor = window.scale_factor().map_err(|error| error.to_string())?;
    let monitor_position = monitor.position().to_logical::<f64>(scale_factor);
    let monitor_size = monitor.size().to_logical::<f64>(scale_factor);
    let dock_side = detect_dock_side(window, &monitor_position, &monitor_size)?;

    let side_margin = 24.0;
    let bottom_margin = 32.0;
    let peek_width = 28.0;
    let centered_y = monitor_position.y + monitor_size.height * 0.55 - height / 2.0;
    let min_y = monitor_position.y + 18.0;
    let max_y = monitor_position.y + monitor_size.height - height - 18.0;
    let y = centered_y.clamp(min_y, max_y);

    let (x, y) = match mode {
        "idle-peek" => (
            peek_x(dock_side, &monitor_position, monitor_size.width, width, peek_width),
            y,
        ),
        "idle-reveal" => (
            dock_x(dock_side, &monitor_position, monitor_size.width, width, side_margin),
            y,
        ),
        "preview" => (
            dock_x(dock_side, &monitor_position, monitor_size.width, width, side_margin),
            y,
        ),
        "status" => (
            dock_x(dock_side, &monitor_position, monitor_size.width, width, side_margin),
            y,
        ),
        "chat" => (
            dock_x(dock_side, &monitor_position, monitor_size.width, width, side_margin),
            monitor_position.y + monitor_size.height - height - bottom_margin,
        ),
        "menu" => (
            dock_x(dock_side, &monitor_position, monitor_size.width, width, side_margin),
            monitor_position.y + monitor_size.height - height - 118.0,
        ),
        _ => return Ok(dock_side.label().to_string()),
    };

    window
        .set_position(Position::Logical(LogicalPosition { x, y }))
        .map_err(|error| error.to_string())?;

    Ok(dock_side.label().to_string())
}

fn detect_dock_side(
    window: &WebviewWindow,
    monitor_position: &LogicalPosition<f64>,
    monitor_size: &LogicalSize<f64>,
) -> Result<DockSide, String> {
    let scale_factor = window.scale_factor().map_err(|error| error.to_string())?;
    let fallback_center = monitor_position.x + monitor_size.width / 2.0;
    let window_center = if let Ok(position) = window.outer_position() {
        let x = position.to_logical::<f64>(scale_factor).x;
        let size = window
            .outer_size()
            .map_err(|error| error.to_string())?
            .to_logical::<f64>(scale_factor);
        x + size.width / 2.0
    } else {
        fallback_center
    };

    Ok(if window_center < fallback_center {
        DockSide::Left
    } else {
        DockSide::Right
    })
}

fn dock_x(
    dock_side: DockSide,
    monitor_position: &LogicalPosition<f64>,
    monitor_width: f64,
    width: f64,
    side_margin: f64,
) -> f64 {
    match dock_side {
        DockSide::Left => monitor_position.x + side_margin,
        DockSide::Right => monitor_position.x + monitor_width - width - side_margin,
    }
}

fn peek_x(
    dock_side: DockSide,
    monitor_position: &LogicalPosition<f64>,
    monitor_width: f64,
    width: f64,
    peek_width: f64,
) -> f64 {
    match dock_side {
        DockSide::Left => monitor_position.x - (width - peek_width),
        DockSide::Right => monitor_position.x + monitor_width - peek_width,
    }
}

fn build_tray_menu(app: &AppHandle, runtime: &RuntimeState) -> tauri::Result<tauri::menu::Menu<tauri::Wry>> {
    let status_label = format!("状态：{}", runtime.status.label());
    let pause_label = if runtime.paused { "继续" } else { "暂停" };

    let status_item = MenuItemBuilder::with_id(STATUS_ID, status_label)
        .enabled(false)
        .build(app)?;
    let pause_item = MenuItemBuilder::with_id(TOGGLE_PAUSE_ID, pause_label).build(app)?;
    let open_item = MenuItemBuilder::with_id(OPEN_PANEL_ID, "打开控制面板").build(app)?;
    let hide_item = MenuItemBuilder::with_id(HIDE_ID, "隐藏").build(app)?;
    let exit_item = MenuItemBuilder::with_id(EXIT_ID, "退出程序").build(app)?;
    let separator = PredefinedMenuItem::separator(app)?;

    MenuBuilder::new(app)
        .items(&[
            &status_item,
            &separator,
            &pause_item,
            &open_item,
            &hide_item,
            &exit_item,
        ])
        .build()
}

fn refresh_tray_menu(app: &AppHandle, runtime: &State<'_, Mutex<RuntimeState>>) -> Result<(), String> {
    let runtime = runtime.lock().map_err(|_| "runtime lock poisoned")?;
    let tray = app.tray_by_id(TRAY_ID).ok_or("tray not found")?;
    let menu = build_tray_menu(app, &runtime).map_err(|error| error.to_string())?;
    tray.set_menu(Some(menu)).map_err(|error| error.to_string())?;
    Ok(())
}

fn show_main_window(app: &AppHandle) -> Result<(), String> {
    let window = app
        .get_webview_window(WINDOW_LABEL)
        .ok_or("window not found")?;

    apply_window_layout(&window, 96.0, 96.0, "idle-peek")?;
    window.show().map_err(|error| error.to_string())?;
    window.set_focus().map_err(|error| error.to_string())?;
    app.emit("tray://show", serde_json::json!({}))
        .map_err(|error| error.to_string())?;
    Ok(())
}

fn hide_main_window(app: &AppHandle) -> Result<(), String> {
    let window = app
        .get_webview_window(WINDOW_LABEL)
        .ok_or("window not found")?;
    window.hide().map_err(|error| error.to_string())?;
    app.emit("tray://hide", serde_json::json!({}))
        .map_err(|error| error.to_string())?;
    Ok(())
}

fn toggle_pause(app: &AppHandle, runtime: &State<'_, Mutex<RuntimeState>>) -> Result<(), String> {
    let paused = {
        let mut runtime = runtime.lock().map_err(|_| "runtime lock poisoned")?;
        runtime.paused = !runtime.paused;
        runtime.status = if runtime.paused {
            RuntimeStatus::Paused
        } else {
            RuntimeStatus::Idle
        };
        runtime.paused
    };

    refresh_tray_menu(app, runtime)?;
    app.emit("tray://pause", serde_json::json!({ "paused": paused }))
        .map_err(|error| error.to_string())?;
    Ok(())
}

fn main() {
    tauri::Builder::default()
        .manage(Mutex::new(RuntimeState {
            paused: false,
            status: RuntimeStatus::Idle,
        }))
        .plugin(tauri_plugin_positioner::init())
        .invoke_handler(tauri::generate_handler![
            sync_window_layout,
            hide_to_tray,
            show_from_tray,
            set_runtime_status
        ])
        .setup(|app| {
            let window = if let Some(window) = app.get_webview_window(WINDOW_LABEL) {
                window
            } else {
                WebviewWindowBuilder::new(app, WINDOW_LABEL, WebviewUrl::default())
                    .title("Orb Weave")
                    .decorations(false)
                    .transparent(true)
                    .shadow(false)
                    .always_on_top(true)
                    .resizable(false)
                    .skip_taskbar(true)
                    .inner_size(110.0, 110.0)
                    .build()
                    .map_err(|error| error.to_string())?
            };

            #[cfg(target_os = "windows")]
            {
                let _ = window_vibrancy::apply_acrylic(&window, Some((10, 18, 28, 120)));
            }

            let runtime = app.state::<Mutex<RuntimeState>>();
            let menu = {
                let runtime = runtime.lock().map_err(|_| "runtime lock poisoned")?;
                build_tray_menu(app.handle(), &runtime).map_err(|error| error.to_string())?
            };

            TrayIconBuilder::with_id(TRAY_ID)
                .menu(&menu)
                .show_menu_on_left_click(false)
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        let _ = show_main_window(tray.app_handle());
                    }
                })
                .build(app)
                .map_err(|error| error.to_string())?;

            let app_handle = app.handle().clone();
            app.on_menu_event(move |app, event| match event.id().as_ref() {
                TOGGLE_PAUSE_ID => {
                    let runtime = app.state::<Mutex<RuntimeState>>();
                    let _ = toggle_pause(&app_handle, &runtime);
                }
                OPEN_PANEL_ID => {
                    let _ = show_main_window(&app_handle);
                }
                HIDE_ID => {
                    let _ = hide_main_window(&app_handle);
                }
                EXIT_ID => {
                    app.exit(0);
                }
                _ => {}
            });

            let _ = apply_window_layout(&window, 96.0, 96.0, "idle-peek");

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
