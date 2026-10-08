import { ExecutionLifecycle, checkpoint } from "./lifecycle.js";
import { Codex, type CodexOptions } from "@openai/codex-sdk";
import { mkdtempSync, mkdirSync, readdirSync, lstatSync, writeFileSync, symlinkSync, readFileSync, existsSync, realpathSync, rmSync } from "node:fs";
import { execFile, execFileSync, spawnSync, spawn } from "node:child_process";
import path from "node:path";
import os from "node:os";
import { createInterface } from "node:readline";
import { randomUUID } from "node:crypto";
import { MAX_RESULT_BYTES, consumeEvent, parseStructuredResult, type RunRequest, type StreamState } from "./protocol.js";

// Certification is owner-authorized, disposable only. It uses the same broker
// implementation intended for hardened mode; it never enables production.
export function assertCertificationFixture(request: RunRequest, allowed: string | undefined): void {
  if (!allowed || realpathSync(allowed) !== request.working_directory || !path.resolve(allowed).includes("/.cdo-sandbox-certification-") || !request.sandbox_scope) throw new Error("CERTIFICATION_FIXTURE_ADMISSION_DENIED");
  if (request.thread_id) throw new Error("HARDENED_RESUME_NOT_SUPPORTED");
}

export function brokerOnlyConfig(url: string, token: string): NonNullable<CodexOptions["config"]> {
  return {
    model_provider: "openai",
    web_search: "disabled",
    orchestrator: { skills: { enabled: false } },
    tools: { experimental_request_user_input: { enabled: false } },
    features: { goals: false, token_budget: false, current_time_reminder: false, tool_search: false, tool_search_always_defer_mcp_tools: false, shell_tool: false, unified_exec: false, multi_agent: false, multi_agent_v2: false, plugins: false, apps: false, code_mode: false, request_permissions: false, image_generation: false },
    mcp_servers: { cdo: { url, http_headers: { Authorization: "Bearer " + token }, startup_timeout_sec: 30, tool_timeout_sec: 75, required: true, tools: { read_file: { approval_mode: "auto" }, propose_file: { approval_mode: "auto" }, propose_delete: { approval_mode: "auto" }, command: { approval_mode: "auto" } } } },
  };
}

