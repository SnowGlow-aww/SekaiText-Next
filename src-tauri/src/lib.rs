// The stdio transport (frames, multiplexer, protocol handler) is release-only,
// gated behind `#[cfg(not(debug_assertions))]`. In a debug build it is dead code,
// so silence the resulting cosmetic warnings there; release still warns normally.
#![cfg_attr(debug_assertions, allow(dead_code, unused_imports))]

use std::collections::HashMap;
use std::io::{self, BufRead, BufReader, Read, Write};
use std::process::{Child, ChildStderr, ChildStdout, Command as StdCommand, Stdio};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::mpsc::{sync_channel, SyncSender};
use std::sync::{Arc, Condvar, Mutex};
use std::time::Duration;

use serde::{Deserialize, Serialize};
use tauri::http::header::{
  ACCESS_CONTROL_ALLOW_HEADERS, ACCESS_CONTROL_ALLOW_METHODS, ACCESS_CONTROL_ALLOW_ORIGIN,
};
use tauri::http::{HeaderName, HeaderValue, Method, Response, StatusCode};
use tauri::Manager;

#[cfg(target_os = "windows")]
use std::os::windows::process::CommandExt;

#[cfg(target_os = "windows")]
const CREATE_NO_WINDOW: u32 = 0x08000000;

// The bundled Go child. Kept so we can kill it on shutdown as a backstop after
// closing its stdin (which already drives a clean EOF exit on the Go side).
struct SidecarProcess(Mutex<Option<Child>>);

// Saves `contents` to a user-chosen path via the NATIVE save dialog. The path comes
// from the OS picker, never from JS, so a loaded plugin can't use this to write to
// an arbitrary location (the reason it's not a raw path-taking write command). Used
// by glossary export, which a custom `sekai://` webview can't do via browser download.
// Returns the chosen path, or None if the user cancelled.
#[tauri::command]
fn save_text_dialog(
  app: tauri::AppHandle,
  default_name: String,
  contents: String,
) -> Result<Option<String>, String> {
  use tauri_plugin_dialog::DialogExt;
  let picked = app
    .dialog()
    .file()
    .set_file_name(default_name)
    .add_filter("JSON", &["json"])
    .blocking_save_file();
  match picked {
    Some(fp) => {
      let path = fp.as_path().ok_or("invalid save path")?.to_path_buf();
      std::fs::write(&path, &contents).map_err(|e| e.to_string())?;
      Ok(Some(path.to_string_lossy().into_owned()))
    }
    None => Ok(None),
  }
}

// ── stdio transport ─────────────────────────────────────────────────────────
//
// In release the frontend talks to Go over a custom `sekai://` URI scheme instead
// of TCP. Each webview request is framed (§2 of the contract) and written to the
// child's stdin; the child writes framed responses back on stdout. A single reader
// thread demultiplexes responses by id into per-request channels — mirroring the
// id-correlated pending map in backend/internal/service/engine.go.

// Frame magic; every frame carries it so a desync is detectable.
const MAGIC: &[u8; 4] = b"SKF1";

// Request header (Rust → Go). Field names are fixed by the contract.
#[derive(Serialize)]
struct ReqHeader<'a> {
  id: u64,
  method: &'a str,
  path: &'a str,
  query: &'a str,
  headers: HashMap<String, String>,
}

// Response header (Go → Rust).
#[derive(Deserialize)]
struct RespHeader {
  id: u64,
  status: u16,
  #[serde(default)]
  headers: HashMap<String, String>,
}

// A delivered response routed back to the waiting request thread.
struct IpcResp {
  status: u16,
  headers: HashMap<String, String>,
  body: Vec<u8>,
}

struct IpcInner {
  // Serializes writes so each frame reaches the child uninterleaved. Set to None
  // once the transport is torn down (child EOF/desync/death) so further writes fail
  // fast instead of writing to a dead pipe and parking the full timeout.
  stdin: Mutex<Option<std::process::ChildStdin>>,
  // Request id allocator; 0 is reserved for the Go "ready" frame, so start at 1.
  next_id: AtomicU64,
  // id → oneshot-style sender awaiting that response.
  pending: Mutex<HashMap<u64, SyncSender<IpcResp>>>,
  // Ready gate: flipped true when Go emits its id=0 ready frame.
  ready: (Mutex<bool>, Condvar),
  // Set once the reader loop exits (EOF / desync / child death). Every waiter and
  // every new request then fails fast rather than blocking for the full timeout.
  dead: AtomicBool,
}

