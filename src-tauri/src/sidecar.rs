use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::{
    atomic::{AtomicU64, Ordering},
    Mutex,
};
use std::time::{SystemTime, UNIX_EPOCH};

static REQUEST_SEQUENCE: AtomicU64 = AtomicU64::new(0);
const CORE_PROTOCOL_VERSION: u8 = 1;
const CORE_SCHEMA_VERSION: u16 = 6;

#[derive(Debug)]
pub(crate) struct SidecarLaunch {
    executable: PathBuf,
    #[allow(dead_code)]
    data_dir: PathBuf,
}

impl SidecarLaunch {
    pub(crate) fn new(
        resource_dir: PathBuf,
        executable_dir: PathBuf,
        data_dir: PathBuf,
    ) -> Result<Self, &'static str> {
        if !resource_dir.is_absolute() || !executable_dir.is_absolute() || !data_dir.is_absolute() {
            return Err("sidecar resource, executable, and data directories must be absolute");
        }
        validate_manifest(&resource_dir)?;
        let packaged = executable_dir.join(sidecar_file_name());
        let executable = if packaged.is_file() {
            packaged
        } else {
            resource_dir.join("binaries").join(sidecar_file_name())
        };
        Ok(Self {
            executable,
            data_dir,
        })
    }

    #[allow(dead_code)]
    pub(crate) fn command(&self) -> Command {
        let mut command = Command::new(&self.executable);
        command.arg("serve").arg("--data-dir").arg(&self.data_dir);
        command
    }

    #[allow(dead_code)]
    pub(crate) fn executable(&self) -> &Path {
        &self.executable
    }
}

#[derive(serde::Deserialize)]
struct SidecarManifest {
    core_version: String,
    protocol_version: u8,
    schema_version: u16,
    targets: Vec<String>,
}

fn validate_manifest(resource_dir: &Path) -> Result<(), &'static str> {
    let encoded = std::fs::read_to_string(resource_dir.join("sidecar-manifest.json"))
        .map_err(|_| "packaged sidecar manifest is unavailable")?;
    let manifest: SidecarManifest =
        serde_json::from_str(&encoded).map_err(|_| "packaged sidecar manifest is invalid")?;
    if manifest.core_version != env!("CARGO_PKG_VERSION")
        || manifest.protocol_version != CORE_PROTOCOL_VERSION
        || manifest.schema_version != CORE_SCHEMA_VERSION
        || !manifest
            .targets
            .iter()
            .any(|target| target == sidecar_target())
    {
        return Err("packaged sidecar manifest is incompatible");
    }
    Ok(())
}

#[derive(Default)]
pub(crate) struct CoreProcess(Mutex<Option<Child>>);

impl CoreProcess {
    pub(crate) fn start(&self, launch: &SidecarLaunch) -> Result<(), &'static str> {
        let mut child = self.0.lock().map_err(|_| "core process lock poisoned")?;
        if child
            .as_mut()
            .is_some_and(|process| process.try_wait().ok().flatten().is_none())
        {
            return Ok(());
        }
        if !launch.executable.is_file() {
            return Err("packaged Go sidecar is unavailable");
        }
        let process = launch
            .command()
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .map_err(|_| "failed to start Go sidecar")?;
        *child = Some(process);
        Ok(())
    }

    pub(crate) fn health(&self, launch: &SidecarLaunch) -> Result<serde_json::Value, &'static str> {
        self.request(launch, "core.health", serde_json::json!({}))
    }

    pub(crate) fn request(
        &self,
        launch: &SidecarLaunch,
        method: &str,
        params: serde_json::Value,
    ) -> Result<serde_json::Value, &'static str> {
        self.request_with_idempotency(launch, method, params, None)
    }

    pub(crate) fn request_with_idempotency(
        &self,
        launch: &SidecarLaunch,
        method: &str,
        params: serde_json::Value,
        idempotency_key: Option<&str>,
    ) -> Result<serde_json::Value, &'static str> {
        let mut meta =
            serde_json::json!({"protocol_version":1,"request_id":request_id()?,"caller":"gateway"});
        if let Some(key) = idempotency_key {
            meta["idempotency_key"] = serde_json::Value::String(key.to_owned());
        }
        let request =
            serde_json::json!({"jsonrpc":"2.0","id":1,"method":method,"params":params,"meta":meta})
                .to_string();

        match self.request_once(launch, &request) {
            Ok(response) => Ok(response),
            Err(_) => {
                self.reset()?;
                self.request_once(launch, &request)
            }
        }
    }

    fn request_once(
        &self,
        launch: &SidecarLaunch,
        request: &str,
    ) -> Result<serde_json::Value, &'static str> {
        self.start(launch)?;
        let mut child = self.0.lock().map_err(|_| "core process lock poisoned")?;
        let process = child.as_mut().ok_or("core process was not started")?;
        let stdin = process.stdin.as_mut().ok_or("core stdin is unavailable")?;
        stdin
            .write_all(request.as_bytes())
            .map_err(|_| "failed to write core health request")?;
        stdin
            .write_all(b"\n")
            .map_err(|_| "failed to finish core health request")?;
        stdin
            .flush()
            .map_err(|_| "failed to flush core health request")?;
        let stdout = process
            .stdout
            .as_mut()
            .ok_or("core stdout is unavailable")?;
        let mut response = String::new();
        BufReader::new(stdout)
            .read_line(&mut response)
            .map_err(|_| "failed to read core health response")?;
        if response.is_empty() {
            return Err("core closed the response stream");
        }
        serde_json::from_str(&response).map_err(|_| "invalid core health response")
    }

    fn reset(&self) -> Result<(), &'static str> {
        let mut child = self.0.lock().map_err(|_| "core process lock poisoned")?;
        if let Some(mut process) = child.take() {
            let _ = process.kill();
            let _ = process.wait();
        }
        Ok(())
    }
}

