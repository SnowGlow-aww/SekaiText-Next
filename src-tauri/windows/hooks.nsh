; ============================================================================
;  SekaiText Next - NSIS installer hooks
;  Wired via tauri.conf.json -> bundle.windows.nsis.installerHooks
;
;  Purpose: fix "Error opening file for writing: sekaitext-backend.exe".
;  When the updater/installer runs over a live install, the Go backend
;  sidecar (sekaitext-backend.exe, holds port 9800) and the main Tauri host
;  (SekaiText Next.exe) keep their .exe files open, so NSIS cannot overwrite
;  them. NSIS_HOOK_PREINSTALL runs BEFORE any files are copied or registry
;  keys are written -- the only safe window to terminate those processes.
;
;  Tauri inserts each hook macro independently (the ones you do not define are
;  guarded out), so defining just NSIS_HOOK_PREINSTALL here is sufficient and
;  compiles cleanly.
; ============================================================================

!macro NSIS_HOOK_PREINSTALL
  ; --- Kill the Go backend sidecar -------------------------------------------
  ; nsExec::Exec runs taskkill with NO console window and NEVER aborts the
  ; installer. If the target is not running, taskkill returns a non-zero exit
  ; code (e.g. 128 = "process not found") which we Pop and discard.
  ; /F = force, /T = also terminate child processes, /IM = image name.
  ; Using the full $SYSDIR path avoids any PATH-resolution failure.
  nsExec::Exec '"$SYSDIR\taskkill.exe" /F /T /IM "sekaitext-backend.exe"'
  Pop $0  ; discard exit code (0 = killed, 128 = not running)

  ; --- Kill the main Tauri host app ------------------------------------------
  ; Image name is derived from productName ("SekaiText Next") and contains a
  ; space, so it MUST stay quoted. This does NOT kill the running installer,
  ; whose image name is the setup/updater exe, not "SekaiText Next.exe".
  nsExec::Exec '"$SYSDIR\taskkill.exe" /F /T /IM "SekaiText Next.exe"'
  Pop $0  ; discard exit code (0 = killed, 128 = not running)

  ; --- Let Windows release the file handles ----------------------------------
  ; taskkill returns as soon as termination is signalled; give the OS a brief
  ; moment to actually close the handles before NSIS begins writing files.
  Sleep 1500
!macroend
