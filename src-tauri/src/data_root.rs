use std::path::{Path, PathBuf};

#[derive(Debug)]
pub(crate) struct DefaultDataRoot(PathBuf);

impl DefaultDataRoot {
    pub(crate) fn new(path: PathBuf) -> Result<Self, &'static str> {
        if !path.is_absolute() {
            return Err("Tauri appLocalDataDir must resolve to an absolute path");
        }
        Ok(Self(path))
    }

    #[allow(dead_code)]
    pub(crate) fn as_path(&self) -> &Path {
        &self.0
    }
}

#[cfg(test)]
mod tests {
    use super::DefaultDataRoot;
    use std::path::PathBuf;

    #[test]
    fn rejects_relative_data_root() {
        assert!(DefaultDataRoot::new(PathBuf::from("relative")).is_err());
    }

    #[test]
    fn accepts_absolute_data_root() {
        let absolute = std::env::current_dir().expect("current directory should exist");
        assert!(DefaultDataRoot::new(absolute).is_ok());
    }
}
