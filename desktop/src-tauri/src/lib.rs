use std::collections::HashMap;

use serde::{Deserialize, Serialize};

/// A REST request proxied through the Rust core (WS2/WS8). This lets the desktop
/// build keep the bearer token out of the webview JS: the frontend calls this
/// command instead of `fetch` when running under Tauri, and the token is
/// attached here. The plain-browser build uses `fetch` directly.
#[derive(Deserialize)]
struct HubRequest {
    method: String,
    url: String,
    #[serde(default)]
    headers: HashMap<String, String>,
    #[serde(default)]
    body: Option<String>,
}

#[derive(Serialize)]
struct HubResponse {
    status: u16,
    body: String,
}

#[tauri::command]
async fn hub_request(req: HubRequest) -> Result<HubResponse, String> {
    let method = reqwest::Method::from_bytes(req.method.as_bytes()).map_err(|e| e.to_string())?;
    let client = reqwest::Client::new();
    let mut builder = client.request(method, &req.url);
    for (key, value) in req.headers {
        builder = builder.header(key, value);
    }
    if let Some(body) = req.body {
        builder = builder.body(body);
    }
    let resp = builder.send().await.map_err(|e| e.to_string())?;
    let status = resp.status().as_u16();
    let body = resp.text().await.map_err(|e| e.to_string())?;
    Ok(HubResponse { status, body })
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![hub_request])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
