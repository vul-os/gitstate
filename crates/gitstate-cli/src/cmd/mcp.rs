//! `gitstate mcp` — a Model Context Protocol (MCP) stdio server exposing
//! gitstate to any MCP-capable agent host (Claude Code, Cursor, …).
//!
//! Ported from the standalone Go `cmd/gitstate-mcp` binary, folded into
//! `gitstate-cli` per the port plan's product-surface decision (T11 wave 1):
//! one binary, not six. The wire shape (newline-delimited JSON-RPC 2.0 over
//! stdio; stdout carries protocol ONLY; diagnostics go to stderr) ports
//! unchanged — what does NOT port is the transport underneath each tool call.
//!
//! ## Auth (the wave's flagged spike, resolved)
//!
//! Go's bridge held a `GITSTATE_TOKEN` and proxied every tool call over HTTP
//! to a remote, multi-tenant server; the token was the only thing standing
//! between "this MCP client" and "everyone else's org's data". That model has
//! no target here: this process calls `gitstate_daemon::ops` **in-process**
//! against the one local SQLite file, exactly like every other `gitstate`
//! subcommand (`gitstate agent runs`, `gitstate context list`, …). There is
//! no second tenant and no network hop, so there is nothing left for a
//! bearer token to gate.
//!
//! Whoever can launch this process already has the OS-level filesystem
//! permissions to run `gitstate agent log-run` directly — an MCP host spawns
//! it as a local child process, the same way a shell spawns any other CLI.
//! Gating this one entry point with a token while every sibling subcommand
//! stays open would be theater, not security: the real trust boundary is
//! "who can execute a process as this OS user", and the OS draws that
//! boundary once, not per-subcommand.
//!
//! If gitstate ever grows a reason for this bridge to reach a *remote*
//! daemon over HTTP instead of in-process (e.g. a phone client talking home),
//! the reuse point is the daemon's existing `AdminAuth` gate
//! (`GITSTATE_ADMIN_TOKEN` — see `gitstate_daemon::state`), not a new
//! per-tool scope system: that gate already exists, is already tested, and
//! already answers "is this caller allowed to touch this node's data" for
//! every other route (including `/api/agent-runs` — see `routes/agent_runs.rs`
//! in `gitstate-daemon`). A second scoping scheme (Go's per-token
//! `write:agent_runs`) would recreate multi-tenant machinery this product has
//! no second tenant to justify.
//!
//! ## Scope
//!
//! Only `log_agent_run` is wired up in this wave — it is the one Go MCP tool
//! this wave's domain (`store/agent_runs.go`) covers. Go's other five tools
//! (`search_issues`, `get_issue`, `get_pr_context`, `list_issues`,
//! `update_issue_state`) depend on domains this wave explicitly excludes
//! (search/embeddings = a later wave; context_bundle = a later wave) or on
//! work-item mutation with no Rust equivalent yet. They are deliberately not
//! stubbed here — `tools/list` must never advertise a tool that would
//! immediately fail every call.

use std::io::{self, BufRead, Write};

use serde::Deserialize;
use serde_json::{json, Value};

use gitstate_daemon::dto::NewAgentRun;
use gitstate_daemon::{ops, AppState};

const PROTOCOL_VERSION: &str = "2024-11-05";
const SERVER_NAME: &str = "gitstate-mcp";

const PARSE_ERROR: i64 = -32700;
const INVALID_REQUEST: i64 = -32600;
const METHOD_NOT_FOUND: i64 = -32601;

/// An incoming JSON-RPC request or notification (no `id` ⇒ notification, no
/// response is ever sent for it — see `handle_line`).
#[derive(Debug, Default, Deserialize)]
struct RpcRequest {
    #[serde(default)]
    jsonrpc: String,
    id: Option<Value>,
    #[serde(default)]
    method: String,
    #[serde(default)]
    params: Value,
}

