mod client;
mod config;
mod install;
mod protocol;
mod service;
mod tunnel;
mod wsconn;

use std::path::Path;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let flag = args.get(1).map(|s| s.as_str()).unwrap_or("");

    // Service mode initialises its own file logger inside service_main.
    if flag != "--run" {
        tracing_subscriber::fmt()
            .with_env_filter(
                tracing_subscriber::EnvFilter::from_default_env()
                    .add_directive(tracing::Level::INFO.into()),
            )
            .init();
    }

    let result = match flag {
        "--install" => {
            let get_opt = |prefix: &str| -> Option<String> {
                args[2..].iter().find(|a| a.starts_with(prefix))
                    .map(|a| a[prefix.len()..].to_owned())
            };
            install::install(install::InstallOpts {
                server_url: get_opt("--server-url="),
                client_id: get_opt("--client-id="),
                token: get_opt("--token="),
            })
        }
        "--uninstall" => install::uninstall(),
        "--status" => install::status(),
        "--run" => service::windows_service::run_as_service(),
        "" => {
            // Interactive mode: read config from current directory or ProgramData
            let cfg_path = find_config();
            let cfg = match config::Config::load(&cfg_path) {
                Ok(c) => c,
                Err(e) => {
                    eprintln!("Error loading config: {e}");
                    std::process::exit(1);
                }
            };
            let stop = Arc::new(AtomicBool::new(false));
            {
                let stop2 = stop.clone();
                ctrlc_handler(stop2);
            }
            let rt = tokio::runtime::Runtime::new().expect("tokio runtime");
            rt.block_on(async {
                client::Client::new(cfg, stop).run().await;
            });
            Ok(())
        }
        other => {
            eprintln!("Unknown flag: {other}");
            eprintln!("Usage: stealthclient [--install [--server-url=WSS_URL] [--client-id=ID] [--token=TOKEN] | --uninstall | --run | --status]");
            std::process::exit(1);
        }
    };

    if let Err(e) = result {
        eprintln!("Error: {e:#}");
        std::process::exit(1);
    }
}

fn find_config() -> std::path::PathBuf {
    let local = Path::new("config.toml");
    if local.exists() {
        return local.to_owned();
    }
    Path::new(r"C:\ProgramData\wnsvc\config.toml").to_owned()
}

fn ctrlc_handler(stop: Arc<AtomicBool>) {
    // Best-effort Ctrl+C handler for interactive mode
    std::thread::spawn(move || {
        let _ = ctrlc_wait();
        stop.store(true, Ordering::Relaxed);
    });
}

fn ctrlc_wait() -> std::io::Result<()> {
    // Simple blocking read from stdin; on Ctrl+C stdin closes
    use std::io::Read;
    let mut buf = [0u8; 1];
    loop {
        match std::io::stdin().read(&mut buf) {
            Ok(0) | Err(_) => return Ok(()),
            Ok(_) => continue,
        }
    }
}
