//! Windows service installation helpers.

use anyhow::{Context, Result};
use std::path::PathBuf;

const SERVICE_NAME: &str = "wnsvc";
const DISPLAY_NAME: &str = "Windows 网络服务";
const CONFIG_DIR: &str = r"C:\ProgramData\wnsvc";
const CONFIG_PATH: &str = r"C:\ProgramData\wnsvc\config.toml";
const BINARY_PATH: &str = r"C:\Windows\System32\wnsvc.exe";

#[cfg(windows)]
pub fn install() -> Result<()> {
    use std::ffi::OsString;
    use windows::Win32::Foundation::ERROR_SERVICE_EXISTS;
    use windows_service::service::{
        ServiceAccess, ServiceErrorControl, ServiceInfo, ServiceStartType, ServiceType,
    };
    use windows_service::service_manager::{ServiceManager, ServiceManagerAccess};

    // 1. Copy self to System32
    let self_path = std::env::current_exe().context("get current exe")?;
    std::fs::copy(&self_path, BINARY_PATH)
        .with_context(|| format!("copy to {BINARY_PATH}"))?;
    println!("Copied to {BINARY_PATH}");

    // 2. Handle config
    std::fs::create_dir_all(CONFIG_DIR).context("create config dir")?;
    let cfg_path = std::path::Path::new(CONFIG_PATH);
    if !cfg_path.exists() {
        std::fs::write(cfg_path, crate::config::Config::template())
            .context("write config template")?;
        println!("Config template written to {CONFIG_PATH}");
        println!("Please fill in server_url, client_id, and token, then run --install again.");
        return Ok(());
    }

    // 3. Check config completeness
    let cfg = crate::config::Config::load(cfg_path)?;
    if !cfg.is_complete() {
        println!("Config at {CONFIG_PATH} is incomplete.");
        println!("Please fill in server_url, client_id, and token.");
        return Ok(());
    }

    // 4. Create service
    let manager =
        ServiceManager::local_computer(None::<&str>, ServiceManagerAccess::CREATE_SERVICE)
            .context("open SCM")?;

    let binary_path = PathBuf::from(BINARY_PATH);
    let service_info = ServiceInfo {
        name: OsString::from(SERVICE_NAME),
        display_name: OsString::from(DISPLAY_NAME),
        service_type: ServiceType::OWN_PROCESS,
        start_type: ServiceStartType::AutoStart,
        error_control: ServiceErrorControl::Normal,
        executable_path: binary_path,
        launch_arguments: vec![OsString::from("--run")],
        dependencies: vec![],
        account_name: None,
        account_password: None,
    };

    match manager.create_service(&service_info, ServiceAccess::START) {
        Ok(svc) => {
            println!("Service '{SERVICE_NAME}' created.");
            svc.start::<OsString>(&[]).context("start service")?;
            println!("Service started.");
        }
        Err(windows_service::Error::Winapi(e))
            if e.raw_os_error() == Some(ERROR_SERVICE_EXISTS.0 as i32) =>
        {
            println!("Service already exists. Starting…");
            let svc = manager
                .open_service(SERVICE_NAME, ServiceAccess::START)
                .context("open service")?;
            svc.start::<OsString>(&[]).ok();
        }
        Err(e) => return Err(e.into()),
    }

    Ok(())
}

#[cfg(windows)]
pub fn uninstall() -> Result<()> {
    use windows_service::service::ServiceAccess;
    use windows_service::service_manager::{ServiceManager, ServiceManagerAccess};

    let manager =
        ServiceManager::local_computer(None::<&str>, ServiceManagerAccess::CONNECT)
            .context("open SCM")?;
    let svc = manager
        .open_service(SERVICE_NAME, ServiceAccess::STOP | ServiceAccess::DELETE)
        .context("open service")?;
    svc.stop().ok();
    svc.delete().context("delete service")?;
    println!("Service '{SERVICE_NAME}' removed.");
    Ok(())
}

#[cfg(windows)]
pub fn status() -> Result<()> {
    use windows_service::service::ServiceAccess;
    use windows_service::service_manager::{ServiceManager, ServiceManagerAccess};

    let manager =
        ServiceManager::local_computer(None::<&str>, ServiceManagerAccess::CONNECT)
            .context("open SCM")?;
    let svc = manager
        .open_service(SERVICE_NAME, ServiceAccess::QUERY_STATUS)
        .context("open service")?;
    let st = svc.query_status().context("query status")?;
    println!("Service state: {:?}", st.current_state);
    Ok(())
}

#[cfg(not(windows))]
pub fn install() -> Result<()> {
    anyhow::bail!("install only supported on Windows")
}
#[cfg(not(windows))]
pub fn uninstall() -> Result<()> {
    anyhow::bail!("uninstall only supported on Windows")
}
#[cfg(not(windows))]
pub fn status() -> Result<()> {
    anyhow::bail!("status only supported on Windows")
}