export async function runHardenedCertification(request: RunRequest, emit: (value: unknown) => void): Promise<void> {
  // The host controller is not integrated with the production coordinator yet.
  // Reject before creating control state or entering the legacy Docker path.
  if (["hardened","production"].includes(process.env.CDO_SANDBOX_EXECUTION_MODE || "")) throw new Error("RUNTIME_TRANSPORT_NOT_CERTIFIED");
  assertCertificationFixture(request, process.env.CDO_CERTIFICATION_ROOT);
  if (!process.env.CDO_SDK_STATE_IMAGE) throw new Error("SDK_PRIVATE_STATE_RUNTIME_UNAVAILABLE");
  const root = mkdtempSync(path.join(os.tmpdir(), "cdo-trusted-broker-"));
  const evidence=process.env.CDO_CERTIFICATION_EVIDENCE;
  if(!evidence) {rmSync(root,{recursive:true,force:true});throw new Error("BROKER_TRUSTED_CONFIGURATION_ABSENT");}
  const lifecycle=new ExecutionLifecycle(evidence+".execution.json",request.execution_id || randomUUID(),request.attempt || 1,root);
  let result:unknown;
  try {
    await runInPrivateControl(request,value=>{if((value as {type:string}).type==="result")result=value;else emit(value);},root,lifecycle);
    rmSync(root,{recursive:true,force:true});
    lifecycle.move("completed");emit(result);
  } catch(error) {
    const message=String(error);const unknown=/UNKNOWN|CLEANUP.*(?:UNPROVEN|NOT_PROVEN)/.test(message);
    if(!unknown)rmSync(root,{recursive:true,force:true});
    lifecycle.move(unknown?"infrastructure_unknown":request.execution_deadline && Date.now()>=Date.parse(request.execution_deadline)?"timed_out":/Abort|cancel/i.test(message)?"cancelled":/TIMEOUT|TIMED_OUT/.test(message)?"timed_out":/SANDBOX_LOST/.test(message)?"sandbox_lost":"failed");
    throw error;
  }
}
async function runInPrivateControl(request: RunRequest, emit: (value:unknown)=>void, root: string, lifecycle:ExecutionLifecycle): Promise<void> {
  const home = path.join(root, "codex"); mkdirSync(home, {mode: 0o700});
  const control = path.join(root, "control"); mkdirSync(control, {mode: 0o700});
  const stateDir = path.join(root, "state"); mkdirSync(stateDir, {mode: 0o700});
  const policyFile = path.join(root, "policy.json");
  const broker = path.resolve(path.dirname(new URL(import.meta.url).pathname), "../bin/cdo_broker.py");
  const python = process.env.CDO_BROKER_PYTHON || "python3";
  const evidence = process.env.CDO_CERTIFICATION_EVIDENCE;
  if (!evidence || !process.env.CDO_BROKER_IMAGE) throw new Error("BROKER_TRUSTED_CONFIGURATION_ABSENT");
  const token = randomUUID() + randomUUID();
  writeFileSync(policyFile, JSON.stringify({root: request.working_directory, role: request.sandbox_scope!.profile, phase: "scoped-request", write_paths: request.sandbox_scope!.write_paths, delete_paths: [], execution_id: lifecycle.identity, image: process.env.CDO_BROKER_IMAGE, go_dependency_bundle:process.env.CDO_GO_DEPENDENCY_BUNDLE, go_dependency_manifest_sha256:process.env.CDO_GO_DEPENDENCY_MANIFEST_SHA256, token, state_dir: stateDir, evidence}), {mode: 0o600});
  // No local execution environment means native shell, patch and image tools
  // are not registered by pinned CLI. MCP cdo is the sole filesystem surface.
  writeFileSync(path.join(home, "environments.toml"), "include_local = false\n", {mode: 0o600});
  const credentialHome = process.env.CODEX_HOME;
  if (credentialHome && existsSync(path.join(credentialHome,"auth.json"))) symlinkSync(path.join(credentialHome,"auth.json"), path.join(home,"auth.json"));
  try {execFileSync(python,[broker,"admit",policyFile],{timeout:30000,stdio:["ignore","pipe","pipe"]});}
  catch(error) {const stderr=(error as {stderr?:Buffer}).stderr?.toString() || "";throw new Error(stderr.match(/GO_DEPENDENCY_[A-Z_]+/)?.[0] || "BROKER_ADMISSION_DENIED");}
  lifecycle.move("admitted");
  const server = spawn(python,[broker,"serve-http",policyFile],{env:{...process.env,CDO_SDK_OCI_TRANSPORT:"1"},stdio:["ignore","pipe","ignore"],detached:true});
  const lines = createInterface({input:server.stdout});
  const port = await new Promise<number>((resolve,reject) => {
    const timer=setTimeout(()=>{server.kill("SIGTERM");reject(new Error("BROKER_START_TIMEOUT"));},30000);
    lines.once("line",line=>{clearTimeout(timer);try{const v=JSON.parse(line) as {port:number};if(!Number.isInteger(v.port)) throw new Error("BROKER_PORT_INVALID");resolve(v.port);}catch(e){reject(e);}});
    server.once("exit",()=>{clearTimeout(timer);reject(new Error("BROKER_START_FAILED"));});
  });
  lifecycle.move("running");
  const config = brokerOnlyConfig("http://127.0.0.1:"+port,token);
  const fakeURL = process.env.CDO_CERTIFICATION_PROVIDER_URL;
  if (fakeURL && ["hardened","production"].includes(process.env.CDO_SANDBOX_EXECUTION_MODE || "")) throw new Error("PRODUCTION_PROVIDER_OVERRIDE_DENIED");
  if (fakeURL) {
    const u = new URL(fakeURL); if (u.hostname !== "127.0.0.1") throw new Error("FAKE_PROVIDER_MUST_BE_LOOPBACK");
    config.model_provider = "cdo_fixture";
    config.model_providers = { cdo_fixture: { name: "Deterministic SDK certification fixture", base_url: fakeURL, wire_api: "responses", requires_openai_auth: false } };
  }
  const sdkAdapter = path.resolve(path.dirname(new URL(import.meta.url).pathname), "../bin/cdo_sdk_state.py");
  if (!process.env.CDO_SDK_STATE_IMAGE) throw new Error("SDK_PRIVATE_STATE_RUNTIME_UNAVAILABLE");
  const environment: Record<string,string> = {PATH: process.env.PATH || "/usr/bin:/bin", HOME: home, CODEX_HOME: home, CDO_SDK_STATE_CONTROL: stateDir, CDO_SDK_STATE_IMAGE: process.env.CDO_SDK_STATE_IMAGE, CDO_EXECUTION_ID:lifecycle.identity};
  if (credentialHome && existsSync(path.join(credentialHome,"auth.json"))) environment.CDO_SDK_AUTH_REFERENCE=path.join(credentialHome,"auth.json");
  const abort = new AbortController(); const stop = () => abort.abort();
  server.once("exit",()=>{if(!stoppingBroker)abort.abort(new Error("SANDBOX_LOST"));});
  let stoppingBroker=false;
  process.on("SIGTERM",stop);process.on("SIGINT",stop);
  const parent=process.ppid;
  const parentWatch=setInterval(()=>{try{process.kill(parent,0);if(process.ppid!==parent)stop();}catch{stop();}},100);

  const stopBroker = async () => {
    stoppingBroker=true;
    if(server.exitCode !== null || server.signalCode !== null) return;
    await new Promise<void>(resolve=>{const timer=setTimeout(()=>{server.kill("SIGKILL");resolve();},15000);server.once("exit",()=>{clearTimeout(timer);resolve();});server.kill("SIGTERM");});
  };
  const previousTMPDIR=process.env.TMPDIR; process.env.TMPDIR=control;
  let completed=false;
  try {
    const codex = new Codex({codexPathOverride:sdkAdapter, env: environment, config, apiKey: process.env.CODEX_API_KEY || process.env.OPENAI_API_KEY});
    const thread = codex.startThread({model: request.model, modelReasoningEffort: request.reasoning_effort, workingDirectory: control, skipGitRepoCheck: true, webSearchMode: "disabled", approvalPolicy: "never"});
    const state: StreamState = {};
    const streamed = await thread.runStreamed(request.prompt + "\nUse only cdo broker tools: read_file, propose_file, command. Source paths are repository-relative; command view is readonly and reflects proposals. No native tools are available. Return the requested structured result.", {outputSchema: request.output_schema, signal: abort.signal});
    for await (const event of streamed.events) {
      if (event.type.startsWith("item.") && "item" in event) {
        if (event.item.type === "command_execution" || event.item.type === "file_change" || event.item.type === "web_search") throw new Error("NATIVE_TOOL_SURFACE_REGRESSION");
        if (event.item.type === "mcp_tool_call" && event.item.server !== "cdo") throw new Error("UNTRUSTED_MCP_SURFACE");
      }
      const prior = state.threadId;consumeEvent(state,event);
      if (!prior && state.threadId) emit({type:"thread_started",thread_id:state.threadId});
    }
    if (!state.threadId) throw new Error("HARDENED_THREAD_ABSENT");
    const sdkMetrics = JSON.parse(readFileSync(path.join(stateDir,"sdk-metrics.json"),"utf8"));
    if (sdkMetrics.failure || sdkMetrics.cleanup !== true || sdkMetrics.phase !== "FINAL") throw new Error(sdkMetrics.failure || "SDK_PRIVATE_STATE_CLEANUP_UNPROVEN");
    abort.signal.throwIfAborted();
    lifecycle.move("verification");
    await checkpoint("verification",abort.signal);
    const result = parseStructuredResult(state.finalResponse);
    await checkpoint("before-publication",abort.signal);
    await stopBroker();
    cleanupContainer(stateDir);
    abort.signal.throwIfAborted();
    await new Promise<void>((resolve,reject)=>{execFile(python,[broker,"publish",policyFile],{signal:abort.signal,timeout:30000,maxBuffer:1048576},error=>error?reject(error):resolve());});
    abort.signal.throwIfAborted();
    const audit=JSON.parse(readFileSync(evidence,"utf8")) as Record<string,unknown>;
    audit.sdk_private_state=sdkMetrics;
    audit.storage_budgets=storageBudgets(stateDir,sdkMetrics,Buffer.byteLength(JSON.stringify(result)),true);
    audit.trusted_runner_storage_observed=privateStorage(root);
    writeFileSync(evidence,JSON.stringify(audit),{mode:0o600});
    emit({type:"result",thread_id:state.threadId,result,usage:state.usage});completed=true;
  } catch (error) {
    // Poison the existing durable publication journal before cleanup. No result
    // reaches the orchestrator and this disposable attempt cannot be republished.
    execFileSync(python,[broker,"invalidate",policyFile],{timeout:15000});
    throw error;
  } finally {
    await stopBroker();
    lines.close();
    if(previousTMPDIR===undefined)delete process.env.TMPDIR;else process.env.TMPDIR=previousTMPDIR;
    clearInterval(parentWatch);
    process.off("SIGTERM",stop);process.off("SIGINT",stop);
    cleanupContainer(stateDir);
    const sdkContainerFile=path.join(stateDir,"sdk-container.json");
    if(existsSync(sdkContainerFile)) {
      const name=JSON.parse(readFileSync(sdkContainerFile,"utf8")).container as string;
      if(!/^cdo-sdk-state-[a-f0-9]{32}$/.test(name))throw new Error("SDK_CLEANUP_STATE_INVALID");
      removeAndObserve(name,"SDK_PRIVATE_STATE_CLEANUP_UNPROVEN");
    }
    const sdkFile=path.join(stateDir,"sdk-metrics.json");
    const sdkFailure=existsSync(sdkFile)?JSON.parse(readFileSync(sdkFile,"utf8")):undefined;
    if(sdkFailure)sdkFailure.cleanup=true;
    // Intentionally retain only bounded audit data, never auth/session contents.
    if (!completed && existsSync(path.join(stateDir,"state.json"))) writeFileSync(evidence,JSON.stringify({failure:sdkFailure?.failure || "HARDENED_EXECUTION_FAILED",sdk_private_state:sdkFailure,storage_budgets:storageBudgets(stateDir,sdkFailure,0,false),broker:JSON.parse(readFileSync(path.join(stateDir,"state.json"),"utf8"))}),{mode:0o600});
    const brokerFailure=existsSync(path.join(stateDir,"state.json"))?JSON.parse(readFileSync(path.join(stateDir,"state.json"),"utf8")).failure:undefined;
    rmSync(root,{recursive:true,force:true});
    if(!completed && sdkFailure?.failure)throw new Error(sdkFailure.failure);
    if(!completed && brokerFailure)throw new Error(brokerFailure);
  }
}
function cleanupContainer(stateDir: string): void {
  const stateFile = path.join(stateDir,"state.json");if (!existsSync(stateFile)) return;
  const state = JSON.parse(readFileSync(stateFile,"utf8")) as {container: string};
  if (!/^cdo-sandbox-broker-[a-f0-9]{16}$/.test(state.container)) throw new Error("BROKER_CLEANUP_STATE_INVALID");
  removeAndObserve(state.container,"BROKER_CLEANUP_NOT_PROVEN");
}

