use serde::{Deserialize, Serialize};
use sockd::Daemon;
use std::collections::HashMap;
use std::env;
use std::error::Error;
use std::fs;
use std::path::PathBuf;
use std::time::{Duration, SystemTime};

type BoxError = Box<dyn Error + Send + Sync + 'static>;

#[derive(Debug)]
struct Config {
    socket_path: PathBuf,
    pid_file: PathBuf,
    state_file: PathBuf,
    idle_timeout: Duration,
}

#[derive(Debug, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
enum Query {
    Health,
    HubInfo,
    WorkingSet,
    RecentEvents { limit: Option<usize> },
    ProjectStats,
}

/// Deserialize a field that may be null or missing as an empty Vec.
/// Go's json.Marshal writes `null` for nil slices, but serde's #[default]
/// only handles missing fields, not explicit nulls.
fn null_as_empty_vec<'de, D, T>(deserializer: D) -> Result<Vec<T>, D::Error>
where
    D: serde::Deserializer<'de>,
    T: Deserialize<'de>,
{
    Ok(Option::<Vec<T>>::deserialize(deserializer)?.unwrap_or_default())
}

fn null_as_empty_map<'de, D, V>(deserializer: D) -> Result<HashMap<String, V>, D::Error>
where
    D: serde::Deserializer<'de>,
    V: Deserialize<'de>,
{
    Ok(Option::<HashMap<String, V>>::deserialize(deserializer)?.unwrap_or_default())
}

