// The stdio transport (frames, multiplexer, protocol handler) is release-only,
// gated behind `#[cfg(not(debug_assertions))]`. In a debug build it is dead code,
// so silence the resulting cosmetic warnings there; release still warns normally.
#![cfg_attr(debug_assertions, allow(dead_code, unused_imports))]

use std::collections::HashMap;
use std::io::{self, BufRead, BufReader, Read, Write};
use std::process::{Child, ChildStderr, ChildStdout, Command as StdCommand, Stdio};
use std::sync::atomic::{AtomicBool, AtomicU64, AtomicUsize, Ordering};
use std::sync::mpsc::{sync_channel, RecvTimeoutError, SyncSender};
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

#[cfg(unix)]
use std::os::unix::process::CommandExt;

#[cfg(target_os = "windows")]
const CREATE_NO_WINDOW: u32 = 0x08000000;

#[cfg(target_os = "windows")]
const CREATE_NEW_PROCESS_GROUP: u32 = 0x00000200;

// The bundled Go child. Kept so we can kill it on shutdown as a backstop after
// closing its stdin (which already drives a clean EOF exit on the Go side).
struct SidecarProcess {
    child: Arc<Mutex<Option<Child>>>,
    #[cfg(target_os = "windows")]
    job: Arc<Mutex<Option<windows_job::Job>>>,
}

#[cfg(target_os = "windows")]
mod windows_job {
    use std::ffi::c_void;
    use std::io;
    use std::mem::{size_of, zeroed};
    use std::os::windows::io::AsRawHandle;
    use std::process::Child;
    use std::ptr::null;

    type Handle = *mut c_void;
    const JOB_OBJECT_EXTENDED_LIMIT_INFORMATION_CLASS: i32 = 9;
    const JOB_OBJECT_LIMIT_BREAKAWAY_OK: u32 = 0x0000_0800;
    const JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: u32 = 0x0000_2000;

    #[repr(C)]
    struct BasicLimitInformation {
        per_process_user_time_limit: i64,
        per_job_user_time_limit: i64,
        limit_flags: u32,
        minimum_working_set_size: usize,
        maximum_working_set_size: usize,
        active_process_limit: u32,
        affinity: usize,
        priority_class: u32,
        scheduling_class: u32,
    }

    #[repr(C)]
    struct ExtendedLimitInformation {
        basic_limit_information: BasicLimitInformation,
        io_info: [u64; 6],
        process_memory_limit: usize,
        job_memory_limit: usize,
        peak_process_memory_used: usize,
        peak_job_memory_used: usize,
    }

    #[link(name = "kernel32")]
    extern "system" {
        fn CreateJobObjectW(attributes: *const c_void, name: *const u16) -> Handle;
        fn SetInformationJobObject(job: Handle, class: i32, info: *const c_void, len: u32) -> i32;
        fn AssignProcessToJobObject(job: Handle, process: Handle) -> i32;
        fn CloseHandle(handle: Handle) -> i32;
    }

    pub struct Job(usize);

    impl Job {
        pub fn assign(child: &Child) -> io::Result<Self> {
            unsafe {
                let handle = CreateJobObjectW(null(), null());
                if handle.is_null() {
                    return Err(io::Error::last_os_error());
                }
                let mut info: ExtendedLimitInformation = zeroed();
                info.basic_limit_information.limit_flags =
                    JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | JOB_OBJECT_LIMIT_BREAKAWAY_OK;
                if SetInformationJobObject(
                    handle,
                    JOB_OBJECT_EXTENDED_LIMIT_INFORMATION_CLASS,
                    &info as *const _ as *const c_void,
                    size_of::<ExtendedLimitInformation>() as u32,
                ) == 0
                {
                    let err = io::Error::last_os_error();
                    CloseHandle(handle);
                    return Err(err);
                }
                if AssignProcessToJobObject(handle, child.as_raw_handle() as Handle) == 0 {
                    let err = io::Error::last_os_error();
                    CloseHandle(handle);
                    return Err(err);
                }
                Ok(Self(handle as usize))
            }
        }
    }

