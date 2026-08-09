mod data_root;
mod sidecar;

use std::time::{SystemTime, UNIX_EPOCH};
use tauri::Manager;

#[derive(serde::Serialize)]
struct DesktopCoreStatus {
    data_dir: String,
    sidecar_path: String,
    ready: bool,
}

#[tauri::command]
fn desktop_core_status(
    data_root: tauri::State<'_, data_root::DefaultDataRoot>,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
) -> DesktopCoreStatus {
    DesktopCoreStatus {
        data_dir: data_root.as_path().display().to_string(),
        sidecar_path: sidecar.executable().display().to_string(),
        ready: sidecar.executable().is_file(),
    }
}

#[tauri::command]
fn desktop_core_start(
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    process.health(&sidecar).map_err(str::to_owned)
}

#[tauri::command]
fn desktop_knowledge_list(
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    process
        .request(&sidecar, "knowledge.list", serde_json::json!({}))
        .map_err(str::to_owned)
}

#[tauri::command]
fn desktop_pending_candidate_list(
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    process
        .request(&sidecar, "candidate.pending.list", serde_json::json!({}))
        .map_err(str::to_owned)
}

#[tauri::command]
fn desktop_import_text(
    content: String,
    display_name: Option<String>,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    if content.trim().is_empty() {
        return Err("content is required".to_owned());
    }
    let key = format!(
        "gui-import-{}",
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| "clock unavailable".to_owned())?
            .as_nanos()
    );
    process.request_with_idempotency(&sidecar, "import.create", serde_json::json!({"kind":"text","content":content,"display_name":display_name.unwrap_or_default()}), Some(&key)).map_err(str::to_owned)
}

fn gateway_key(prefix: &str) -> Result<String, String> {
    Ok(format!(
        "{prefix}-{}",
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| "clock unavailable".to_owned())?
            .as_nanos()
    ))
}

#[tauri::command]
fn desktop_promote_candidate(
    candidate_id: String,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    let approval = process
        .request_with_idempotency(
            &sidecar,
            "candidate.request_approval",
            serde_json::json!({"candidate_id":candidate_id}),
            Some(&gateway_key("gui-approval")?),
        )
        .map_err(str::to_owned)?;
    let approval_id = approval["result"]["value"]["id"]
        .as_str()
        .ok_or("invalid approval response".to_owned())?;
    let resolved = process
        .request_with_idempotency(
            &sidecar,
            "approval.resolve",
            serde_json::json!({"approval_id":approval_id,"approve":true}),
            Some(&gateway_key("gui-resolve")?),
        )
        .map_err(str::to_owned)?;
    let token = resolved["result"]["value"]["token"]
        .as_str()
        .ok_or("invalid approval token".to_owned())?;
    process
        .request_with_idempotency(
            &sidecar,
            "candidate.approve",
            serde_json::json!({"candidate_id":candidate_id,"token":token}),
            Some(&gateway_key("gui-promote")?),
        )
        .map_err(str::to_owned)
}

#[tauri::command]
fn desktop_reject_candidate(
    candidate_id: String,
    expected_version: i64,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    process
        .request_with_idempotency(
            &sidecar,
            "candidate.reject",
            serde_json::json!({"id":candidate_id,"expected_version":expected_version}),
            Some(&gateway_key("gui-reject")?),
        )
        .map_err(str::to_owned)
}

#[tauri::command]
fn desktop_update_candidate(
    candidate_id: String,
    expected_version: i64,
    content: String,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    if content.trim().is_empty() {
        return Err("candidate content is required".to_owned());
    }
    process
        .request_with_idempotency(
            &sidecar,
            "candidate.update",
            serde_json::json!({"id":candidate_id,"expected_version":expected_version,"content":content}),
            Some(&gateway_key("gui-candidate-update")?),
        )
        .map_err(str::to_owned)
}

#[tauri::command]
fn desktop_query(
    question: String,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    if question.trim().is_empty() {
        return Err("question is required".to_owned());
    }
    process
        .request_with_idempotency(
            &sidecar,
            "query.start",
            serde_json::json!({"question":question,"mode":"strict","profile_version":"local_v1"}),
            Some(&gateway_key("gui-query")?),
        )
        .map_err(str::to_owned)
}

#[tauri::command]
fn desktop_revise_knowledge(
    knowledge_id: String,
    expected_revision_id: String,
    content: String,
    reason: String,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    if content.trim().is_empty() || reason.trim().is_empty() {
        return Err("revision content and reason are required".to_owned());
    }
    process
        .request_with_idempotency(
            &sidecar,
            "knowledge.revise",
            serde_json::json!({"knowledge_id":knowledge_id,"expected_revision_id":expected_revision_id,"content":content,"reason":reason}),
            Some(&gateway_key("gui-knowledge-revise")?),
        )
        .map_err(str::to_owned)
}

#[tauri::command]
fn desktop_agent_pending_list(
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    process
        .request(&sidecar, "agent.pending.list", serde_json::json!({}))
        .map_err(str::to_owned)
}

#[tauri::command]
fn desktop_suggest_missing_evidence(
    question: String,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    if question.trim().is_empty() {
        return Err("question is required".to_owned());
    }
    process
        .request_with_idempotency(
            &sidecar,
            "agent.prompt.suggest",
            serde_json::json!({"topic":"missing-evidence","detail":format!("问题「{question}」缺少本地证据；可导入相关资料。")}),
            Some(&gateway_key("gui-missing-evidence")?),
        )
        .map_err(str::to_owned)
}

#[tauri::command]
fn desktop_resolve_missing_evidence_prompt(
    pending_id: String,
    action: String,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    let state = match action.as_str() {
        "close" => "closed",
        "ignore" => "ignored",
        "defer" => "deferred",
        _ => return Err("invalid prompt action".to_owned()),
    };
    if action == "ignore" || action == "defer" {
        let preference = if action == "ignore" {
            serde_json::json!({"topic":"missing-evidence","state":"ignored"})
        } else {
            serde_json::json!({"topic":"missing-evidence","state":"deferred","defer_for_seconds":86400})
        };
        process
            .request_with_idempotency(
                &sidecar,
                "agent.prompt.preference.set",
                preference,
                Some(&gateway_key("gui-missing-evidence-preference")?),
            )
            .map_err(str::to_owned)?;
    }
    process
        .request_with_idempotency(
            &sidecar,
            "agent.pending.resolve",
            serde_json::json!({"id":pending_id,"state":state}),
            Some(&gateway_key("gui-missing-evidence-resolve")?),
        )
        .map_err(str::to_owned)
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .invoke_handler(tauri::generate_handler![
            desktop_core_status,
            desktop_core_start,
            desktop_knowledge_list,
            desktop_pending_candidate_list,
            desktop_import_text,
            desktop_promote_candidate,
            desktop_reject_candidate,
            desktop_update_candidate,
            desktop_query,
            desktop_revise_knowledge,
            desktop_agent_pending_list,
            desktop_suggest_missing_evidence,
            desktop_resolve_missing_evidence_prompt
        ])
        .setup(|app| {
            let resolved = app.path().app_local_data_dir()?;
            let data_root =
                data_root::DefaultDataRoot::new(resolved).map_err(std::io::Error::other)?;
            let resource_dir = app.path().resource_dir()?;
            let sidecar =
                sidecar::SidecarLaunch::new(resource_dir, data_root.as_path().to_path_buf())
                    .map_err(std::io::Error::other)?;
            app.manage(data_root);
            app.manage(sidecar);
            app.manage(sidecar::CoreProcess::default());
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running desktop knowledge companion");
}
