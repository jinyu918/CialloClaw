// 该入口负责启动桌面端 Tauri 宿主。
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use serde_json::Value;
use std::collections::HashMap;
use std::fs::OpenOptions;
use std::io::{BufReader, BufWriter, Write};
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::{mpsc, Arc, Mutex};
use tauri::ipc::Channel;

type JsonChannel = Channel<Value>;

const NAMED_PIPE_PATH: &str = r"\\.\pipe\cialloclaw-rpc";

enum BridgeCommand {
    Request { payload: Value },
}

#[derive(Clone)]
struct BridgeSession {
    writer_tx: mpsc::Sender<BridgeCommand>,
}

struct NamedPipeBridgeState {
    session: Mutex<Option<BridgeSession>>,
    pending: Mutex<HashMap<String, mpsc::Sender<Result<Value, String>>>>,
    subscriptions: Mutex<HashMap<String, HashMap<u32, JsonChannel>>>,
    next_subscription_id: AtomicU32,
}

impl Default for NamedPipeBridgeState {
    fn default() -> Self {
        Self {
            session: Mutex::new(None),
            pending: Mutex::new(HashMap::new()),
            subscriptions: Mutex::new(HashMap::new()),
            next_subscription_id: AtomicU32::new(1),
        }
    }
}

impl NamedPipeBridgeState {
    fn request(self: &Arc<Self>, payload: Value) -> Result<Value, String> {
        let request_id = extract_request_id(&payload)?;
        let session = self.ensure_session()?;
        let (response_tx, response_rx) = mpsc::channel();

        self.pending
            .lock()
            .map_err(|_| "named pipe pending map lock poisoned".to_string())?
            .insert(request_id.clone(), response_tx);

        if let Err(error) = session.writer_tx.send(BridgeCommand::Request { payload }) {
            self.pending
                .lock()
                .map_err(|_| "named pipe pending map lock poisoned".to_string())?
                .remove(&request_id);
            return Err(format!("failed to queue named pipe request: {error}"));
        }

        response_rx
            .recv()
            .map_err(|error| format!("named pipe response wait failed: {error}"))?
    }

    fn subscribe(self: &Arc<Self>, topic: String, channel: JsonChannel) -> Result<u32, String> {
        self.ensure_session()?;

        let subscription_id = self.next_subscription_id.fetch_add(1, Ordering::Relaxed);
        let mut subscriptions = self
            .subscriptions
            .lock()
            .map_err(|_| "named pipe subscriptions lock poisoned".to_string())?;

        subscriptions
            .entry(topic)
            .or_insert_with(HashMap::new)
            .insert(subscription_id, channel);

        Ok(subscription_id)
    }

    fn unsubscribe(&self, subscription_id: u32) -> Result<(), String> {
        let mut subscriptions = self
            .subscriptions
            .lock()
            .map_err(|_| "named pipe subscriptions lock poisoned".to_string())?;

        for topic_channels in subscriptions.values_mut() {
            if topic_channels.remove(&subscription_id).is_some() {
                return Ok(());
            }
        }

        Ok(())
    }

    fn ensure_session(self: &Arc<Self>) -> Result<BridgeSession, String> {
        let mut session_guard = self
            .session
            .lock()
            .map_err(|_| "named pipe session lock poisoned".to_string())?;

        if let Some(session) = session_guard.clone() {
            return Ok(session);
        }

        let stream = OpenOptions::new()
            .read(true)
            .write(true)
            .open(NAMED_PIPE_PATH)
            .map_err(|error| format!("failed to open named pipe {NAMED_PIPE_PATH}: {error}"))?;

        let reader = stream
            .try_clone()
            .map_err(|error| format!("failed to clone named pipe handle: {error}"))?;

        let writer = stream;
        let (writer_tx, writer_rx) = mpsc::channel();
        let state = Arc::clone(self);
        let writer_state = Arc::clone(&state);
        std::thread::spawn(move || writer_loop(writer, writer_rx, writer_state));
        std::thread::spawn(move || reader_loop(reader, state));

        let session = BridgeSession { writer_tx };
        *session_guard = Some(session.clone());
        Ok(session)
    }

    fn dispatch_incoming(&self, message: Value) {
        if let Some(method) = message.get("method").and_then(Value::as_str) {
            self.dispatch_notification(method, &message);
            return;
        }

        if let Some(id) = message.get("id") {
            let request_id = normalize_id(id);
            if let Ok(mut pending) = self.pending.lock() {
                if let Some(sender) = pending.remove(&request_id) {
                    let _ = sender.send(Ok(message));
                }
            }
        }
    }

