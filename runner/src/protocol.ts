import type { AgentMessageItem, ThreadEvent } from "@openai/codex-sdk";

export const MAX_INPUT_BYTES = 1024 * 1024;
export const MAX_RESULT_BYTES = 512 * 1024;

export type RunnerRole = "coder" | "reviewer" | "analyst";

export interface RunRequest {
  role: RunnerRole;
  thread_id?: string;
  working_directory: string;
  model?: string;
  prompt: string;
  output_schema: Record<string, unknown>;
}

export interface StreamState {
  threadId?: string;
  finalResponse?: string;
}

export function parseRequest(value: unknown): RunRequest {
  if (!isRecord(value)) {
    throw new Error("request must be a JSON object");
  }
  if (value.role !== "coder" && value.role !== "reviewer" && value.role !== "analyst") {
    throw new Error("role must be coder, reviewer, or analyst");
  }
  const request: RunRequest = {
    role: value.role,
    working_directory: requiredString(value, "working_directory"),
    prompt: requiredString(value, "prompt"),
    output_schema: requiredRecord(value, "output_schema"),
  };
  if (value.thread_id !== undefined) {
    request.thread_id = requiredString(value, "thread_id");
  }
  if (value.model !== undefined) {
    request.model = requiredString(value, "model");
  }
  return request;
}

export function consumeEvent(state: StreamState, event: ThreadEvent): void {
  if (event.type === "thread.started") {
    state.threadId = event.thread_id;
    return;
  }
  if (event.type === "item.completed" && event.item.type === "agent_message") {
    state.finalResponse = (event.item as AgentMessageItem).text;
    return;
  }
  if (event.type === "turn.failed") {
    throw new Error(`Codex turn failed: ${event.error.message}`);
  }
  if (event.type === "error") {
    throw new Error(`Codex stream failed: ${event.message}`);
  }
}

export function parseStructuredResult(content: string | undefined): unknown {
  if (!content) {
    throw new Error("Codex returned no final agent response");
  }
  if (Buffer.byteLength(content, "utf8") > MAX_RESULT_BYTES) {
    throw new Error("Codex structured result exceeds the size limit");
  }
  try {
    return JSON.parse(content) as unknown;
  } catch {
    throw new Error("Codex final response is not valid JSON");
  }
}

export function sanitizedEnvironment(source: NodeJS.ProcessEnv): Record<string, string> {
  const allowed = [
    "PATH",
    "HOME",
    "USER",
    "LOGNAME",
    "SHELL",
    "TMPDIR",
    "LANG",
    "LC_ALL",
    "TERM",
    "CI",
    "CODEX_HOME",
    "OPENAI_BASE_URL",
    "HTTP_PROXY",
    "HTTPS_PROXY",
    "NO_PROXY",
  ];
  const environment: Record<string, string> = {};
  for (const key of allowed) {
    if (source[key]) {
      environment[key] = source[key] as string;
    }
  }
  return environment;
}

export function agentCommandEnvironment(source: NodeJS.ProcessEnv): Record<string, string> {
  const allowed = [
    "PATH",
    "HOME",
    "USER",
    "LOGNAME",
    "SHELL",
    "TMPDIR",
    "LANG",
    "LC_ALL",
    "TERM",
    "CI",
  ];
  const environment: Record<string, string> = {};
  for (const key of allowed) {
    if (source[key]) {
      environment[key] = source[key] as string;
    }
  }
  return environment;
}

function requiredString(value: Record<string, unknown>, key: string): string {
  const field = value[key];
  if (typeof field !== "string" || field.trim() === "") {
    throw new Error(`${key} must be a non-empty string`);
  }
  return field;
}

function requiredRecord(value: Record<string, unknown>, key: string): Record<string, unknown> {
  const field = value[key];
  if (!isRecord(field)) {
    throw new Error(`${key} must be a JSON object`);
  }
  return field;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
