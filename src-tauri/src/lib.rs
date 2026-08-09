mod data_root;
mod sidecar;

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

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .invoke_handler(tauri::generate_handler![desktop_core_status])
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
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running desktop knowledge companion");
}
