use tauri::Manager;
use std::process::{Child, Command as StdCommand};
use std::sync::Mutex;

#[cfg(target_os = "windows")]
use std::os::windows::process::CommandExt;

#[cfg(target_os = "windows")]
const CREATE_NO_WINDOW: u32 = 0x08000000;

struct SidecarProcess(Mutex<Option<Child>>);

// Per-launch capability token. The Go sidecar requires it (header X-Sekai-Token)
// on mutating requests, so a stray web page hitting 127.0.0.1:9800 cannot drive
// state changes (settings → download → open). Empty in dev: the sidecar is an
// external process there and enforcement is off.
struct AppToken(String);

#[tauri::command]
fn auth_token(state: tauri::State<AppToken>) -> String {
  state.0.clone()
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
  let mut ctx = tauri::generate_context!();
  ctx.set_default_window_icon(Some(
    tauri::image::Image::from_bytes(
      include_bytes!("../icons/icon.png"),
    ).expect("Failed to load window icon"),
  ));

  tauri::Builder::default()
    .plugin(tauri_plugin_dialog::init())
    .invoke_handler(tauri::generate_handler![auth_token])
    .setup(|app| {
      if cfg!(debug_assertions) {
        app.handle().plugin(
          tauri_plugin_log::Builder::default()
            .level(log::LevelFilter::Info)
            .build(),
        )?;
      }

      // Generate the per-launch capability token (random in release; empty in dev
      // where the sidecar is external and enforcement is off).
      let token = if cfg!(debug_assertions) {
        String::new()
      } else {
        let mut b = [0u8; 16];
        getrandom::getrandom(&mut b).expect("failed to generate auth token");
        b.iter().map(|x| format!("{:02x}", x)).collect::<String>()
      };
      app.manage(AppToken(token.clone()));

      // Spawn Go backend in release mode (dev uses external server)
      if !cfg!(debug_assertions) {
        let resource_dir = app.path().resource_dir()
          .expect("failed to resolve resource directory");
        let data_dir = app.path().app_local_data_dir()
          .expect("failed to resolve app data directory");

        let exe_dir = std::env::current_exe()
          .expect("failed to get exe path")
          .parent()
          .expect("failed to get exe dir")
          .to_path_buf();

        #[cfg(target_os = "windows")]
        let sidecar_path = exe_dir.join("sekaitext-backend.exe");
        #[cfg(not(target_os = "windows"))]
        let sidecar_path = exe_dir.join("sekaitext-backend");

        let mut cmd = StdCommand::new(&sidecar_path);
        cmd.args([
          "--port", "9800",
          "--dir", &resource_dir.to_string_lossy(),
          "--data-dir", &data_dir.to_string_lossy(),
          "--auth-token", &token,
        ]);

        #[cfg(target_os = "windows")]
        cmd.creation_flags(CREATE_NO_WINDOW);

        let child = cmd.spawn()
          .unwrap_or_else(|e| panic!("failed to spawn {}: {}", sidecar_path.display(), e));

        app.manage(SidecarProcess(Mutex::new(Some(child))));
      }

      Ok(())
    })
    .on_window_event(|window, event| {
      if let tauri::WindowEvent::Destroyed = event {
        if !cfg!(debug_assertions) {
          if let Some(state) = window.app_handle().try_state::<SidecarProcess>() {
            if let Some(mut child) = state.0.lock().unwrap().take() {
              let _ = child.kill();
            }
          }
        }
        if cfg!(debug_assertions) {
          let _ = StdCommand::new("node")
            .args(["../scripts/cleanup.mjs", "9800", "5173"])
            .spawn();
        }
      }
    })
    .run(ctx)
    .expect("error while running tauri application");
}