    fn dispatch_notification(&self, topic: &str, message: &Value) {
        let channels = self
            .subscriptions
            .lock()
            .ok()
            .and_then(|subscriptions| subscriptions.get(topic).cloned());

        if let Some(channels) = channels {
            for (_, channel) in channels {
                let _ = channel.send(message.clone());
            }
        }
    }

    fn handle_disconnect(&self, reason: String) {
        if let Ok(mut session) = self.session.lock() {
            *session = None;
        }

        if let Ok(mut pending) = self.pending.lock() {
            for (_, sender) in pending.drain() {
                let _ = sender.send(Err(reason.clone()));
            }
        }

        let message = serde_json::json!({
            "method": "bridge.disconnected",
            "params": {
                "reason": reason,
            }
        });
        self.dispatch_notification("bridge.disconnected", &message);
    }
}

#[tauri::command]
async fn named_pipe_request(
    state: tauri::State<'_, Arc<NamedPipeBridgeState>>,
    payload: Value,
) -> Result<Value, String> {
    let state = Arc::clone(state.inner());
    tauri::async_runtime::spawn_blocking(move || state.request(payload))
        .await
        .map_err(|error| format!("named pipe bridge task failed: {error}"))?
}

#[tauri::command]
async fn named_pipe_subscribe(
    state: tauri::State<'_, Arc<NamedPipeBridgeState>>,
    topic: String,
    on_event: JsonChannel,
) -> Result<u32, String> {
    let state = Arc::clone(state.inner());
    tauri::async_runtime::spawn_blocking(move || state.subscribe(topic, on_event))
        .await
        .map_err(|error| format!("named pipe subscribe task failed: {error}"))?
}

#[tauri::command]
async fn named_pipe_unsubscribe(
    state: tauri::State<'_, Arc<NamedPipeBridgeState>>,
    subscription_id: u32,
) -> Result<(), String> {
    let state = Arc::clone(state.inner());
    tauri::async_runtime::spawn_blocking(move || state.unsubscribe(subscription_id))
        .await
        .map_err(|error| format!("named pipe unsubscribe task failed: {error}"))?
}

fn writer_loop(
    writer: std::fs::File,
    receiver: mpsc::Receiver<BridgeCommand>,
    state: Arc<NamedPipeBridgeState>,
) {
    let mut writer = BufWriter::new(writer);

    while let Ok(command) = receiver.recv() {
        let result = match command {
            BridgeCommand::Request { payload } => (|| -> Result<(), String> {
                serde_json::to_writer(&mut writer, &payload)
                    .map_err(|error| format!("failed to serialize json-rpc payload: {error}"))?;
                writer
                    .write_all(b"\n")
                    .map_err(|error| format!("failed to write named pipe delimiter: {error}"))?;
                writer
                    .flush()
                    .map_err(|error| format!("failed to flush named pipe payload: {error}"))?;
                Ok(())
            })(),
        };

        if let Err(error) = result {
            state.handle_disconnect(error);
            return;
        }
    }
}

fn reader_loop(reader: std::fs::File, state: Arc<NamedPipeBridgeState>) {
    let mut responses =
        serde_json::Deserializer::from_reader(BufReader::new(reader)).into_iter::<Value>();

    while let Some(result) = responses.next() {
        match result {
            Ok(message) => state.dispatch_incoming(message),
            Err(error) => {
                state.handle_disconnect(format!("failed to decode named pipe response: {error}"));
                return;
            }
        }
    }

    state.handle_disconnect(
        "named pipe response stream ended before any json-rpc envelope was returned".to_string(),
    );
}

fn extract_request_id(payload: &Value) -> Result<String, String> {
    let id = payload
        .get("id")
        .ok_or_else(|| "json-rpc payload missing id".to_string())?;

    Ok(normalize_id(id))
}

fn normalize_id(id: &Value) -> String {
    serde_json::to_string(id).unwrap_or_else(|_| "null".to_string())
}

fn main() {
    tauri::Builder::default()
        .manage(Arc::new(NamedPipeBridgeState::default()))
        .invoke_handler(tauri::generate_handler![
            named_pipe_request,
            named_pipe_subscribe,
            named_pipe_unsubscribe
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