/// Run the stdio read/dispatch loop until EOF on stdin. Builds the local
/// `AppState` once up front (same cost, same store, as any other subcommand)
/// and reuses it for every tool call — there is no per-request auth to check.
pub async fn run() -> anyhow::Result<()> {
    eprintln!("gitstate-mcp: starting (stdio transport, in-process — no network, no token)");
    let state = gitstate_daemon::build_state_from_env()?;

    let stdin = io::stdin();
    let mut stdout = io::stdout();
    for line in stdin.lock().lines() {
        let line = line?;
        if line.trim().is_empty() {
            continue;
        }
        if let Some(resp) = handle_line(&state, &line).await {
            stdout.write_all(&serde_json::to_vec(&resp)?)?;
            stdout.write_all(b"\n")?;
            stdout.flush()?;
        }
    }
    Ok(())
}

fn result_response(id: Value, result: Value) -> Value {
    json!({ "jsonrpc": "2.0", "id": id, "result": result })
}

fn error_response(id: Value, code: i64, message: impl Into<String>) -> Value {
    json!({ "jsonrpc": "2.0", "id": id, "error": { "code": code, "message": message.into() } })
}

/// Parse and dispatch one line. Returns `None` for a notification (per
/// JSON-RPC 2.0, no response is sent for those) or a malformed envelope that
/// itself looks like a notification (no readable `id`).
async fn handle_line(state: &AppState, line: &str) -> Option<Value> {
    let req: RpcRequest = match serde_json::from_str(line) {
        Ok(r) => r,
        Err(e) => {
            return Some(error_response(
                Value::Null,
                PARSE_ERROR,
                format!("parse error: invalid JSON: {e}"),
            ))
        }
    };

    let is_notification = req.id.is_none();
    if req.jsonrpc != "2.0" || req.method.is_empty() {
        return if is_notification {
            None
        } else {
            Some(error_response(
                req.id.unwrap_or(Value::Null),
                INVALID_REQUEST,
                "invalid request: expected jsonrpc 2.0 with a method",
            ))
        };
    }

    if is_notification {
        eprintln!("gitstate-mcp: notification: {}", req.method);
        return None;
    }
    let id = req.id.unwrap_or(Value::Null);

    Some(match req.method.as_str() {
        "initialize" => result_response(
            id,
            json!({
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": { "tools": {} },
                "serverInfo": { "name": SERVER_NAME, "version": env!("CARGO_PKG_VERSION") },
            }),
        ),
        "tools/list" => result_response(id, json!({ "tools": [log_agent_run_schema()] })),
        "tools/call" => handle_call_tool(state, id, req.params).await,
        "ping" => result_response(id, json!({})),
        other => error_response(id, METHOD_NOT_FOUND, format!("method not found: {other}")),
    })
}

fn log_agent_run_schema() -> Value {
    json!({
        "name": "log_agent_run",
        "description": "Record an agent run in gitstate: what the agent attempted and the \
            outcome. Feeds the agent-effectiveness flywheel (attribution + estimation). Runs \
            entirely locally, against this machine's own gitstate database — no network call.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "goal": { "type": "string", "description": "What the agent was asked to do (required)." },
                "repo_id": { "type": "string", "description": "Associated repo id." },
                "pr_id": { "type": "string", "description": "Associated pull-request id." },
                "issue_id": { "type": "string", "description": "Associated issue id." },
                "agent_name": { "type": "string", "description": "Name/model of the agent (e.g. claude-opus)." },
                "branch": { "type": "string", "description": "Branch the agent worked on." },
                "tests_passed": { "type": "boolean", "description": "Whether the agent's own tests passed." },
                "human_action": {
                    "type": "string",
                    "enum": ["accepted", "edited", "reverted"],
                    "description": "What the human did with the result."
                },
                "iterations": { "type": "integer", "description": "Number of iterations/turns the agent took." },
                "cost_usd": { "type": "number", "description": "Total cost of the run in USD." },
                "additions": { "type": "integer", "description": "Lines added in the run's diff." },
                "deletions": { "type": "integer", "description": "Lines deleted in the run's diff." },
                "changed_files": { "type": "integer", "description": "Number of files changed in the run's diff." }
            },
            "required": ["goal"]
        }
    })
}