#[derive(Debug, Clone, Default, Deserialize)]
struct CodemapState {
    #[serde(default)]
    updated_at: String,
    #[serde(default)]
    file_count: usize,
    #[serde(default, deserialize_with = "null_as_empty_vec")]
    hubs: Vec<String>,
    #[serde(default, deserialize_with = "null_as_empty_map")]
    importers: HashMap<String, Vec<String>>,
    #[serde(default, deserialize_with = "null_as_empty_map")]
    imports: HashMap<String, Vec<String>>,
    #[serde(default, deserialize_with = "null_as_empty_vec")]
    recent_events: Vec<RecentEvent>,
    #[serde(default)]
    working_set: Option<WorkingSet>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct RecentEvent {
    #[serde(default)]
    time: String,
    #[serde(default)]
    op: String,
    #[serde(default)]
    path: String,
    #[serde(default)]
    lang: String,
    #[serde(default)]
    lines: i64,
    #[serde(default)]
    delta: i64,
    #[serde(default)]
    size_delta: i64,
    #[serde(default)]
    dirty: bool,
    #[serde(default)]
    importers: usize,
    #[serde(default)]
    imports: usize,
    #[serde(default)]
    is_hub: bool,
    #[serde(default)]
    related_hot: Vec<String>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct WorkingSet {
    #[serde(default)]
    files: HashMap<String, WorkingFile>,
    #[serde(default)]
    started_at: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct WorkingFile {
    #[serde(default)]
    path: String,
    #[serde(default)]
    first_touch: String,
    #[serde(default)]
    last_touch: String,
    #[serde(default)]
    edit_count: usize,
    #[serde(default)]
    net_delta: i64,
    #[serde(default)]
    is_hub: bool,
    #[serde(default)]
    importers: usize,
}

#[derive(Debug, Serialize)]
struct HubInfoResponse {
    updated_at: String,
    file_count: usize,
    hubs: Vec<String>,
    importers: HashMap<String, Vec<String>>,
    imports: HashMap<String, Vec<String>>,
}

#[derive(Debug, Serialize)]
struct WorkingSetResponse {
    updated_at: String,
    working_set: Option<WorkingSet>,
}

#[derive(Debug, Serialize)]
struct RecentEventsResponse {
    updated_at: String,
    recent_events: Vec<RecentEvent>,
}

#[derive(Debug, Serialize)]
struct ProjectStatsResponse {
    updated_at: String,
    file_count: usize,
    hubs: Vec<String>,
}

#[derive(Debug, Serialize)]
struct HealthResponse {
    status: &'static str,
}

#[derive(Debug)]
struct StateCache {
    state_file: PathBuf,
    modified_at: Option<SystemTime>,
    cached: Option<CodemapState>,
}

impl StateCache {
    fn new(state_file: PathBuf) -> Self {
        Self {
            state_file,
            modified_at: None,
            cached: None,
        }
    }

    fn handle(&mut self, payload: &[u8]) -> Result<Vec<u8>, BoxError> {
        let query: Query = serde_json::from_slice(payload)?;
        match query {
            Query::Health => {
                serde_json::to_vec(&HealthResponse { status: "ok" }).map_err(Into::into)
            }
            Query::HubInfo => {
                let state = self.load_state()?;
                serde_json::to_vec(&HubInfoResponse {
                    updated_at: state.updated_at.clone(),
                    file_count: state.file_count,
                    hubs: state.hubs.clone(),
                    importers: state.importers.clone(),
                    imports: state.imports.clone(),
                })
                .map_err(Into::into)
            }
            Query::WorkingSet => {
                let state = self.load_state()?;
                serde_json::to_vec(&WorkingSetResponse {
                    updated_at: state.updated_at.clone(),
                    working_set: state.working_set.clone(),
                })
                .map_err(Into::into)
            }
            Query::RecentEvents { limit } => {
                let state = self.load_state()?;
                let recent_events = trim_recent_events(&state.recent_events, limit);
                serde_json::to_vec(&RecentEventsResponse {
                    updated_at: state.updated_at.clone(),
                    recent_events,
                })
                .map_err(Into::into)
            }
            Query::ProjectStats => {
                let state = self.load_state()?;
                serde_json::to_vec(&ProjectStatsResponse {
                    updated_at: state.updated_at.clone(),
                    file_count: state.file_count,
                    hubs: state.hubs.clone(),
                })
                .map_err(Into::into)
            }
        }
    }

    fn load_state(&mut self) -> Result<&CodemapState, BoxError> {
        let metadata = fs::metadata(&self.state_file)?;
        let modified_at = metadata.modified()?;

        let needs_reload = self.cached.is_none() || self.modified_at != Some(modified_at);
        if needs_reload {
            let bytes = fs::read(&self.state_file)?;
            let state: CodemapState = serde_json::from_slice(&bytes)?;
            self.cached = Some(state);
            self.modified_at = Some(modified_at);
        }

        self.cached
            .as_ref()
            .ok_or_else(|| "codemap state cache unavailable".into())
    }
}

fn trim_recent_events(events: &[RecentEvent], limit: Option<usize>) -> Vec<RecentEvent> {
    let Some(limit) = limit else {
        return events.to_vec();
    };
    if limit == 0 || events.len() <= limit {
        return events.to_vec();
    }
    events[events.len() - limit..].to_vec()
}

fn main() -> Result<(), BoxError> {
    let config = parse_args(env::args().skip(1))?;
    if let Some(parent) = config.socket_path.parent() {
        fs::create_dir_all(parent)?;
    }

    let state_file = config.state_file.clone();
    let daemon = Daemon::builder()
        .socket(&config.socket_path)
        .pid_file(&config.pid_file)
        .idle_timeout(config.idle_timeout)
        .on_start(move || Ok(StateCache::new(state_file.clone())))
        .on_request(|cache, payload| cache.handle(payload))
        .build()?;

    daemon.run()?;
    Ok(())
}

fn parse_args<I>(args: I) -> Result<Config, BoxError>
where
    I: IntoIterator<Item = String>,
{
    let mut root: Option<PathBuf> = None;
    let mut socket_path: Option<PathBuf> = None;
    let mut pid_file: Option<PathBuf> = None;
    let mut state_file: Option<PathBuf> = None;
    let mut idle_secs = 300u64;

    let mut iter = args.into_iter();
    while let Some(arg) = iter.next() {
        match arg.as_str() {
            "--root" => root = Some(PathBuf::from(next_arg(&mut iter, "--root")?)),
            "--socket" => {
                socket_path = Some(PathBuf::from(next_arg(&mut iter, "--socket")?));
            }
            "--pid-file" => {
                pid_file = Some(PathBuf::from(next_arg(&mut iter, "--pid-file")?));
            }
            "--state-file" => {
                state_file = Some(PathBuf::from(next_arg(&mut iter, "--state-file")?));
            }
            "--idle-secs" => {
                idle_secs = next_arg(&mut iter, "--idle-secs")?.parse()?;
            }
            "--help" | "-h" => {
                print_usage();
                std::process::exit(0);
            }
            other => {
                return Err(format!("unexpected argument: {other}").into());
            }
        }
    }

    let root = root.ok_or_else(|| "--root is required".to_string())?;
    let codemap_dir = root.join(".codemap");

    Ok(Config {
        socket_path: socket_path.unwrap_or_else(|| codemap_dir.join("codemapd.sock")),
        pid_file: pid_file.unwrap_or_else(|| codemap_dir.join("codemapd.pid")),
        state_file: state_file.unwrap_or_else(|| codemap_dir.join("state.json")),
        idle_timeout: Duration::from_secs(idle_secs),
    })
}

fn next_arg<I>(iter: &mut I, flag: &str) -> Result<String, BoxError>
where
    I: Iterator<Item = String>,
{
    iter.next()
        .ok_or_else(|| format!("{flag} requires a value").into())
}

fn print_usage() {
    eprintln!(
        "Usage: codemapd --root <project-root> [--socket <path>] [--pid-file <path>] [--state-file <path>] [--idle-secs <n>]"
    );
}