    impl Drop for Job {
        fn drop(&mut self) {
            unsafe {
                CloseHandle(self.0 as Handle);
            }
        }
    }
}

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

// Quits the entire app. Used by the self-updater right after the installer is
// launched so the user doesn't have to manually quit before the new version can
// replace this still-running one. Routes through app.exit(0) → RunEvent::Exit,
// which runs the normal teardown (recovery clear + backend child kill). The
// launched installer (`open`/`start`) is detached, so killing our sidecar on exit
// doesn't touch it.
#[tauri::command]
fn quit_app(app: tauri::AppHandle) {
    app.exit(0);
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
const MAX_FRAME_HEADER_SIZE: u32 = 1 << 20;
const MAX_REQUEST_BODY_SIZE: u32 = 16 << 20;
const MAX_FRAME_BODY_SIZE: u32 = 128 << 20;
const MAX_CONCURRENT_REQUESTS: usize = 8;
const IPC_OVERLOADED: &str = "backend request limit reached";
const IPC_REQUEST_TOO_LARGE: &str = "request frame exceeds size limit";

// Request header (Rust → Go). Field names are fixed by the contract.
#[derive(Serialize)]
struct ReqHeader<'a> {
    id: u64,
    method: &'a str,
    path: &'a str,
    query: &'a str,
    headers: HashMap<String, String>,
}

#[derive(Serialize)]
struct CancelHeader {
    id: u64,
    cancel: u64,
}

// Response header (Go → Rust).
#[derive(Deserialize)]
struct RespHeader {
    id: u64,
    status: u16,
    #[serde(default)]
    headers: HashMap<String, String>,
}

// Minimal fallback view: recovers just the request id when a response header fails
// to fully deserialize into RespHeader (e.g. the backend omitted the required
// `status`). Lets the reader fail the one waiting request fast instead of leaving it
// to park the full response timeout.
#[derive(Deserialize)]
struct RespId {
    id: u64,
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
    // Bounds URI worker threads independently of the pending-map check in exchange.
    in_flight: AtomicUsize,
}

// Managed Tauri state wrapping the shared transport.
struct Ipc(Arc<IpcInner>);

struct RequestPermit(Arc<IpcInner>);

impl Drop for RequestPermit {
    fn drop(&mut self) {
        self.0.in_flight.fetch_sub(1, Ordering::SeqCst);
    }
}

impl IpcInner {
    fn try_acquire(self: &Arc<Self>) -> Option<RequestPermit> {
        self.in_flight
            .fetch_update(Ordering::SeqCst, Ordering::SeqCst, |current| {
                (current < MAX_CONCURRENT_REQUESTS).then_some(current + 1)
            })
            .ok()
            .map(|_| RequestPermit(self.clone()))
    }

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
        let header = ReqHeader {
            id,
            method,
            path,
            query,
            headers,
        };
        let header_json = match serde_json::to_vec(&header) {
            Ok(j) => j,
            Err(_) => return Err("encode request header"),
        };
        if validate_frame_lengths(header_json.len(), body.len(), MAX_REQUEST_BODY_SIZE).is_err() {
            return Err(IPC_REQUEST_TOO_LARGE);
        }

        let (tx, rx) = sync_channel::<IpcResp>(1);
        {
            let mut pending = self.pending.lock().unwrap_or_else(|e| e.into_inner());
            if pending.len() >= MAX_CONCURRENT_REQUESTS {
                return Err(IPC_OVERLOADED);
            }
            pending.insert(id, tx);
        }

        {
            let mut guard = self.stdin.lock().unwrap_or_else(|e| e.into_inner());
            let ok = match guard.as_mut() {
                Some(stdin) => write_frame(stdin, &header_json, body).is_ok(),
                None => false, // transport torn down
            };
            drop(guard);
            if !ok {
                self.pending
                    .lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .remove(&id);
                return Err("write to backend failed");
            }
        }