// Managed Tauri state wrapping the shared transport.
struct Ipc(Arc<IpcInner>);

impl IpcInner {
  // Blocks until the backend has signalled ready or `timeout` elapses. A dead
  // transport flips ready (so this returns promptly); callers re-check `dead`.
  fn wait_ready(&self, timeout: Duration) -> bool {
    let (lock, cvar) = &self.ready;
    let guard = lock.lock().unwrap_or_else(|e| e.into_inner());
    let (_guard, res) = cvar
      .wait_timeout_while(guard, timeout, |ready| !*ready)
      .unwrap_or_else(|e| e.into_inner());
    !res.timed_out()
  }

  // Performs one request/response round-trip over the stdio pipe. Blocking by
  // design — callers run it off the main thread (a per-request worker thread).
  fn exchange(
    &self,
    method: &str,
    path: &str,
    query: &str,
    headers: HashMap<String, String>,
    body: &[u8],
    ready_timeout: Duration,
    resp_timeout: Duration,
  ) -> Result<IpcResp, &'static str> {
    if self.dead.load(Ordering::SeqCst) {
      return Err("backend transport closed");
    }
    if !self.wait_ready(ready_timeout) {
      return Err("backend not ready");
    }
    // The ready gate also flips on death; re-check so we don't write to a dead pipe.
    if self.dead.load(Ordering::SeqCst) {
      return Err("backend transport closed");
    }

    let id = self.next_id.fetch_add(1, Ordering::SeqCst);
    let (tx, rx) = sync_channel::<IpcResp>(1);
    self.pending.lock().unwrap_or_else(|e| e.into_inner()).insert(id, tx);

    let header = ReqHeader {
      id,
      method,
      path,
      query,
      headers,
    };
    let header_json = match serde_json::to_vec(&header) {
      Ok(j) => j,
      Err(_) => {
        self.pending.lock().unwrap_or_else(|e| e.into_inner()).remove(&id);
        return Err("encode request header");
      }
    };

    {
      let mut guard = self.stdin.lock().unwrap_or_else(|e| e.into_inner());
      let ok = match guard.as_mut() {
        Some(stdin) => write_frame(stdin, &header_json, body).is_ok(),
        None => false, // transport torn down
      };
      drop(guard);
      if !ok {
        self.pending.lock().unwrap_or_else(|e| e.into_inner()).remove(&id);
        return Err("write to backend failed");
      }
    }

    match rx.recv_timeout(resp_timeout) {
      Ok(resp) => Ok(resp),
      Err(_) => {
        self.pending.lock().unwrap_or_else(|e| e.into_inner()).remove(&id);
        Err("backend response timeout")
      }
    }
  }
}

// Writes one frame: MAGIC | headerLen(u32 LE) | bodyLen(u32 LE) | header | body.
fn write_frame<W: Write>(w: &mut W, header: &[u8], body: &[u8]) -> io::Result<()> {
  w.write_all(MAGIC)?;
  w.write_all(&(header.len() as u32).to_le_bytes())?;
  w.write_all(&(body.len() as u32).to_le_bytes())?;
  w.write_all(header)?;
  w.write_all(body)?;
  w.flush()
}

// Reads one frame, validating the magic. Returns (headerJSON, body).
fn read_frame<R: Read>(r: &mut R) -> io::Result<(Vec<u8>, Vec<u8>)> {
  let mut magic = [0u8; 4];
  r.read_exact(&mut magic)?;
  if &magic != MAGIC {
    return Err(io::Error::new(io::ErrorKind::InvalidData, "bad frame magic"));
  }
  let mut len = [0u8; 4];
  r.read_exact(&mut len)?;
  let header_len = u32::from_le_bytes(len) as usize;
  r.read_exact(&mut len)?;
  let body_len = u32::from_le_bytes(len) as usize;

  let mut header = vec![0u8; header_len];
  r.read_exact(&mut header)?;
  let mut body = vec![0u8; body_len];
  r.read_exact(&mut body)?;
  Ok((header, body))
}