/// Dispatches `tools/call`. Tool-level failures (bad args, a rejected
/// `human_action`, …) are returned in-band as `isError: true`, per the MCP
/// convention — never as a JSON-RPC protocol error, so the host can show the
/// message to the model instead of treating the whole call as transport-dead.
async fn handle_call_tool(state: &AppState, id: Value, params: Value) -> Value {
    let name = params
        .get("name")
        .and_then(Value::as_str)
        .unwrap_or_default();
    let args = params.get("arguments").cloned().unwrap_or(json!({}));

    if name != "log_agent_run" {
        return result_response(id, tool_error(format!("unknown tool: {name}")));
    }

    match call_log_agent_run(state, &args).await {
        Ok(run) => result_response(
            id,
            tool_text(serde_json::to_string_pretty(&run).unwrap_or_else(|_| "{}".to_string())),
        ),
        Err(e) => result_response(id, tool_error(e.to_string())),
    }
}

async fn call_log_agent_run(
    state: &AppState,
    args: &Value,
) -> anyhow::Result<gitstate_core::AgentRun> {
    let goal = args
        .get("goal")
        .and_then(Value::as_str)
        .filter(|s| !s.trim().is_empty())
        .ok_or_else(|| anyhow::anyhow!("missing required argument \"goal\""))?
        .to_string();

    let str_field = |k: &str| args.get(k).and_then(Value::as_str).map(str::to_string);
    let u32_field = |k: &str| args.get(k).and_then(Value::as_u64).map(|n| n as u32);

    let additions = u32_field("additions");
    let deletions = u32_field("deletions");
    let changed_files = u32_field("changed_files");
    let diff_summary = if additions.is_some() || deletions.is_some() || changed_files.is_some() {
        Some(gitstate_core::AgentDiffSummary {
            additions: additions.unwrap_or(0),
            deletions: deletions.unwrap_or(0),
            changed_files: changed_files.unwrap_or(0),
        })
    } else {
        None
    };

    let req = NewAgentRun {
        goal,
        repo_id: str_field("repo_id"),
        pr_id: str_field("pr_id"),
        issue_id: str_field("issue_id"),
        supervisor_id: None,
        agent_name: str_field("agent_name"),
        branch: str_field("branch"),
        diff_summary,
        tests_passed: args.get("tests_passed").and_then(Value::as_bool),
        human_action: str_field("human_action"),
        iterations: u32_field("iterations"),
        cost_usd: args.get("cost_usd").and_then(Value::as_f64),
    };
    Ok(ops::log_agent_run(state, req)?)
}

fn tool_text(text: String) -> Value {
    json!({ "content": [{ "type": "text", "text": text }], "isError": false })
}

fn tool_error(message: String) -> Value {
    json!({ "content": [{ "type": "text", "text": message }], "isError": true })
}

#[cfg(test)]
mod tests {
    use super::*;