        match rx.recv_timeout(resp_timeout) {
            Ok(resp) => Ok(resp),
            Err(RecvTimeoutError::Timeout) => {
                self.pending
                    .lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .remove(&id);
                self.send_cancel(id);
                Err("backend response timeout")
            }
            Err(RecvTimeoutError::Disconnected) => Err("backend transport closed"),
        }
    }

    fn send_cancel(&self, id: u64) {
        if self.dead.load(Ordering::SeqCst) {
            return;
        }
        let header = match serde_json::to_vec(&CancelHeader { id: 0, cancel: id }) {
            Ok(header) => header,
            Err(_) => return,
        };
        let mut guard = self.stdin.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(stdin) = guard.as_mut() {
            let _ = write_frame(stdin, &header, &[]);
        }
    }

    fn close(&self) {
        self.dead.store(true, Ordering::SeqCst);
        self.pending
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .clear();
        let (lock, cvar) = &self.ready;
        *lock.lock().unwrap_or_else(|e| e.into_inner()) = true;
        cvar.notify_all();
        // A worker may be stuck in a pipe write while holding stdin. Exit must
        // continue to the child-kill backstop instead of waiting on that mutex.
        if let Ok(mut stdin) = self.stdin.try_lock() {
            *stdin = None;
        }
    }
}

// Writes one frame: MAGIC | headerLen(u32 LE) | bodyLen(u32 LE) | header | body.
fn write_frame<W: Write>(w: &mut W, header: &[u8], body: &[u8]) -> io::Result<()> {
    let (header_len, body_len) =
        validate_frame_lengths(header.len(), body.len(), MAX_REQUEST_BODY_SIZE)?;
    w.write_all(MAGIC)?;
    w.write_all(&header_len.to_le_bytes())?;
    w.write_all(&body_len.to_le_bytes())?;
    w.write_all(header)?;
    w.write_all(body)?;
    w.flush()
}

fn validate_frame_lengths(
    header_len: usize,
    body_len: usize,
    max_body_len: u32,
) -> io::Result<(u32, u32)> {
    let header_len = u32::try_from(header_len).map_err(|_| {
        io::Error::new(
            io::ErrorKind::InvalidInput,
            "frame header length does not fit u32",
        )
    })?;
    let body_len = u32::try_from(body_len).map_err(|_| {
        io::Error::new(
            io::ErrorKind::InvalidInput,
            "frame body length does not fit u32",
        )
    })?;
    if header_len > MAX_FRAME_HEADER_SIZE {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "frame header exceeds size limit",
        ));
    }
    if body_len > max_body_len {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "frame body exceeds size limit",
        ));
    }
    Ok((header_len, body_len))
}

