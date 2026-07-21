import { Codex, type ThreadOptions } from "@openai/codex-sdk";
import {
  agentCommandEnvironment,
  consumeEvent,
  MAX_INPUT_BYTES,
  parseRequest,
  parseStructuredResult,
  sanitizedEnvironment,
  type StreamState,
} from "./protocol.js";

async function main(): Promise<void> {
  const input = await readInput();
  const request = parseRequest(JSON.parse(input) as unknown);
  const apiKey = process.env.CODEX_API_KEY || process.env.OPENAI_API_KEY;
  const commandEnvironment = agentCommandEnvironment(process.env);
  const codex = new Codex({
    apiKey,
    env: sanitizedEnvironment(process.env),
    config: {
      shell_environment_policy: {
        inherit: "none",
        ignore_default_excludes: false,
        set: commandEnvironment,
      },
    },
  });
  const options: ThreadOptions = {
    model: request.model,
    sandboxMode: request.role === "coder" ? "workspace-write" : "read-only",
    workingDirectory: request.working_directory,
    skipGitRepoCheck: false,
    networkAccessEnabled: false,
    webSearchMode: "disabled",
    approvalPolicy: "never",
  };
  const thread = request.thread_id
    ? codex.resumeThread(request.thread_id, options)
    : codex.startThread(options);
  const state: StreamState = { threadId: request.thread_id };
  if (state.threadId) {
    writeLine({ type: "thread_started", thread_id: state.threadId });
  }

  const streamed = await thread.runStreamed(request.prompt, {
    outputSchema: request.output_schema,
  });
  for await (const event of streamed.events) {
    const previousThreadID = state.threadId;
    consumeEvent(state, event);
    if (!previousThreadID && state.threadId) {
      writeLine({ type: "thread_started", thread_id: state.threadId });
    }
  }
  if (!state.threadId) {
    throw new Error("Codex stream did not provide a thread ID");
  }
  writeLine({
    type: "result",
    thread_id: state.threadId,
    result: parseStructuredResult(state.finalResponse),
  });
}

async function readInput(): Promise<string> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of process.stdin) {
    const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    size += buffer.length;
    if (size > MAX_INPUT_BYTES) {
      throw new Error("runner request exceeds the size limit");
    }
    chunks.push(buffer);
  }
  if (size === 0) {
    throw new Error("runner request is empty");
  }
  return Buffer.concat(chunks).toString("utf8");
}

function writeLine(value: unknown): void {
  process.stdout.write(`${JSON.stringify(value)}\n`);
}

main().catch((error: unknown) => {
  const message = error instanceof Error ? error.message : "unknown runner error";
  process.stderr.write(`${JSON.stringify({ type: "error", message })}\n`);
  process.exitCode = 1;
});
