import assert from "node:assert/strict";
import test from "node:test";

import {
  agentCommandEnvironment,
  consumeEvent,
  parseRequest,
  parseStructuredResult,
  sanitizedEnvironment,
  type StreamState,
} from "./protocol.js";

test("parses a bounded coder request", () => {
  const request = parseRequest({
    role: "coder",
    working_directory: "/tmp/worktree",
    model: "gpt-5.6-terra",
    reasoning_effort: "low",
    prompt: "implement the fixture",
    output_schema: { type: "object" },
  });
  assert.equal(request.role, "coder");
  assert.equal(request.working_directory, "/tmp/worktree");
  assert.equal(request.model, "gpt-5.6-terra");
  assert.equal(request.reasoning_effort, "low");
});

test("parses a read-only analyst request", () => {
  const request = parseRequest({
    role: "analyst",
    working_directory: "/tmp/repository",
    prompt: "analyze the fixture",
    output_schema: { type: "object" },
  });
  assert.equal(request.role, "analyst");
});

test("parses dedicated read-only issue and pull-request manager requests", () => {
  for (const role of ["issue-manager", "pull-request-manager"] as const) {
    const request = parseRequest({
      role,
      working_directory: "/tmp/repository",
      prompt: "prepare Russian metadata",
      output_schema: { type: "object" },
    });
    assert.equal(request.role, role);
  }
});

test("parses a read-only operator request", () => {
  const request = parseRequest({
    role: "operator",
    working_directory: "/tmp/repository",
    prompt: "explain current state",
    output_schema: { type: "object" },
  });
  assert.equal(request.role, "operator");
});

test("collects thread and structured agent response", () => {
  const state: StreamState = {};
  consumeEvent(state, { type: "thread.started", thread_id: "thread-1" });
  consumeEvent(state, {
    type: "item.completed",
    item: { id: "message-1", type: "agent_message", text: '{"status":"completed"}' },
  });
  consumeEvent(state, {
    type: "turn.completed",
    usage: { input_tokens: 100, cached_input_tokens: 20, output_tokens: 30, reasoning_output_tokens: 10 },
  });
  assert.equal(state.threadId, "thread-1");
  assert.equal(state.usage?.reasoning_output_tokens, 10);
  assert.deepEqual(parseStructuredResult(state.finalResponse), { status: "completed" });
});

test("does not pass orchestrator secrets to Codex", () => {
  const environment = sanitizedEnvironment({
    PATH: "/bin",
    HOME: "/tmp/home",
    DATABASE_PASSWORD: "secret",
    GITLAB_TOKEN: "secret",
    CODEX_API_KEY: "secret",
  });
  assert.deepEqual(environment, { PATH: "/bin", HOME: "/tmp/home" });
});

test("gives agent commands an explicit secret-free environment", () => {
  const environment = agentCommandEnvironment({
    PATH: "/bin",
    HOME: "/tmp/home",
    GOPATH: "/data/cache/go",
    GOCACHE: "/data/cache/go-build",
    GOMODCACHE: "/data/cache/go-mod",
    npm_config_cache: "/data/cache/npm",
    CODEX_HOME: "/data/codex",
    OPENAI_API_KEY: "secret",
    DATABASE_URL: "secret",
  });
  assert.deepEqual(environment, {
    PATH: "/bin",
    HOME: "/tmp/home",
    GOPATH: "/data/cache/go",
    GOCACHE: "/data/cache/go-build",
    GOMODCACHE: "/data/cache/go-mod",
    npm_config_cache: "/data/cache/npm",
  });
});

test("rejects an invalid structured result", () => {
  assert.throws(() => parseStructuredResult("not JSON"), /not valid JSON/);
});

test("rejects unsupported reasoning effort", () => {
  assert.throws(
    () =>
      parseRequest({
        role: "coder",
        working_directory: "/tmp/worktree",
        reasoning_effort: "ultra",
        prompt: "implement",
        output_schema: { type: "object" },
      }),
    /reasoning_effort is not supported/,
  );
});