// Reads one frame, validating the magic. Returns (headerJSON, body).
fn read_frame<R: Read>(r: &mut R) -> io::Result<(Vec<u8>, Vec<u8>)> {
    let mut magic = [0u8; 4];
    r.read_exact(&mut magic)?;
    if &magic != MAGIC {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "bad frame magic",
        ));
    }
    let mut len = [0u8; 4];
    r.read_exact(&mut len)?;
    let header_len = u32::from_le_bytes(len);
    r.read_exact(&mut len)?;
    let body_len = u32::from_le_bytes(len);
    if header_len > MAX_FRAME_HEADER_SIZE {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "frame header exceeds size limit",
        ));
    }
    if body_len > MAX_FRAME_BODY_SIZE {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "frame body exceeds size limit",
        ));
    }

    let mut header = vec![0u8; header_len as usize];
    r.read_exact(&mut header)?;
    let mut body = vec![0u8; body_len as usize];
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
                    Err(_) => {
                        // The frame itself was read correctly (header_len/body_len were consumed
                        // exactly), so the stream is still in sync — this is a malformed *header*,
                        // not a framing desync, and breaking would needlessly fail every other
                        // in-flight request. Instead, if we can still recover the id, fail just
                        // that one waiter fast with a 502 rather than leaving it to park the full
                        // (up to 1800s) response timeout. If even the id is unrecoverable there is
                        // no waiter we can target, so skip the frame and keep the stream in sync.
                        if let Ok(RespId { id }) = serde_json::from_slice::<RespId>(&header) {
                            if id != 0 {
                                if let Some(tx) = inner
                                    .pending
                                    .lock()
                                    .unwrap_or_else(|e| e.into_inner())
                                    .remove(&id)
                                {
                                    let _ = tx.send(IpcResp {
                                        status: 502,
                                        headers: HashMap::new(),
                                        body: b"malformed backend response header".to_vec(),
                                    });
                                }
                            }
                        }
                        continue;
                    }
                };
                if h.id == 0 {
                    // Ready control frame: open the gate.
                    let (lock, cvar) = &inner.ready;
                    *lock.lock().unwrap_or_else(|e| e.into_inner()) = true;
                    cvar.notify_all();
                    continue;
                }
                if let Some(tx) = inner
                    .pending
                    .lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .remove(&h.id)
                {
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
    inner.close();
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

fn wait_for_child(child: &mut Child, timeout: Duration) -> bool {
    let deadline = std::time::Instant::now() + timeout;
    loop {
        match child.try_wait() {
            Ok(Some(_)) => return true,
            Ok(None) if std::time::Instant::now() < deadline => {
                std::thread::sleep(Duration::from_millis(50));
            }
            Ok(None) | Err(_) => return false,
        }
    }
}

#[cfg(unix)]
extern "C" {
    #[link_name = "kill"]
    fn kill_process_group(pid: i32, signal: i32) -> i32;
    #[link_name = "setsid"]
    fn create_process_session() -> i32;
    #[link_name = "getsid"]
    fn get_process_session(pid: i32) -> i32;
}

#[cfg(unix)]
fn kill_sidecar_session(session_id: u32) {
    let output = StdCommand::new("/bin/ps")
        .args(["-ax", "-o", "pid="])
        .output();
    if let Ok(output) = output {
        let listing = String::from_utf8_lossy(&output.stdout);
        for line in listing.lines() {
            if let Ok(pid) = line.trim().parse::<i32>() {
                let belongs_to_session =
                    unsafe { get_process_session(pid) } == i32::try_from(session_id).unwrap_or(-1);
                if belongs_to_session {
                    unsafe {
                        let _ = kill_process_group(pid, 9);
                    }
                }
            }
        }
    }
}

#[cfg(unix)]
fn force_kill_sidecar(child: &mut Child) {
    let session_id = child.id();
    kill_sidecar_session(session_id);
    if let Ok(pid) = i32::try_from(session_id) {
        unsafe {
            let _ = kill_process_group(-pid, 9);
        }
    }
    let _ = child.kill();
}

#[cfg(not(unix))]
fn force_kill_sidecar(child: &mut Child) {
    let _ = child.kill();
}

// Builds an http::Response carrying `body`, copying `extra_headers` (minus any
// CORS / content-length the backend set) and ALWAYS appending the three CORS
// allow-* headers so the custom-scheme origin can read the response.
fn cors_response(
    status: u16,
    body: Vec<u8>,
    extra_headers: &HashMap<String, String>,
) -> Response<Vec<u8>> {
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
        if let (Ok(name), Ok(val)) = (
            HeaderName::from_bytes(k.as_bytes()),
            HeaderValue::from_str(v),
        ) {
            h.append(name, val);
        }
    }
    h.insert(ACCESS_CONTROL_ALLOW_ORIGIN, HeaderValue::from_static("*"));
    h.insert(ACCESS_CONTROL_ALLOW_HEADERS, HeaderValue::from_static("*"));
    h.insert(ACCESS_CONTROL_ALLOW_METHODS, HeaderValue::from_static("*"));
    resp
}