// Demultiplexes response frames from the child's stdout. Runs for the life of the
// child; on EOF/desync it fails every pending request so none hang.
fn reader_loop(inner: Arc<IpcInner>, stdout: ChildStdout) {
  let mut reader = BufReader::with_capacity(1 << 16, stdout);
  loop {
    match read_frame(&mut reader) {
      Ok((header, body)) => {
        let h: RespHeader = match serde_json::from_slice(&header) {
          Ok(h) => h,
          Err(_) => continue, // skip an unparseable header rather than desync
        };
        if h.id == 0 {
          // Ready control frame: open the gate.
          let (lock, cvar) = &inner.ready;
          *lock.lock().unwrap_or_else(|e| e.into_inner()) = true;
          cvar.notify_all();
          continue;
        }
        if let Some(tx) = inner.pending.lock().unwrap_or_else(|e| e.into_inner()).remove(&h.id) {
          let _ = tx.send(IpcResp {
            status: h.status,
            headers: h.headers,
            body,
          });
        }
      }
      Err(_) => break, // EOF, desync, or child death → terminal teardown below
    }
  }

  // The response stream ended for good. Mark the transport dead so every future
  // request fails fast (exchange checks `dead` before writing) instead of writing
  // to a now-unmonitored stdin and parking the full timeout. Then: drop our stdin
  // handle so the child gets EOF and exits (this also unwedges a child that had
  // blocked on stdout backpressure after we stopped reading); drop all pending
  // senders so blocked receivers wake immediately; and flip+notify the ready gate
  // so any thread parked in wait_ready returns at once (it re-checks `dead`).
  inner.dead.store(true, Ordering::SeqCst);
  *inner.stdin.lock().unwrap_or_else(|e| e.into_inner()) = None;
  inner.pending.lock().unwrap_or_else(|e| e.into_inner()).clear();
  let (lock, cvar) = &inner.ready;
  *lock.lock().unwrap_or_else(|e| e.into_inner()) = true;
  cvar.notify_all();
}

// Forwards the child's stderr to our own stderr so backend logs are visible and
// the pipe never fills up (which would deadlock the child).
fn drain_stderr(stderr: ChildStderr) {
  let reader = BufReader::new(stderr);
  for line in reader.lines() {
    match line {
      Ok(l) => eprintln!("[backend] {l}"),
      Err(_) => break,
    }
  }
}

