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
    prompt: "implement the fixture",
    output_schema: { type: "object" },
  });
  assert.equal(request.role, "coder");
  assert.equal(request.working_directory, "/tmp/worktree");
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

test("collects thread and structured agent response", () => {
  const state: StreamState = {};
  consumeEvent(state, { type: "thread.started", thread_id: "thread-1" });
  consumeEvent(state, {
    type: "item.completed",
    item: { id: "message-1", type: "agent_message", text: '{"status":"completed"}' },
  });
  assert.equal(state.threadId, "thread-1");
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
    CODEX_HOME: "/data/codex",
    OPENAI_API_KEY: "secret",
    DATABASE_URL: "secret",
  });
  assert.deepEqual(environment, { PATH: "/bin", HOME: "/tmp/home" });
});

test("rejects an invalid structured result", () => {
  assert.throws(() => parseStructuredResult("not JSON"), /not valid JSON/);
});