// Reports a fatal startup failure to the user via a native error dialog, then exits
// once they dismiss it — instead of a bare panic that flash-crashes in release with
// no window and no visible, diagnosable message. The backend is mandatory in release
// (there is nothing to degrade to), so we surface the cause and quit cleanly. Uses
// the non-blocking `show` with an exit callback rather than `blocking_show`: this runs
// on the main thread during setup, before the event loop starts, where a blocking
// dialog would deadlock waiting on a loop that is not yet pumping.
fn report_fatal_startup(app: &tauri::AppHandle, detail: String) {
    use tauri_plugin_dialog::{DialogExt, MessageDialogKind};
    eprintln!("[startup] fatal: {detail}");
    app.dialog()
        .message(detail)
        .title("SekaiText")
        .kind(MessageDialogKind::Error)
        .show(|_| std::process::exit(1));
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
    .invoke_handler(tauri::generate_handler![save_text_dialog, quit_app])
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
        let resource_dir = match app.path().resource_dir() {
          Ok(d) => d,
          Err(e) => {
            report_fatal_startup(
              app.handle(),
              format!("Failed to resolve the resource directory: {e}"),
            );
            return Ok(());
          }
        };
        let data_dir = match app.path().app_local_data_dir() {
          Ok(d) => d,
          Err(e) => {
            report_fatal_startup(
              app.handle(),
              format!("Failed to resolve the app data directory: {e}"),
            );
            return Ok(());
          }
        };

        let exe = match std::env::current_exe() {
          Ok(p) => p,
          Err(e) => {
            report_fatal_startup(
              app.handle(),
              format!("Failed to determine the executable path: {e}"),
            );
            return Ok(());
          }
        };
        let exe_dir = match exe.parent() {
          Some(d) => d.to_path_buf(),
          None => {
            report_fatal_startup(
              app.handle(),
              format!(
                "Failed to determine the executable directory (no parent): {}",
                exe.display()
              ),
            );
            return Ok(());
          }
        };

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
        cmd.creation_flags(CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP);

        #[cfg(unix)]
        unsafe {
          cmd.pre_exec(|| {
            if create_process_session() < 0 {
              return Err(io::Error::last_os_error());
            }
            Ok(())
          });
        }

        let mut child = match cmd.spawn() {
          Ok(c) => c,
          Err(e) => {
            report_fatal_startup(
              app.handle(),
              format!(
                "Failed to start the SekaiText backend.\n\nPath: {}\nError: {e}\n\nThe backend executable may be missing, removed by security software, lacking execute permission, or quarantined by the OS. Please reinstall SekaiText.",
                sidecar_path.display()
              ),
            );
            return Ok(());
          }
        };

        #[cfg(target_os = "windows")]
        let sidecar_job = match windows_job::Job::assign(&child) {
          Ok(job) => Some(job),
          Err(e) => {
            let _ = child.kill();
            let _ = child.wait();
            report_fatal_startup(
              app.handle(),
              format!("Failed to contain the SekaiText backend process tree.\n\nError: {e}\n\nPlease restart or reinstall SekaiText."),
            );
            return Ok(());
          }
        };

        let child_stdin = child.stdin.take().expect("sidecar stdin");
        let child_stdout = child.stdout.take().expect("sidecar stdout");
        let child_stderr = child.stderr.take().expect("sidecar stderr");

        let inner = Arc::new(IpcInner {
          stdin: Mutex::new(Some(child_stdin)),
          next_id: AtomicU64::new(1),
          pending: Mutex::new(HashMap::new()),
          ready: (Mutex::new(false), Condvar::new()),
          dead: AtomicBool::new(false),
          in_flight: AtomicUsize::new(0),
        });

        {
          let inner = inner.clone();
          std::thread::spawn(move || reader_loop(inner, child_stdout));
        }
        std::thread::spawn(move || drain_stderr(child_stderr));

        app.manage(Ipc(inner));

        // Reap the backend child so it never lingers as a <defunct> zombie. std's
        // Child neither reaps on Drop nor lets the reader thread (which only owns
        // stdout) collect it, so if the backend exits on its own mid-session (e.g. it
        // panics) nothing would reap it until app teardown. This reaper polls try_wait
        // (a short lock + non-blocking check, sleeping OUTSIDE the lock) so the
        // RunEvent::Exit handler can still lock the same handle to kill + wait on a
        // normal quit; whichever side reaps first clears the handle and the other
        // becomes a no-op.
        let child = Arc::new(Mutex::new(Some(child)));
        #[cfg(target_os = "windows")]
        let job = Arc::new(Mutex::new(sidecar_job));
        {
          let child = child.clone();
          #[cfg(target_os = "windows")]
          let job = job.clone();
          std::thread::spawn(move || loop {
            std::thread::sleep(Duration::from_secs(2));
            let mut guard = child.lock().unwrap_or_else(|e| e.into_inner());
            // Take an owned poll result so the `&mut Child` borrow ends before we
            // touch `guard` again below.
            match guard.as_mut().map(|c| c.try_wait()) {
              Some(Ok(Some(_))) => {
                #[cfg(unix)]
                if let Some(exited) = guard.as_ref() {
                  kill_sidecar_session(exited.id());
                }
                *guard = None; // exited on its own; try_wait already reaped it
                #[cfg(target_os = "windows")]
                {
                  *job.lock().unwrap_or_else(|e| e.into_inner()) = None;
                }
                break;
              }
              Some(Ok(None)) => {} // still running
              Some(Err(_)) => break, // can't poll it (already reaped / OS error)
              None => break, // Exit handler already took and reaped it
            }
          });
        }
        app.manage(SidecarProcess {
          child,
          #[cfg(target_os = "windows")]
          job,
        });
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
        builder = builder.register_asynchronous_uri_scheme_protocol(
            "sekai",
            |ctx, request, responder| {
                // OPTIONS preflight is short-circuited in Rust — never round-trips to Go.
                if *request.method() == Method::OPTIONS {
                    responder.respond(cors_response(204, Vec::new(), &HashMap::new()));
                    return;
                }

                let method = request.method().as_str().to_string();
                let path = request.uri().path().to_string();
                let query = request.uri().query().unwrap_or("").to_string();
                if request.body().len() > MAX_REQUEST_BODY_SIZE as usize {
                    responder.respond(cors_response(
                        413,
                        b"request body exceeds IPC limit".to_vec(),
                        &HashMap::new(),
                    ));
                    return;
                }
                let mut metadata_size = method.len() + path.len() + query.len();
                let mut headers = HashMap::new();
                for (k, v) in request.headers() {
                    if let Ok(s) = v.to_str() {
                        metadata_size = metadata_size
                            .saturating_add(k.as_str().len())
                            .saturating_add(s.len());
                        if metadata_size > MAX_FRAME_HEADER_SIZE as usize {
                            responder.respond(cors_response(
                                431,
                                b"request headers exceed IPC limit".to_vec(),
                                &HashMap::new(),
                            ));
                            return;
                        }
                        headers.insert(k.as_str().to_string(), s.to_string());
                    }
                }
                let inner = match ctx.app_handle().try_state::<Ipc>() {
                    Some(s) => s.0.clone(),
                    None => {
                        responder.respond(cors_response(
                            503,
                            b"backend not initialized".to_vec(),
                            &HashMap::new(),
                        ));
                        return;
                    }
                };
                let permit = match inner.try_acquire() {
                    Some(permit) => permit,
                    None => {
                        responder.respond(cors_response(
                            503,
                            IPC_OVERLOADED.as_bytes().to_vec(),
                            &HashMap::new(),
                        ));
                        return;
                    }
                };
                let body = request.body().clone();

                // Never block the main/IPC thread: do the stdio round-trip on a worker.
                std::thread::spawn(move || {
                    let _permit = permit;
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
                        Ok(resp) => {
                            responder.respond(cors_response(resp.status, resp.body, &resp.headers))
                        }
                        Err(e) => {
                            let status = if e == IPC_OVERLOADED {
                                503
                            } else if e == IPC_REQUEST_TOO_LARGE {
                                413
                            } else {
                                502
                            };
                            responder.respond(cors_response(
                                status,
                                format!("ipc error: {e}").into_bytes(),
                                &HashMap::new(),
                            ))
                        }
                    }
                });
            },
        );
    }

    builder
        .build(ctx)
        .expect("error while building tauri application")
        .run(|app, event| {
            // RunEvent::Exit is the single terminal event that fires on EVERY quit path
            // (window close, Cmd-Q, Dock quit, Apple-event quit). On macOS an Apple-event
            // quit emits ONLY Exit (no ExitRequested, no per-window Destroyed) — which
            // also means those quit paths NEVER show the frontend's unsaved-changes
            // dialog. Recovery must therefore NOT be cleared here: with unsaved edits,
            // Cmd-Q would silently destroy both the edits and their only autosave backup.
            // The frontend clears recovery at the moments it is truly obsolete (after a
            // successful save, and when the user discards a restore); a leftover file
            // just re-offers recovery on next launch. Exit closes backend stdin first so
            // Go can stop its engines, then force-kills only if that grace period expires.
            #[cfg(not(debug_assertions))]
            if let tauri::RunEvent::Exit = event {
                if let Some(ipc) = app.try_state::<Ipc>() {
                    ipc.0.close();
                }
                if let Some(state) = app.try_state::<SidecarProcess>() {
                    #[cfg(target_os = "windows")]
                    let mut job = state.job.lock().unwrap_or_else(|e| e.into_inner()).take();
                    if let Some(mut child) =
                        state.child.lock().unwrap_or_else(|e| e.into_inner()).take()
                    {
                        if !wait_for_child(&mut child, Duration::from_secs(15)) {
                            // Closing the Windows Job kills the backend and every descendant.
                            #[cfg(target_os = "windows")]
                            drop(job.take());
                            force_kill_sidecar(&mut child);
                        }
                        let _ = child.wait();
                    }
                }
            }
            #[cfg(debug_assertions)]
            let _ = (app, event);
        });
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Cursor;

    #[test]
    fn frame_round_trip() {
        for (header, body) in [
            (br#"{"id":0,"status":204}"#.as_slice(), &[][..]),
            (
                br#"{"id":7,"status":200}"#.as_slice(),
                &[0, 1, b'S', b'K', b'F', b'1', 255][..],
            ),
        ] {
            let mut encoded = Vec::new();
            write_frame(&mut encoded, header, body).unwrap();
            let (got_header, got_body) = read_frame(&mut Cursor::new(encoded)).unwrap();
            assert_eq!(got_header, header);
            assert_eq!(got_body, body);
        }
    }

    #[test]
    fn read_frame_rejects_oversize_before_allocating_payload() {
        for (header_len, body_len) in [(MAX_FRAME_HEADER_SIZE + 1, 0), (0, MAX_FRAME_BODY_SIZE + 1)]
        {
            let mut encoded = Vec::new();
            encoded.extend_from_slice(MAGIC);
            encoded.extend_from_slice(&header_len.to_le_bytes());
            encoded.extend_from_slice(&body_len.to_le_bytes());
            let err = read_frame(&mut Cursor::new(encoded)).unwrap_err();
            assert_eq!(err.kind(), io::ErrorKind::InvalidData);
        }
    }

    #[test]
    fn write_frame_rejects_protocol_limits() {
        assert!(validate_frame_lengths(
            MAX_FRAME_HEADER_SIZE as usize + 1,
            0,
            MAX_REQUEST_BODY_SIZE
        )
        .is_err());
        assert!(validate_frame_lengths(
            0,
            MAX_REQUEST_BODY_SIZE as usize + 1,
            MAX_REQUEST_BODY_SIZE
        )
        .is_err());
    }

    #[cfg(target_pointer_width = "64")]
    #[test]
    fn write_frame_rejects_usize_to_u32_truncation() {
        let err =
            validate_frame_lengths(u32::MAX as usize + 1, 0, MAX_REQUEST_BODY_SIZE).unwrap_err();
        assert!(err.to_string().contains("does not fit u32"));
    }
}
