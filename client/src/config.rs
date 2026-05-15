use anyhow::{Context, Result};
use serde::Deserialize;
use std::path::Path;

#[derive(Debug, Deserialize, Clone)]
pub struct Config {
    pub server_url: String,
    pub client_id: String,
    pub token: String,
}

impl Config {
    pub fn load(path: &Path) -> Result<Self> {
        let raw = std::fs::read_to_string(path)
            .with_context(|| format!("read config {}", path.display()))?;
        toml::from_str(&raw).context("parse config.toml")
    }

    pub fn is_complete(&self) -> bool {
        !self.server_url.is_empty() && !self.client_id.is_empty() && !self.token.is_empty()
    }

    pub fn template() -> &'static str {
        r#"server_url = "wss://example.com/api/v1/stream"
client_id  = ""
token      = ""
"#
    }
}
