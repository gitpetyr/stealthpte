//! Windows service integration.
//! On non-Windows platforms this is a stub so the code compiles cross-platform.

#[cfg(windows)]
pub mod windows_service {
    use std::ffi::OsString;
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::Arc;
    use std::time::Duration;
    use windows_service::{
        define_windows_service,
        service::{
            ServiceControl, ServiceControlAccept, ServiceExitCode, ServiceState, ServiceStatus,
            ServiceType,
        },
        service_control_handler::{self, ServiceControlHandlerResult},
        service_dispatcher,
    };

    static STOP_FLAG: std::sync::OnceLock<Arc<AtomicBool>> = std::sync::OnceLock::new();

    define_windows_service!(ffi_service_main, service_main);

    pub fn run_as_service() -> anyhow::Result<()> {
        service_dispatcher::start("wnsvc", ffi_service_main)
            .map_err(|e| anyhow::anyhow!("service_dispatcher: {e}"))?;
        Ok(())
    }

    fn service_main(_args: Vec<OsString>) {
        // Write rolling daily logs to C:\ProgramData\wnsvc\wnsvc.log.<date>
        // _guard must stay alive until service_main returns (service stops).
        let file_appender =
            tracing_appender::rolling::daily(r"C:\ProgramData\wnsvc", "wnsvc.log");
        let (non_blocking, _guard) = tracing_appender::non_blocking(file_appender);
        tracing_subscriber::fmt()
            .with_env_filter(
                tracing_subscriber::EnvFilter::from_default_env()
                    .add_directive(tracing::Level::INFO.into()),
            )
            .with_writer(non_blocking)
            .with_ansi(false)
            .init();

        let stop = Arc::new(AtomicBool::new(false));
        STOP_FLAG.set(stop.clone()).ok();

        let status_handle = service_control_handler::register(
            "wnsvc",
            move |ctrl| match ctrl {
                ServiceControl::Stop => {
                    stop.store(true, Ordering::Relaxed);
                    ServiceControlHandlerResult::NoError
                }
                ServiceControl::Interrogate => ServiceControlHandlerResult::NoError,
                _ => ServiceControlHandlerResult::NotImplemented,
            },
        )
        .expect("register service handler");

        status_handle
            .set_service_status(ServiceStatus {
                service_type: ServiceType::OWN_PROCESS,
                current_state: ServiceState::Running,
                controls_accepted: ServiceControlAccept::STOP,
                exit_code: ServiceExitCode::Win32(0),
                checkpoint: 0,
                wait_hint: Duration::default(),
                process_id: None,
            })
            .expect("set running");

        // Build tokio runtime and run client
        let rt = tokio::runtime::Runtime::new().expect("tokio runtime");
        rt.block_on(async {
            let cfg_path = std::path::Path::new(r"C:\ProgramData\wnsvc\config.toml");
            let cfg = crate::config::Config::load(cfg_path).expect("load config");
            let stop_flag = STOP_FLAG.get().unwrap().clone();
            let client = crate::client::Client::new(cfg, stop_flag);
            client.run().await;
        });

        status_handle
            .set_service_status(ServiceStatus {
                service_type: ServiceType::OWN_PROCESS,
                current_state: ServiceState::Stopped,
                controls_accepted: ServiceControlAccept::empty(),
                exit_code: ServiceExitCode::Win32(0),
                checkpoint: 0,
                wait_hint: Duration::default(),
                process_id: None,
            })
            .ok();
    }
}

#[cfg(not(windows))]
pub mod windows_service {
    pub fn run_as_service() -> anyhow::Result<()> {
        anyhow::bail!("Windows service only supported on Windows")
    }
}