// Builds an http::Response carrying `body`, copying `extra_headers` (minus any
// CORS / content-length the backend set) and ALWAYS appending the three CORS
// allow-* headers so the custom-scheme origin can read the response.
fn cors_response(status: u16, body: Vec<u8>, extra_headers: &HashMap<String, String>) -> Response<Vec<u8>> {
  let mut resp = Response::new(body);
  *resp.status_mut() = StatusCode::from_u16(status).unwrap_or(StatusCode::INTERNAL_SERVER_ERROR);
  let h = resp.headers_mut();
  for (k, v) in extra_headers {
    let kl = k.to_ascii_lowercase();
    // Drop the backend's own CORS headers (we set our own below) and its
    // Content-Length (the platform derives it from the body; a stale value would
    // truncate large binary streams like live2d models).
    if kl == "content-length"
      || kl == "access-control-allow-origin"
      || kl == "access-control-allow-headers"
      || kl == "access-control-allow-methods"
    {
      continue;
    }
    if let (Ok(name), Ok(val)) = (HeaderName::from_bytes(k.as_bytes()), HeaderValue::from_str(v)) {
      h.append(name, val);
    }
  }
  h.insert(ACCESS_CONTROL_ALLOW_ORIGIN, HeaderValue::from_static("*"));
  h.insert(ACCESS_CONTROL_ALLOW_HEADERS, HeaderValue::from_static("*"));
  h.insert(ACCESS_CONTROL_ALLOW_METHODS, HeaderValue::from_static("*"));
  resp
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
  let mut ctx = tauri::generate_context!();
  ctx.set_default_window_icon(Some(
    tauri::image::Image::from_bytes(include_bytes!("../icons/icon.png"))
      .expect("Failed to load window icon"),
  ));

  // The frontend reads window.__SEKAI_ORIGIN__ to decide where to send requests.
  // Release uses the custom scheme (platform-dependent origin); dev keeps TCP.
  #[cfg(windows)]
  const RELEASE_ORIGIN: &str = "http://sekai.localhost";
  #[cfg(not(windows))]
  const RELEASE_ORIGIN: &str = "sekai://localhost";
  let origin = if cfg!(debug_assertions) {
    "http://localhost:9800"
  } else {
    RELEASE_ORIGIN
  };
  // {:?} emits a properly-quoted/escaped JS string literal.
  let init_script = format!("window.__SEKAI_ORIGIN__={:?};", origin);
  let origin_plugin = tauri::plugin::Builder::<tauri::Wry>::new("sekai-origin")
    .js_init_script(init_script)
    .build();

  let mut builder = tauri::Builder::default();

  // single-instance must be registered first: a second launch focuses the existing
  // window instead of spawning a competing backend. Release-only — dev relies on
  // the external TCP server and `tauri dev` hot-reload.
  #[cfg(not(debug_assertions))]
  {
    builder = builder.plugin(tauri_plugin_single_instance::init(|app, _argv, _cwd| {
      if let Some(w) = app.get_webview_window("main") {
        let _ = w.unminimize();
        let _ = w.show();
        let _ = w.set_focus();
      }
    }));
  }

  builder = builder
    .plugin(tauri_plugin_dialog::init())
    .plugin(origin_plugin)
    .invoke_handler(tauri::generate_handler![save_text_dialog])
    .setup(|app| {
      if cfg!(debug_assertions) {
        app.handle().plugin(
          tauri_plugin_log::Builder::default()
            .level(log::LevelFilter::Info)
            .build(),
        )?;
      }

      // Spawn the bundled Go backend in release and wire up the stdio transport.
      // Dev spawns nothing — it talks to the externally-run server over TCP.
      if !cfg!(debug_assertions) {
        let resource_dir = app
          .path()
          .resource_dir()
          .expect("failed to resolve resource directory");
        let data_dir = app
          .path()
          .app_local_data_dir()
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

        let res = resource_dir.to_string_lossy().to_string();
        let data = data_dir.to_string_lossy().to_string();

        let mut cmd = StdCommand::new(&sidecar_path);
        // --ipc: no TCP bind, frames over stdin/stdout. No --port / --auth-token.
        cmd
          .args(["--ipc", "--dir", res.as_str(), "--data-dir", data.as_str()])
          .stdin(Stdio::piped())
          .stdout(Stdio::piped())
          .stderr(Stdio::piped());

        #[cfg(target_os = "windows")]
        cmd.creation_flags(CREATE_NO_WINDOW);

        let mut child = cmd
          .spawn()
          .unwrap_or_else(|e| panic!("failed to spawn {}: {}", sidecar_path.display(), e));

        let child_stdin = child.stdin.take().expect("sidecar stdin");
        let child_stdout = child.stdout.take().expect("sidecar stdout");
        let child_stderr = child.stderr.take().expect("sidecar stderr");

        let inner = Arc::new(IpcInner {
          stdin: Mutex::new(Some(child_stdin)),
          next_id: AtomicU64::new(1),
          pending: Mutex::new(HashMap::new()),
          ready: (Mutex::new(false), Condvar::new()),
          dead: AtomicBool::new(false),
        });

        {
          let inner = inner.clone();
          std::thread::spawn(move || reader_loop(inner, child_stdout));
        }
        std::thread::spawn(move || drain_stderr(child_stderr));

        app.manage(Ipc(inner));
        app.manage(SidecarProcess(Mutex::new(Some(child))));
      }

      Ok(())
    })
    .on_window_event(|window, event| {
      // Release teardown (recovery clear + child kill) is handled in the
      // RunEvent::Exit handler so it fires on every quit gesture (window close,
      // Cmd-Q, Dock quit) exactly once, and never on the detached live2d window.
      // Here we only do the dev-server port cleanup, gated to the main window so
      // closing the live2d window in dev doesn't kill the running dev servers.
      if cfg!(debug_assertions) {
        if let tauri::WindowEvent::Destroyed = event {
          if window.label() == "main" {
            let _ = StdCommand::new("node")
              .args(["../scripts/cleanup.mjs", "9800", "5173"])
              .spawn();
          }
        }
      }
    });

  // Register the custom scheme only in release; dev never issues sekai:// requests.
  #[cfg(not(debug_assertions))]
  {
    builder = builder.register_asynchronous_uri_scheme_protocol("sekai", |ctx, request, responder| {
      // OPTIONS preflight is short-circuited in Rust — never round-trips to Go.
      if *request.method() == Method::OPTIONS {
        responder.respond(cors_response(204, Vec::new(), &HashMap::new()));
        return;
      }

      let app = ctx.app_handle().clone();
      let method = request.method().as_str().to_string();
      let path = request.uri().path().to_string();
      let query = request.uri().query().unwrap_or("").to_string();
      let mut headers = HashMap::new();
      for (k, v) in request.headers() {
        if let Ok(s) = v.to_str() {
          headers.insert(k.as_str().to_string(), s.to_string());
        }
      }
      let body = request.body().clone();

      // Never block the main/IPC thread: do the stdio round-trip on a worker.
      std::thread::spawn(move || {
        let inner = match app.try_state::<Ipc>() {
          Some(s) => s.0.clone(),
          None => {
            responder.respond(cors_response(503, b"backend not initialized".to_vec(), &HashMap::new()));
            return;
          }
        };
        // Most requests are short; a few are legitimately long-running disk ops.
        // live2d/import does a merge-move that, across volumes (e.g. an external
        // drive), recursively copies a multi-GB asset library — easily past 120s.
        // Give it a generous response window so a slow-but-succeeding import isn't
        // surfaced as a false failure. The request body is tiny and exchange()
        // releases the stdin lock before waiting, so this never blocks other requests.
        let resp_timeout = if path.contains("/live2d/import") {
          Duration::from_secs(1800)
        } else {
          Duration::from_secs(120)
        };
        match inner.exchange(
          &method,
          &path,
          &query,
          headers,
          &body,
          Duration::from_secs(120),
          resp_timeout,
        ) {
          Ok(resp) => responder.respond(cors_response(resp.status, resp.body, &resp.headers)),
          Err(e) => {
            responder.respond(cors_response(502, format!("ipc error: {e}").into_bytes(), &HashMap::new()))
          }
        }
      });
    });
  }

  builder
    .build(ctx)
    .expect("error while building tauri application")
    .run(|app, event| {
      // RunEvent::Exit is the single terminal event that fires on EVERY quit path
      // (window close, Cmd-Q, Dock quit, Apple-event quit). On macOS an Apple-event
      // quit emits ONLY Exit (no ExitRequested, no per-window Destroyed), so Exit is
      // the reliable hook. Release only: best-effort recovery clear (replacing the
      // old beforeunload sendBeacon) with short timeouts so quit isn't delayed, then
      // kill the backend child (its stdin EOFs Go anyway; this is the backstop). Dev
      // clears recovery via the App.vue beforeunload beacon over TCP.
      #[cfg(not(debug_assertions))]
      if let tauri::RunEvent::Exit = event {
        if let Some(ipc) = app.try_state::<Ipc>() {
          let _ = ipc.0.exchange(
            "DELETE",
            "/api/v1/recovery/clear",
            "",
            HashMap::new(),
            &[],
            Duration::from_millis(200),
            Duration::from_millis(400),
          );
        }
        if let Some(state) = app.try_state::<SidecarProcess>() {
          if let Some(mut child) = state.0.lock().unwrap_or_else(|e| e.into_inner()).take() {
            let _ = child.kill();
          }
        }
      }
      #[cfg(debug_assertions)]
      let _ = (app, event);
    });
}
