use axum::{
    extract::Path,
    routing::{get, post, delete},
    Json, Router,
};
use dashmap::DashMap;
use serde::{Deserialize, Serialize};
use sqlx::{SqlitePool, Row};
use std::env;
use std::net::SocketAddr;
use std::sync::Arc;
use tracing_subscriber;

#[derive(Debug, Clone, Serialize, Deserialize)]
struct Proxy {
    id: i64,
    domain: String,
    port: u16,
    enabled: bool,
}

#[derive(Debug, Clone, Deserialize)]
struct CreateProxy {
    domain: String,
    port: u16,
}

struct AppState {
    db: SqlitePool,
    proxies: Arc<DashMap<String, Proxy>>,
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt::init();
    dotenvy::dotenv().ok();

    let db = SqlitePool::connect("sqlite://tunnelfy.db").await?;
    sqlx::query(
        "CREATE TABLE IF NOT EXISTS proxies (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            domain TEXT NOT NULL UNIQUE,
            port INTEGER NOT NULL,
            enabled BOOLEAN NOT NULL DEFAULT 1
        )"
    ).execute(&db).await?;

    let state = Arc::new(AppState {
        db: db.clone(),
        proxies: Arc::new(DashMap::new()),
    });

    // Load existing proxies from DB
    let rows = sqlx::query("SELECT id, domain, port, enabled FROM proxies")
        .fetch_all(&db)
        .await?;
    for row in rows {
        let proxy = Proxy {
            id: row.get(0),
            domain: row.get(1),
            port: row.get(2),
            enabled: row.get(3),
        };
        state.proxies.insert(proxy.domain.clone(), proxy);
    }

    // Load static proxies from env var STATIC_PROXIES
    if let Ok(static_proxies) = env::var("STATIC_PROXIES") {
        for entry in static_proxies.split(',').map(|s| s.trim()).filter(|s| !s.is_empty()) {
            if let Some((domain, port_str)) = entry.split_once(':') {
                if let Ok(port) = port_str.parse::<u16>() {
                    if !state.proxies.contains_key(domain) {
                        let res = sqlx::query(
                            "INSERT INTO proxies (domain, port, enabled) VALUES (?, ?, 1)"
                        )
                        .bind(domain)
                        .bind(port as i64)
                        .execute(&db)
                        .await?;

                        let id = res.last_insert_rowid();
                        let proxy = Proxy { id, domain: domain.to_string(), port, enabled: true };
                        state.proxies.insert(proxy.domain.clone(), proxy);
                        tracing::info!("Inserted static proxy: {} -> {}", domain, port);
                    }
                }
            }
        }
    }

    let app = Router::new()
        .route("/api/proxies", post(create_proxy))
        .route("/api/proxies/:id/enable", post(enable_proxy))
        .route("/api/proxies/:id/disable", post(disable_proxy))
        .route("/api/proxies/:id", delete(delete_proxy))
        .route("/api/proxies", get(list_proxies))
        .with_state(state);

    let addr = SocketAddr::from(([0, 0, 0, 0], 8080));
    tracing::info!("listening on {}", addr);
    axum::Server::bind(&addr)
        .serve(app.into_make_service())
        .await?;

    Ok(())
}

async fn create_proxy(
    axum::extract::State(state): axum::extract::State<Arc<AppState>>,
    Json(payload): Json<CreateProxy>,
) -> Result<Json<Proxy>, String> {
    let res = sqlx::query(
        "INSERT INTO proxies (domain, port, enabled) VALUES (?, ?, 1)"
    )
    .bind(&payload.domain)
    .bind(payload.port as i64)
    .execute(&state.db)
    .await
    .map_err(|e| e.to_string())?;

    let id = res.last_insert_rowid();
    let proxy = Proxy { id, domain: payload.domain, port: payload.port, enabled: true };
    state.proxies.insert(proxy.domain.clone(), proxy.clone());
    Ok(Json(proxy))
}

async fn delete_proxy(
    Path(id): Path<i64>,
    axum::extract::State(state): axum::extract::State<Arc<AppState>>,
) -> Result<Json<String>, String> {
    let result = sqlx::query("DELETE FROM proxies WHERE id = ?")
        .bind(id)
        .execute(&state.db)
        .await
        .map_err(|e| e.to_string())?;

    if result.rows_affected() == 0 {
        return Err("Proxy not found".to_string());
    }

    if let Some(entry) = state.proxies.iter().find(|p| p.value().id == id) {
        let domain = entry.domain.clone();
        state.proxies.remove(&domain);
    }

    Ok(Json(format!("Proxy {} deleted", id)))
}

async fn enable_proxy(
    Path(id): Path<i64>,
    axum::extract::State(state): axum::extract::State<Arc<AppState>>,
) -> Result<Json<Proxy>, String> {
    sqlx::query("UPDATE proxies SET enabled = 1 WHERE id = ?")
        .bind(id)
        .execute(&state.db)
        .await
        .map_err(|e| e.to_string())?;

    if let Some(mut proxy) = state.proxies.iter_mut().find(|p| p.value().id == id) {
        proxy.enabled = true;
        return Ok(Json(proxy.clone()));
    }
    Err("Proxy not found".to_string())
}

async fn disable_proxy(
    Path(id): Path<i64>,
    axum::extract::State(state): axum::extract::State<Arc<AppState>>,
) -> Result<Json<Proxy>, String> {
    sqlx::query("UPDATE proxies SET enabled = 0 WHERE id = ?")
        .bind(id)
        .execute(&state.db)
        .await
        .map_err(|e| e.to_string())?;

    if let Some(mut proxy) = state.proxies.iter_mut().find(|p| p.value().id == id) {
        proxy.enabled = false;
        return Ok(Json(proxy.clone()));
    }
    Err("Proxy not found".to_string())
}

async fn list_proxies(
    axum::extract::State(state): axum::extract::State<Arc<AppState>>,
) -> Json<Vec<Proxy>> {
    let proxies: Vec<Proxy> = state.proxies.iter().map(|e| e.value().clone()).collect();
    Json(proxies)
}
