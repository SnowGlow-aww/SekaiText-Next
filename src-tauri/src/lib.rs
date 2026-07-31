#[cfg(desktop)]
mod desktop;

// The current embedded Go bridge and native plugin are Android-specific. Tauri's
// broader `mobile` cfg also includes iOS, where these APIs and dependencies do
// not exist, so keep the module gated to its actual supported target.
#[cfg(target_os = "android")]
mod mobile;

#[cfg(desktop)]
pub use desktop::run;

#[cfg(target_os = "android")]
pub use mobile::run;

#[cfg(target_os = "ios")]
compile_error!("SekaiText Next's mobile shell currently supports Android only");
