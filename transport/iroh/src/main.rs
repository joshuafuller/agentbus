//! Local stream bridge. Official Iroh owns every network connection.
use base64::{Engine, engine::general_purpose::STANDARD};
use iroh::{
    Endpoint, EndpointAddr, RelayMap, RelayMode, SecretKey,
    endpoint::{Connection, presets},
    tls::CaTlsConfig,
};
use rustls_pki_types::{CertificateDer, pem::PemObject};
use serde::Deserialize;
use serde_json::json;
use std::{
    error::Error,
    io::{BufRead, Read},
    path::PathBuf,
    time::Duration,
};
use tokio::{io::AsyncWriteExt, net::UnixStream};

type Result<T> = std::result::Result<T, Box<dyn Error + Send + Sync>>;
const ALPN: &[u8] = b"agentbus/1";

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Config {
    socket: String,
    seed: Option<String>,
    endpoint: Option<String>,
    relay: String,
    ips: Vec<String>,
    bind: String,
    relay_only: bool,
    direct_only: bool,
}

async fn bridge(conn: Connection, socket: &str, client: bool) -> Result<()> {
    let (mut send, mut recv) = if client {
        conn.open_bi().await?
    } else {
        tokio::time::timeout(Duration::from_secs(15), conn.accept_bi()).await??
    };
    let local = UnixStream::connect(socket).await?;
    let (mut read, mut write) = local.into_split();
    let upload = async {
        tokio::io::copy(&mut read, &mut send).await?;
        send.finish()?;
        Ok::<_, Box<dyn Error + Send + Sync>>(())
    };
    let download = async {
        tokio::io::copy(&mut recv, &mut write).await?;
        write.shutdown().await?;
        Ok::<_, Box<dyn Error + Send + Sync>>(())
    };
    tokio::try_join!(upload, download)?;
    // FIN must reach the reader before connection teardown discards stream bytes.
    let _ = tokio::time::timeout(Duration::from_secs(15), conn.closed()).await;
    Ok(())
}

fn report(conn: &Connection) {
    let paths: Vec<_> = conn
        .paths()
        .iter()
        .map(|p| {
            let s = p.stats();
            json!({"ID":p.id().to_string(),"Validated":true,"Selected":p.is_selected(),
            "Relayed":p.is_relay(),"BytesSent":s.udp_tx.bytes,"BytesReceived":s.udp_rx.bytes})
        })
        .collect();
    println!("{}", json!({"paths":paths}));
}

async fn run(cfg: Config) -> Result<()> {
    let mut builder = Endpoint::builder(presets::Minimal).alpns(vec![ALPN.to_vec()]);
    if let Some(seed) = cfg.seed.as_ref() {
        let seed: [u8; 32] = STANDARD
            .decode(seed)?
            .try_into()
            .map_err(|_| "invalid seed length")?;
        builder = builder.secret_key(SecretKey::from_bytes(&seed));
    }
    if cfg.direct_only {
        builder = builder.relay_mode(RelayMode::Disabled);
    } else if !cfg.relay.is_empty() {
        builder = builder.relay_mode(RelayMode::Custom(RelayMap::from_iter([cfg
            .relay
            .parse::<iroh::RelayUrl>(
        )?])));
    } else {
        builder = builder.relay_mode(RelayMode::Default);
    }
    if cfg.relay_only {
        builder = builder.clear_ip_transports();
    }
    if !cfg.bind.is_empty() {
        builder = builder.clear_ip_transports().bind_addr(&cfg.bind)?;
    }
    if let Ok(path) = std::env::var("SSL_CERT_FILE") {
        let roots =
            CertificateDer::pem_file_iter(path)?.collect::<std::result::Result<Vec<_>, _>>()?;
        if roots.is_empty() {
            return Err("SSL_CERT_FILE contains no certificates".into());
        }
        builder = builder.ca_tls_config(CaTlsConfig::custom_roots(roots));
    }
    let ep = builder.bind().await?;
    if let Some(id) = cfg.endpoint {
        let mut addr = EndpointAddr::new(id.parse()?);
        if !cfg.relay.is_empty() {
            addr = addr.with_relay_url(cfg.relay.parse()?);
        }
        for ip in cfg.ips {
            addr = addr.with_ip_addr(ip.parse()?);
        }
        let conn = tokio::time::timeout(Duration::from_secs(30), ep.connect(addr, ALPN)).await??;
        println!("{}", json!({"ready":true}));
        let exchange = bridge(conn.clone(), &cfg.socket, true);
        tokio::pin!(exchange);
        let mut tick = tokio::time::interval(Duration::from_millis(200));
        loop {
            tokio::select! { result = &mut exchange => { result?; break; }, _ = tick.tick() => report(&conn) }
        }
    } else {
        if !cfg.direct_only {
            tokio::time::timeout(Duration::from_secs(30), ep.online()).await?;
        }
        let addr = ep.addr();
        println!(
            "{}",
            json!({"ready":true,"id":ep.id().to_string(),
            "relay":addr.relay_urls().next().map(ToString::to_string).unwrap_or_default(),
            "ips":addr.ip_addrs().map(ToString::to_string).collect::<Vec<_>>()})
        );
        while let Some(incoming) = ep.accept().await {
            let socket = cfg.socket.clone();
            tokio::spawn(async move {
                let connection = tokio::time::timeout(Duration::from_secs(15), incoming).await;
                if let Ok(Ok(conn)) = connection {
                    let _ = bridge(conn, &socket, false).await;
                }
            });
        }
    }
    ep.close().await;
    Ok(())
}

#[tokio::main]
async fn main() -> Result<()> {
    if std::env::args().nth(1).as_deref() == Some("--version") {
        println!(
            "agentbus-iroh {} (official iroh 1.3.0)",
            env!("CARGO_PKG_VERSION")
        );
        return Ok(());
    }
    let mut stdin = std::io::BufReader::new(std::io::stdin());
    let mut line = String::new();
    stdin.read_line(&mut line)?;
    if line.len() > 16384 {
        return Err("configuration too large".into());
    }
    let cfg: Config = serde_json::from_str(&line)?;
    let socket = PathBuf::from(&cfg.socket);
    // A plain thread avoids trapping Tokio's shutdown on a blocking stdin read.
    // Parent pipe EOF also stops the helper after an abrupt Agentbus exit.
    let (closed, parent_closed) = tokio::sync::oneshot::channel();
    std::thread::spawn(move || {
        let _ = stdin.read(&mut [0u8; 1]);
        let _ = closed.send(());
    });
    let result = tokio::select! { result = run(cfg) => result, _ = parent_closed => Ok(()) };
    // The configuration is parent-owned. Remove only the generated IPC path;
    // remove_dir (not recursive removal) leaves any unexpected contents intact.
    if socket.file_name().is_some_and(|name| name == "stream.sock")
        && let Some(dir) = socket.parent()
        && dir
            .file_name()
            .and_then(|name| name.to_str())
            .is_some_and(|name| name.starts_with("ab-"))
    {
        let _ = std::fs::remove_file(&socket);
        let _ = std::fs::remove_dir(dir);
    }
    result
}
