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

#[derive(serde::Deserialize, serde::Serialize)]
struct CandidateReference {
    id: String,
    expected_version: i64,
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
fn desktop_core_state_snapshot(
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    process
        .request(&sidecar, "core.state_snapshot", serde_json::json!({}))
        .map_err(str::to_owned)
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
    kind: Option<String>,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    if content.trim().is_empty() {
        return Err("content is required".to_owned());
    }
    let kind = import_kind(kind)?;
    let key = format!(
        "gui-import-{}",
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| "clock unavailable".to_owned())?
            .as_nanos()
    );
    process.request_with_idempotency(&sidecar, "import.create", serde_json::json!({"kind":kind,"content":content,"display_name":display_name.unwrap_or_default()}), Some(&key)).map_err(str::to_owned)
}

fn import_kind(kind: Option<String>) -> Result<String, String> {
    let kind = kind.unwrap_or_else(|| "text".to_owned());
    match kind.as_str() {
        "text" | "markdown" => Ok(kind),
        _ => Err("invalid import kind".to_owned()),
    }
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
fn desktop_split_candidate(
    candidate_id: String,
    expected_version: i64,
    parts: Vec<String>,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    let parts = split_parts(parts)?;
    process
        .request_with_idempotency(
            &sidecar,
            "candidate.split",
            serde_json::json!({"id":candidate_id,"expected_version":expected_version,"parts":parts}),
            Some(&gateway_key("gui-candidate-split")?),
        )
        .map_err(str::to_owned)
}

fn split_parts(parts: Vec<String>) -> Result<Vec<String>, String> {
    let parts: Vec<_> = parts
        .into_iter()
        .map(|part| part.trim().to_owned())
        .filter(|part| !part.is_empty())
        .collect();
    if parts.len() < 2 {
        return Err("at least two non-empty candidate parts are required".to_owned());
    }
    Ok(parts)
}

#[tauri::command]
fn desktop_merge_candidates(
    candidates: Vec<CandidateReference>,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    if candidates.len() < 2
        || candidates
            .iter()
            .any(|candidate| candidate.id.trim().is_empty() || candidate.expected_version < 1)
    {
        return Err("at least two versioned candidates are required".to_owned());
    }
    process
        .request_with_idempotency(
            &sidecar,
            "candidate.merge",
            serde_json::json!({"candidates":candidates}),
            Some(&gateway_key("gui-candidate-merge")?),
        )
        .map_err(str::to_owned)
}

#[tauri::command]
fn desktop_query(
    question: String,
    mode: Option<String>,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    if question.trim().is_empty() {
        return Err("question is required".to_owned());
    }
    let mode = query_mode(mode)?;
    process
        .request_with_idempotency(
            &sidecar,
            "query.start",
            serde_json::json!({"question":question,"mode":mode,"profile_version":"local_v1"}),
            Some(&gateway_key("gui-query")?),
        )
        .map_err(str::to_owned)
}

#[tauri::command]
fn desktop_knowledge_source(
    knowledge_id: String,
    sidecar: tauri::State<'_, sidecar::SidecarLaunch>,
    process: tauri::State<'_, sidecar::CoreProcess>,
) -> Result<serde_json::Value, String> {
    process
        .request(
            &sidecar,
            "knowledge.source",
            serde_json::json!({"knowledge_id":knowledge_id}),
        )
        .map_err(str::to_owned)
}

fn query_mode(mode: Option<String>) -> Result<String, String> {
    let mode = mode.unwrap_or_else(|| "strict".to_owned());
    match mode.as_str() {
        "strict" | "augment" | "clarify" => Ok(mode),
        _ => Err("invalid query mode".to_owned()),
    }
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
            desktop_core_state_snapshot,
            desktop_knowledge_list,
            desktop_pending_candidate_list,
            desktop_import_text,
            desktop_promote_candidate,
            desktop_reject_candidate,
            desktop_update_candidate,
            desktop_split_candidate,
            desktop_merge_candidates,
            desktop_query,
            desktop_knowledge_source,
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
            let executable_dir = app.path().executable_dir()?;
            let sidecar = sidecar::SidecarLaunch::new(
                resource_dir,
                executable_dir,
                data_root.as_path().to_path_buf(),
            )
            .map_err(std::io::Error::other)?;
            app.manage(data_root);
            app.manage(sidecar);
            app.manage(sidecar::CoreProcess::default());
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running desktop knowledge companion");
}

#[cfg(test)]
mod tests {
    use super::query_mode;

    #[test]
    fn query_mode_defaults_and_rejects_unknown_values() {
        assert_eq!(query_mode(None).as_deref(), Ok("strict"));
        assert_eq!(
            query_mode(Some("clarify".to_owned())).as_deref(),
            Ok("clarify")
        );
        assert!(query_mode(Some("unknown".to_owned())).is_err());
    }

    #[test]
    fn import_kind_defaults_and_rejects_unknown_values() {
        assert_eq!(super::import_kind(None).as_deref(), Ok("text"));
        assert_eq!(
            super::import_kind(Some("markdown".to_owned())).as_deref(),
            Ok("markdown")
        );
        assert!(super::import_kind(Some("pdf".to_owned())).is_err());
    }

    #[test]
    fn split_parts_rejects_empty_segments() {
        assert_eq!(
            super::split_parts(vec![" first ".to_owned(), "second".to_owned()]),
            Ok(vec!["first".to_owned(), "second".to_owned()])
        );
        assert!(super::split_parts(vec!["one".to_owned(), "  ".to_owned()]).is_err());
    }
}