fn request_id() -> Result<String, &'static str> {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| "clock unavailable")?
        .as_nanos();
    let sequence = REQUEST_SEQUENCE.fetch_add(1, Ordering::Relaxed);
    Ok(format!(
        "{:08x}-{:04x}-7{:03x}-8{:03x}-{:012x}",
        (nanos >> 64) as u32,
        (nanos >> 48) as u16,
        (nanos >> 36) as u16 & 0x0fff,
        (nanos >> 24) as u16 & 0x0fff,
        ((nanos as u64) ^ sequence) & 0x0000_ffff_ffff_ffff,
    ))
}

fn sidecar_file_name() -> &'static str {
    if cfg!(target_os = "windows") {
        "knowledge-core.exe"
    } else {
        "knowledge-core"
    }
}

fn sidecar_target() -> &'static str {
    if cfg!(all(target_os = "windows", target_arch = "x86_64")) {
        "windows-x64"
    } else if cfg!(all(target_os = "linux", target_arch = "x86_64")) {
        "linux-x64"
    } else {
        "unsupported"
    }
}

#[cfg(test)]
mod tests {
    use super::{CoreProcess, SidecarLaunch};
    use std::path::PathBuf;

    #[cfg(unix)]
    fn write_manifest(root: &std::path::Path) {
        std::fs::write(
            root.join("sidecar-manifest.json"),
            r#"{"core_version":"0.1.0","protocol_version":1,"schema_version":6,"targets":["windows-x64","linux-x64"]}"#,
        )
        .unwrap();
    }

    #[test]
    fn rejects_relative_paths() {
        assert!(SidecarLaunch::new(
            PathBuf::from("resource"),
            PathBuf::from("bin"),
            PathBuf::from("data")
        )
        .is_err());
    }

    #[test]
    fn passes_only_fixed_serve_arguments() {
        let resource = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
        let data = resource.join("data");
        let launch = SidecarLaunch::new(resource.clone(), resource, data.clone()).unwrap();
        let command = launch.command();
        let values: Vec<_> = command
            .get_args()
            .map(|value| value.to_string_lossy().into_owned())
            .collect();
        assert_eq!(
            values,
            vec![
                "serve".to_string(),
                "--data-dir".to_string(),
                data.display().to_string()
            ]
        );
    }

