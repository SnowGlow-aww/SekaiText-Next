use serde::{Deserialize, Serialize};
use tauri::{
    plugin::{Builder, PluginHandle},
    Manager,
};

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct MobileBootstrap {
    platform: &'static str,
    backend: &'static str,
    live2d: bool,
    glossary: bool,
    team: bool,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct NativeJsonResponse {
    json: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct NativeJsonRequest<'a> {
    request_json: &'a str,
}

#[derive(Serialize)]
struct NativeContentRequest<'a> {
    content: &'a str,
}

#[derive(Deserialize)]
struct NativeJsonResult {
    json: String,
}

struct MobileCore(PluginHandle<tauri::Wry>);

/// Reports the shell contract. Editor core availability is intentionally
/// separate so the UI can distinguish native-bridge failures from app startup.
#[tauri::command]
fn mobile_bootstrap() -> MobileBootstrap {
    MobileBootstrap {
        platform: "android",
        backend: "embedded-go",
        live2d: true,
        glossary: true,
        team: true,
    }
}

#[tauri::command]
fn mobile_core_bootstrap(core: tauri::State<'_, MobileCore>) -> Result<NativeJsonResponse, String> {
    run_mobile_json(&core, "bootstrap", ())
}

#[tauri::command]
fn mobile_open_url(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "openUrl", &request_json)
}

#[tauri::command]
fn mobile_story_types(core: tauri::State<'_, MobileCore>) -> Result<NativeJsonResponse, String> {
    run_mobile_json(&core, "storyTypes", ())
}