    /// One temp-data-dir state, exercised against a sequence of raw
    /// JSON-RPC lines — deliberately a single test function rather than many:
    /// `build_state_from_env` reads the process-global `GITSTATE_DATA_DIR`
    /// env var, so keeping every scenario in one thread avoids a race with
    /// any other test that might set it.
    #[tokio::test]
    async fn mcp_dispatch_covers_the_protocol_envelope_and_the_one_wired_tool() {
        let dir = std::env::temp_dir().join(format!("gitstate-mcp-test-{}", std::process::id()));
        std::env::set_var("GITSTATE_DATA_DIR", &dir);
        let state = gitstate_daemon::build_state_from_env().unwrap();

        // ── malformed JSON is a parse error, not a panic. ──
        let resp = handle_line(&state, "not json").await.unwrap();
        assert_eq!(resp["error"]["code"], PARSE_ERROR);

        // ── a notification (no id) gets no response at all, valid or not. ──
        assert!(handle_line(
            &state,
            r#"{"jsonrpc":"2.0","method":"notifications/initialized"}"#
        )
        .await
        .is_none());

        // ── initialize / tools/list / ping — the protocol envelope. ──
        let resp = handle_line(&state, r#"{"jsonrpc":"2.0","id":1,"method":"initialize"}"#)
            .await
            .unwrap();
        assert_eq!(resp["result"]["protocolVersion"], PROTOCOL_VERSION);
        assert_eq!(resp["result"]["serverInfo"]["name"], SERVER_NAME);

        let resp = handle_line(&state, r#"{"jsonrpc":"2.0","id":2,"method":"tools/list"}"#)
            .await
            .unwrap();
        let tools = resp["result"]["tools"].as_array().unwrap();
        assert_eq!(tools.len(), 1, "only log_agent_run is wired up this wave");
        assert_eq!(tools[0]["name"], "log_agent_run");
        assert_eq!(tools[0]["inputSchema"]["required"][0], "goal");

        let resp = handle_line(&state, r#"{"jsonrpc":"2.0","id":3,"method":"ping"}"#)
            .await
            .unwrap();
        assert_eq!(resp["result"], json!({}));

        // ── an unknown method is a proper JSON-RPC error, not silence. ──
        let resp = handle_line(&state, r#"{"jsonrpc":"2.0","id":4,"method":"nope"}"#)
            .await
            .unwrap();
        assert_eq!(resp["error"]["code"], METHOD_NOT_FOUND);

        // ── tools/call: an unknown tool name is an in-band error, not a crash. ──
        let resp = handle_line(
            &state,
            r#"{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}"#,
        )
        .await
        .unwrap();
        assert_eq!(resp["result"]["isError"], true);
        assert!(resp["result"]["content"][0]["text"]
            .as_str()
            .unwrap()
            .contains("unknown tool"));

        // ── tools/call log_agent_run: missing `goal` is an in-band error. ──
        let resp = handle_line(
            &state,
            r#"{"jsonrpc":"2.0","id":6,"method":"tools/call",
                "params":{"name":"log_agent_run","arguments":{}}}"#,
        )
        .await
        .unwrap();
        assert_eq!(resp["result"]["isError"], true);

        // ── tools/call log_agent_run: a full, valid call round-trips. ──
        let resp = handle_line(
            &state,
            r#"{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{
                "name":"log_agent_run",
                "arguments":{
                    "goal":"fix the login bug","agent_name":"claude-code",
                    "human_action":"accepted","tests_passed":true,
                    "additions":12,"deletions":3,"changed_files":2
                }
            }}"#,
        )
        .await
        .unwrap();
        assert_eq!(resp["result"]["isError"], false);
        let text = resp["result"]["content"][0]["text"].as_str().unwrap();
        let run: Value = serde_json::from_str(text).unwrap();
        assert_eq!(run["goal"], "fix the login bug");
        assert_eq!(run["human_action"], "accepted");
        assert_eq!(run["diff_summary"]["additions"], 12);

        // ── and it actually persisted — visible through the ordinary list path. ──
        let logged =
            ops::list_agent_runs(&state, gitstate_daemon::dto::AgentRunQuery::default()).unwrap();
        assert_eq!(logged.len(), 1);
        assert_eq!(logged[0].goal, "fix the login bug");

        // ── an unrecognised human_action is rejected, in-band, same as the HTTP route. ──
        let resp = handle_line(
            &state,
            r#"{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{
                "name":"log_agent_run",
                "arguments":{"goal":"bad","human_action":"approved"}
            }}"#,
        )
        .await
        .unwrap();
        assert_eq!(resp["result"]["isError"], true);

        std::env::remove_var("GITSTATE_DATA_DIR");
        let _ = std::fs::remove_dir_all(&dir);
    }
}