    #[test]
    fn rejects_missing_or_incompatible_manifest() {
        let root =
            std::env::temp_dir().join(format!("knowledge-sidecar-manifest-{}", std::process::id()));
        std::fs::create_dir_all(&root).unwrap();
        assert!(SidecarLaunch::new(root.clone(), root.join("bin"), root.join("data")).is_err());
        std::fs::write(
            root.join("sidecar-manifest.json"),
            r#"{"core_version":"0.1.0","protocol_version":1,"schema_version":6,"targets":["windows-x64"]}"#,
        )
        .unwrap();
        assert!(SidecarLaunch::new(root.clone(), root.join("bin"), root.join("data")).is_err());
        std::fs::remove_dir_all(root).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn health_round_trip_uses_stdio_json_rpc() {
        use std::fs;
        use std::os::unix::fs::PermissionsExt;

        let root = std::env::temp_dir().join(format!("knowledge-sidecar-{}", std::process::id()));
        let binaries = root.join("bin");
        fs::create_dir_all(&binaries).unwrap();
        write_manifest(&root);
        let executable = binaries.join("knowledge-core");
        fs::write(&executable, "#!/bin/sh\nread line\nprintf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"value\":{\"ready\":true}}}'\n").unwrap();
        fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
        let launch = SidecarLaunch::new(root.clone(), binaries, root.join("data")).unwrap();
        let value = CoreProcess::default().health(&launch).unwrap();
        assert_eq!(value["result"]["value"]["ready"], true);
        fs::remove_dir_all(root).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn retries_once_after_sidecar_exits_before_response() {
        use std::fs;
        use std::os::unix::fs::PermissionsExt;

        let root =
            std::env::temp_dir().join(format!("knowledge-sidecar-retry-{}", std::process::id()));
        let binaries = root.join("bin");
        fs::create_dir_all(&binaries).unwrap();
        write_manifest(&root);
        let executable = binaries.join("knowledge-core");
        fs::write(&executable, "#!/bin/sh\nstate=\"$0.state\"\nread line\nif [ ! -f \"$state\" ]; then\n  : > \"$state\"\n  exit 1\nfi\nprintf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"value\":{\"ready\":true}}}'\n").unwrap();
        fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
        let launch = SidecarLaunch::new(root.clone(), binaries, root.join("data")).unwrap();
        let value = CoreProcess::default().health(&launch).unwrap();
        assert_eq!(value["result"]["value"]["ready"], true);
        fs::remove_dir_all(root).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn retries_interrupted_write_requests_with_same_idempotency_key() {
        use std::fs;
        use std::os::unix::fs::PermissionsExt;

        let root =
            std::env::temp_dir().join(format!("knowledge-sidecar-write-{}", std::process::id()));
        let binaries = root.join("bin");
        fs::create_dir_all(&binaries).unwrap();
        write_manifest(&root);
        let executable = binaries.join("knowledge-core");
        fs::write(
            &executable,
            "#!/bin/sh\nstate=\"$0.state\"\nread line\nprintf '%s\\n' \"$line\" >> \"$state\"\nif [ \"$(wc -l < \"$state\")\" -eq 1 ]; then\n  exit 1\nfi\nprintf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"value\":{\"ready\":true}}}'\n",
        )
        .unwrap();
        fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
        let launch = SidecarLaunch::new(root.clone(), binaries, root.join("data")).unwrap();
        let process = CoreProcess::default();
        let value = process
            .request_with_idempotency(
                &launch,
                "import.create",
                serde_json::json!({}),
                Some("write-key"),
            )
            .unwrap();
        assert_eq!(value["result"]["value"]["ready"], true);
        let requests: Vec<serde_json::Value> =
            fs::read_to_string(format!("{}.state", executable.display()))
                .unwrap()
                .lines()
                .map(serde_json::from_str)
                .collect::<Result<_, _>>()
                .unwrap();
        assert_eq!(requests.len(), 2);
        assert!(requests
            .iter()
            .all(|item| item["meta"]["idempotency_key"] == "write-key"));
        assert_eq!(
            requests[0]["meta"]["request_id"],
            requests[1]["meta"]["request_id"]
        );
        process.reset().unwrap();
        fs::remove_dir_all(root).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn assigns_distinct_request_ids_to_separate_calls() {
        use std::fs;
        use std::os::unix::fs::PermissionsExt;

        let root = std::env::temp_dir().join(format!(
            "knowledge-sidecar-request-id-{}",
            std::process::id()
        ));
        let binaries = root.join("bin");
        fs::create_dir_all(&binaries).unwrap();
        write_manifest(&root);
        let executable = binaries.join("knowledge-core");
        fs::write(
            &executable,
            "#!/bin/sh\nstate=\"$0.state\"\nwhile read line; do\n  printf '%s\\n' \"$line\" >> \"$state\"\n  printf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"value\":{\"ready\":true}}}'\ndone\n",
        )
        .unwrap();
        fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
        let launch = SidecarLaunch::new(root.clone(), binaries, root.join("data")).unwrap();
        let process = CoreProcess::default();
        process.health(&launch).unwrap();
        process.health(&launch).unwrap();
        let requests: Vec<serde_json::Value> =
            fs::read_to_string(format!("{}.state", executable.display()))
                .unwrap()
                .lines()
                .map(serde_json::from_str)
                .collect::<Result<_, _>>()
                .unwrap();
        assert_eq!(requests.len(), 2);
        assert_ne!(
            requests[0]["meta"]["request_id"],
            requests[1]["meta"]["request_id"]
        );
        process.reset().unwrap();
        fs::remove_dir_all(root).unwrap();
    }
}
