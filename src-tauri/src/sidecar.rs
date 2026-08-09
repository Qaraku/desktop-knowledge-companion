use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;

#[derive(Debug)]
pub(crate) struct SidecarLaunch {
    executable: PathBuf,
    #[allow(dead_code)]
    data_dir: PathBuf,
}

impl SidecarLaunch {
    pub(crate) fn new(resource_dir: PathBuf, data_dir: PathBuf) -> Result<Self, &'static str> {
        if !resource_dir.is_absolute() || !data_dir.is_absolute() {
            return Err("sidecar resource and data directories must be absolute");
        }
        let executable = resource_dir.join("binaries").join(sidecar_file_name());
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
        self.start(launch)?;
        let mut child = self.0.lock().map_err(|_| "core process lock poisoned")?;
        let process = child.as_mut().ok_or("core process was not started")?;
        let request = r#"{"jsonrpc":"2.0","id":1,"method":"core.health","params":{},"meta":{"protocol_version":1,"request_id":"0198c787-8bf0-7afe-8c7d-9a41c6671c23","caller":"gateway"}}"#;
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
        serde_json::from_str(&response).map_err(|_| "invalid core health response")
    }
}

fn sidecar_file_name() -> &'static str {
    if cfg!(target_os = "windows") {
        "knowledge-core.exe"
    } else {
        "knowledge-core"
    }
}

#[cfg(test)]
mod tests {
    use super::{CoreProcess, SidecarLaunch};
    use std::path::PathBuf;

    #[test]
    fn rejects_relative_paths() {
        assert!(SidecarLaunch::new(PathBuf::from("resource"), PathBuf::from("data")).is_err());
    }

    #[test]
    fn passes_only_fixed_serve_arguments() {
        let resource = std::env::current_dir().unwrap();
        let data = resource.join("data");
        let launch = SidecarLaunch::new(resource, data.clone()).unwrap();
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

    #[cfg(unix)]
    #[test]
    fn health_round_trip_uses_stdio_json_rpc() {
        use std::fs;
        use std::os::unix::fs::PermissionsExt;

        let root = std::env::temp_dir().join(format!("knowledge-sidecar-{}", std::process::id()));
        let binaries = root.join("binaries");
        fs::create_dir_all(&binaries).unwrap();
        let executable = binaries.join("knowledge-core");
        fs::write(&executable, "#!/bin/sh\nread line\nprintf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"value\":{\"ready\":true}}}'\n").unwrap();
        fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
        let launch = SidecarLaunch::new(root.clone(), root.join("data")).unwrap();
        let value = CoreProcess::default().health(&launch).unwrap();
        assert_eq!(value["result"]["value"]["ready"], true);
        fs::remove_dir_all(root).unwrap();
    }
}
