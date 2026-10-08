import { runHardenedCertification } from "./hardened.js";
import { assertProductionLaunch, scopedPermissionConfig } from "./sandbox.js";
import { Codex, type ThreadOptions } from "@openai/codex-sdk";
import {
  agentCommandEnvironment,
  consumeEvent,
  MAX_INPUT_BYTES,
  parseRequest,
  permissionProfileForRole,
  parseStructuredResult,
  sanitizedEnvironment,
  type StreamState,
} from "./protocol.js";

// An orchestrator pipe closing must enter trusted teardown, not terminate Node
// through an unhandled EPIPE before finally blocks can invalidate the attempt.
if(["hardened-certification","hardened","production"].includes(process.env.CDO_SANDBOX_EXECUTION_MODE || ""))
  for(const output of [process.stdout,process.stderr])output.on("error",()=>{process.exitCode=1;process.emit("SIGTERM");});

async function main(): Promise<void> {
  const input = await readInput();
  const request = parseRequest(JSON.parse(input) as unknown);
  if (process.env.CDO_SANDBOX_EXECUTION_MODE === "hardened-certification") {
    await runHardenedCertification(request, value => writeLine({...value as object, execution_id:request.execution_id, attempt:request.attempt}));
    return;
  }
  assertProductionLaunch(request, process.env.CDO_SANDBOX_EXECUTION_MODE);
  if (["hardened","production"].includes(process.env.CDO_SANDBOX_EXECUTION_MODE || "")) {
    await runHardenedCertification(request, value => writeLine({...value as object, execution_id:request.execution_id, attempt:request.attempt}));
    return;
  }
  const scopedPermissions = scopedPermissionConfig(request);
  const apiKey = process.env.CODEX_API_KEY || process.env.OPENAI_API_KEY;
  const commandEnvironment = agentCommandEnvironment(process.env);
  const permissionProfile = request.sandbox_scope?.write_paths.length === 0 ? "cdo-read-only" : permissionProfileForRole(request.role);
  const codex = new Codex({
    apiKey,
    env: sanitizedEnvironment(process.env),
    config: {
      ...scopedPermissions,
      default_permissions: permissionProfile,
      shell_environment_policy: {
        inherit: "none",
        ignore_default_excludes: false,
        set: commandEnvironment,
      },
    },
  });
  const options: ThreadOptions = {
    model: request.model,
    modelReasoningEffort: request.reasoning_effort,
    workingDirectory: request.working_directory,
    skipGitRepoCheck: false,
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
    execution_id:request.execution_id, attempt:request.attempt,
    thread_id: state.threadId,
    result: parseStructuredResult(state.finalResponse),
    usage: state.usage ?? {
      input_tokens: 0,
      cached_input_tokens: 0,
      output_tokens: 0,
      reasoning_output_tokens: 0,
    },
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