#[tauri::command]
fn mobile_story_sorts(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "storySorts",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_story_index(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "storyIndex",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_story_chapters(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "storyChapters",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_story_json_path(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "storyJsonPath",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_story_load(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "storyLoad",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_story_load_local(
    core: tauri::State<'_, MobileCore>,
    content: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "storyLoadLocal",
        NativeContentRequest { content: &content },
    )
}

#[tauri::command]
fn mobile_voice_url(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "voiceUrl",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_resolve_live2d_asset(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "resolveLive2DAsset",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_resolve_story_label(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "resolveStoryLabel",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_story_catalog_status(
    core: tauri::State<'_, MobileCore>,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(&core, "storyCatalogStatus", ())
}

#[tauri::command]
fn mobile_update_story_catalog(
    core: tauri::State<'_, MobileCore>,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(&core, "updateStoryCatalog", ())
}

#[tauri::command]
fn mobile_story_update_progress(
    core: tauri::State<'_, MobileCore>,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(&core, "storyUpdateProgress", ())
}

#[tauri::command]
fn mobile_create_translation(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "createTranslation",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_load_translation(
    core: tauri::State<'_, MobileCore>,
    content: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "loadTranslation",
        NativeContentRequest { content: &content },
    )
}

#[tauri::command]
fn mobile_serialize_translation(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "serializeTranslation",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_check_lines(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "checkLines",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_compare_text(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "compareText",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_change_text(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "changeText",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_add_line(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "addLine",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_remove_line(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "removeLine",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_replace_brackets(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "replaceBrackets",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_speaker_count(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "speakerCount",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_check_text(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "checkText",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_glossary_search(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "glossarySearch",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_glossary_categories(
    core: tauri::State<'_, MobileCore>,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(&core, "glossaryCategories", ())
}

#[tauri::command]
fn mobile_glossary_entries(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "glossaryEntries",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_glossary_add_entry(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "glossaryAddEntry",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_glossary_update_entry(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "glossaryUpdateEntry",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_glossary_delete_entry(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "glossaryDeleteEntry",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_glossary_appellation_speakers(
    core: tauri::State<'_, MobileCore>,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(&core, "glossaryAppellationSpeakers", ())
}

#[tauri::command]
fn mobile_glossary_appellation_targets(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "glossaryAppellationTargets",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_glossary_appellation_lookup(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "glossaryAppellationLookup",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_glossary_appellation_upsert(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "glossaryAppellationUpsert",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_glossary_grammar(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(
        &core,
        "glossaryGrammar",
        NativeJsonRequest {
            request_json: &request_json,
        },
    )
}

#[tauri::command]
fn mobile_glossary_export(
    core: tauri::State<'_, MobileCore>,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(&core, "glossaryExport", ())
}

#[tauri::command]
fn mobile_team_status(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamStatus", &request_json)
}

#[tauri::command]
fn mobile_team_login(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamLogin", &request_json)
}

#[tauri::command]
fn mobile_team_logout(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamLogout", &request_json)
}

#[tauri::command]
fn mobile_team_connect(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamConnect", &request_json)
}

#[tauri::command]
fn mobile_team_disconnect(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamDisconnect", &request_json)
}

#[tauri::command]
fn mobile_team_sync(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamSync", &request_json)
}

#[tauri::command]
fn mobile_team_create_proposal(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamCreateProposal", &request_json)
}

#[tauri::command]
fn mobile_team_my_proposals(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamMyProposals", &request_json)
}

#[tauri::command]
fn mobile_team_withdraw_proposal(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamWithdrawProposal", &request_json)
}

#[tauri::command]
fn mobile_team_pending_proposals(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamPendingProposals", &request_json)
}

#[tauri::command]
fn mobile_team_approve_proposal(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamApproveProposal", &request_json)
}

#[tauri::command]
fn mobile_team_reject_proposal(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamRejectProposal", &request_json)
}

#[tauri::command]
fn mobile_team_set_reviewer(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamSetReviewer", &request_json)
}

#[tauri::command]
fn mobile_team_list_users(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamListUsers", &request_json)
}

#[tauri::command]
fn mobile_team_change_password(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamChangePassword", &request_json)
}

#[tauri::command]
fn mobile_team_update_profile(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamUpdateProfile", &request_json)
}

#[tauri::command]
fn mobile_team_account_users(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamAccountUsers", &request_json)
}

#[tauri::command]
fn mobile_team_create_user(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamCreateUser", &request_json)
}

#[tauri::command]
fn mobile_team_set_user_role(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamSetUserRole", &request_json)
}

#[tauri::command]
fn mobile_team_set_user_status(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamSetUserStatus", &request_json)
}

#[tauri::command]
fn mobile_team_reset_user_password(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamResetUserPassword", &request_json)
}

#[tauri::command]
fn mobile_team_delete_user(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamDeleteUser", &request_json)
}

#[tauri::command]
fn mobile_team_glossary_replace(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamGlossaryReplace", &request_json)
}

#[tauri::command]
fn mobile_team_bulk_import(
    core: tauri::State<'_, MobileCore>,
    request_json: String,
) -> Result<NativeJsonResponse, String> {
    run_mobile_request_json(&core, "teamBulkImport", &request_json)
}

fn run_mobile_request_json(
    core: &MobileCore,
    command: &str,
    request_json: &str,
) -> Result<NativeJsonResponse, String> {
    run_mobile_json(core, command, NativeJsonRequest { request_json })
}

fn run_mobile_json<T: Serialize>(
    core: &MobileCore,
    command: &str,
    payload: T,
) -> Result<NativeJsonResponse, String> {
    core.0
        .run_mobile_plugin::<NativeJsonResult>(command, payload)
        .map(|result| NativeJsonResponse { json: result.json })
        .map_err(|error| error.to_string())
}

#[cfg_attr(target_os = "android", tauri::mobile_entry_point)]
pub fn run() {
    let platform_plugin = Builder::<tauri::Wry>::new("sekai-platform")
        .js_init_script("window.__SEKAI_PLATFORM__='android';window.__SEKAI_ORIGIN__='';")
        .setup(|app, api| {
            let handle =
                api.register_android_plugin("com.sekaitext.mobile", "SekaiMobilePlugin")?;
            app.manage(MobileCore(handle));
            Ok(())
        })
        .build();

    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(platform_plugin)
        .invoke_handler(tauri::generate_handler![
            mobile_bootstrap,
            mobile_core_bootstrap,
            mobile_open_url,
            mobile_story_types,
            mobile_story_sorts,
            mobile_story_index,
            mobile_story_chapters,
            mobile_story_json_path,
            mobile_story_load,
            mobile_story_load_local,
            mobile_voice_url,
            mobile_resolve_live2d_asset,
            mobile_resolve_story_label,
            mobile_story_catalog_status,
            mobile_update_story_catalog,
            mobile_story_update_progress,
            mobile_create_translation,
            mobile_load_translation,
            mobile_serialize_translation,
            mobile_check_lines,
            mobile_compare_text,
            mobile_change_text,
            mobile_add_line,
            mobile_remove_line,
            mobile_replace_brackets,
            mobile_speaker_count,
            mobile_check_text,
            mobile_glossary_search,
            mobile_glossary_categories,
            mobile_glossary_entries,
            mobile_glossary_add_entry,
            mobile_glossary_update_entry,
            mobile_glossary_delete_entry,
            mobile_glossary_appellation_speakers,
            mobile_glossary_appellation_targets,
            mobile_glossary_appellation_lookup,
            mobile_glossary_appellation_upsert,
            mobile_glossary_grammar,
            mobile_glossary_export,
            mobile_team_status,
            mobile_team_login,
            mobile_team_logout,
            mobile_team_connect,
            mobile_team_disconnect,
            mobile_team_sync,
            mobile_team_create_proposal,
            mobile_team_my_proposals,
            mobile_team_withdraw_proposal,
            mobile_team_pending_proposals,
            mobile_team_approve_proposal,
            mobile_team_reject_proposal,
            mobile_team_set_reviewer,
            mobile_team_list_users,
            mobile_team_change_password,
            mobile_team_update_profile,
            mobile_team_account_users,
            mobile_team_create_user,
            mobile_team_set_user_role,
            mobile_team_set_user_status,
            mobile_team_reset_user_password,
            mobile_team_delete_user,
            mobile_team_glossary_replace,
            mobile_team_bulk_import,
        ])
        .run(tauri::generate_context!())
        .expect("error while running SekaiText Android");
}