function privateStorage(root: string): {bytes:number; files:number} {
 let bytes=0,files=0;
 function walk(dir:string):void {
  for(const name of readdirSync(dir)){const file=path.join(dir,name),st=lstatSync(file);
   if(st.isSymbolicLink())continue;
   if(st.isDirectory())walk(file);else if(st.isFile()){bytes+=st.size;files++;}
  }
 }
 walk(root);return {bytes,files};
}

function storageBudgets(stateDir:string,sdk: {peak:{bytes:number;files:number}} | undefined,resultBytes:number,published:boolean):Record<string,number> {
  if(resultBytes>MAX_RESULT_BYTES)throw new Error("RESULT_STORAGE_LIMIT_EXCEEDED");
  const stateFile=path.join(stateDir,"state.json");
  const broker=JSON.parse(readFileSync(stateFile,"utf8")) as {proposed_bytes:number;proposals:Record<string,{op:string;data?:string}>;events:Array<{resource_before?:{scratch_bytes:number};resource_after?:{scratch_bytes:number}}>};
  const proposalBytes=Object.values(broker.proposals).reduce((sum,v)=>sum+(v.op==="write"?Buffer.from(v.data!,"base64").length:0),0);
  return {SDK_PRIVATE_STATE_BYTES:sdk?.peak.bytes || 0,SDK_PRIVATE_STATE_FILES:sdk?.peak.files || 0,
    SCRATCH_BYTES:Math.max(0,...broker.events.flatMap(e=>[e.resource_before?.scratch_bytes || 0,e.resource_after?.scratch_bytes || 0])),
    STAGING_BYTES:lstatSync(stateFile).size,PROPOSED_PUBLICATION_BYTES:broker.proposed_bytes,PUBLISHED_BYTES:published?proposalBytes:0,RESULT_BYTES:resultBytes};
}

function removeAndObserve(name:string,failure:string):void {
  spawnSync("docker",["rm","-f",name],{stdio:"ignore",timeout:15000});
  const end=Date.now()+15000;
  do {
    const check=spawnSync("docker",["inspect",name],{encoding:"utf8",timeout:15000});
    if(check.status!==0){
      if(/No such (object|container)/i.test(check.stderr || ""))return;
      throw new Error("DOCKER_CONTAINER_STATE_UNKNOWN");
    }
    // A concurrent automatic removal may still be finishing. Presence is known,
    // not cleanup success; bounded polling must prove absence.
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,50);
  }while(Date.now()<end);
  throw new Error(failure);
}
