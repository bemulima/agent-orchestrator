import test from "node:test";
import assert from "node:assert/strict";
import { brokerOnlyConfig, assertCertificationFixture, runHardenedCertification, assertRuntimeLease, recordRuntimeFailure } from "./hardened.js";
import type { RunRequest } from "./protocol.js";
test("broker only config disables native environment features and scopes approvals",()=>{
 const c=brokerOnlyConfig("http://127.0.0.1:1234","placeholder-token");
 assert.equal((c.features as Record<string,boolean>).shell_tool,false);
 assert.equal((c.features as Record<string,boolean>).multi_agent,false);
 assert.deepEqual(Object.keys(c.mcp_servers as object),["cdo"]);
 assert.equal(c.web_search,"disabled");
});
test("certification denies missing scope and non-disposable project",()=>{
 const r={role:"coder",working_directory:process.cwd(),prompt:"x",output_schema:{}} as RunRequest;
 assert.throws(()=>assertCertificationFixture(r,process.cwd()),/ADMISSION_DENIED/);
 assert.throws(()=>assertCertificationFixture(r,undefined),/ADMISSION_DENIED/);
});

test("hardened config removes optional metadata and arbitrary skill discovery",()=>{
 const c=brokerOnlyConfig("http://127.0.0.1:1234","placeholder-token");
 assert.deepEqual(c.orchestrator,{skills:{enabled:false}});
 assert.deepEqual(c.tools,{experimental_request_user_input:{enabled:false}});
 assert.equal((c.features as Record<string,boolean>).goals,false);
});

import { mkdtempSync,realpathSync,rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
test("hardened resume fails before model or broker execution",()=>{
 const dir=realpathSync(mkdtempSync(path.join(tmpdir(),".cdo-sandbox-certification-resume-")));
 try {const r={role:"coder",working_directory:dir,prompt:"fixture",output_schema:{},thread_id:"previous-thread",sandbox_scope:{profile:"layer",write_paths:[]}} as RunRequest;
 assert.throws(()=>assertCertificationFixture(r,dir),/HARDENED_RESUME_NOT_SUPPORTED/);
 }finally{rmSync(dir,{recursive:true,force:true});}
});

import { ExecutionLifecycle } from "./lifecycle.js";
test("execution lifecycle forbids terminal revival and skipped verification",()=>{
 const dir=mkdtempSync(path.join(tmpdir(),"cdo-lifecycle-test-"));
 try {
  const state=new ExecutionLifecycle(path.join(dir,"audit.json"),"fixture",1,dir);
  assert.throws(()=>state.move("completed"),/TRANSITION_DENIED/);
  state.move("admitted");state.move("running");state.move("infrastructure_unknown");
  assert.throws(()=>state.move("completed"),/TERMINAL_STATE_IMMUTABLE/);
 }finally{rmSync(dir,{recursive:true,force:true});}
});

import fs from "node:fs";
test("runtime lease binds execution/attempt and rejects expired or insecure state",()=>{
 const dir=realpathSync(mkdtempSync(path.join(tmpdir(),"cdo-lease-test-")));
 try{
  const file=path.join(dir,"lease.json");
  const lease={execution_id:"fixture",attempt:1,expires_at:Date.now()/1000+60,status:"ACTIVE"};
  const save=()=>fs.writeFileSync(file,JSON.stringify(lease),{mode:0o600});save();
  const env={CDO_RUNTIME_LEASE_FILE:file,CDO_RUNTIME_OWNED_CONFIG:"/private/owned.json"};
  assertRuntimeLease("fixture",1,env);
  assert.throws(()=>assertRuntimeLease("other",1,env),/IDENTITY_CHANGED/);
  assert.throws(()=>assertRuntimeLease("fixture",2,env),/IDENTITY_CHANGED/);
  lease.expires_at=Date.now()/1000-1;save();assert.throws(()=>assertRuntimeLease("fixture",1,env),/EXPIRED_OR_INVALIDATED/);
  lease.expires_at=Date.now()/1000+60;save();fs.chmodSync(file,0o644);assert.throws(()=>assertRuntimeLease("fixture",1,env),/INVALID/);
  assert.throws(()=>assertRuntimeLease("fixture",1,{CDO_RUNTIME_OWNED_CONFIG:"/private/owned.json"}),/ABSENT/);
  fs.chmodSync(file,0o600);const link=path.join(dir,"link");fs.symlinkSync(file,link);assert.throws(()=>assertRuntimeLease("fixture",1,{...env,CDO_RUNTIME_LEASE_FILE:link}),/INVALID/);
 }finally{rmSync(dir,{recursive:true,force:true});}
});
import childProcess from "node:child_process";
import { syncBuiltinESMExports } from "node:module";
import { Codex } from "@openai/codex-sdk";
test("production and hardened cannot reach legacy Docker, control state or model execution",async t=>{
 const modes=["production","hardened"];
 const keys=["CDO_SANDBOX_EXECUTION_MODE","CDO_SDK_STATE_IMAGE","CDO_BROKER_IMAGE","CDO_CERTIFICATION_EVIDENCE"];
 const previous=Object.fromEntries(keys.map(k=>[k,process.env[k]]));
 const reached:string[]=[],emitted:unknown[]=[];
 const deny=(name:string)=>()=>{reached.push(name);throw new Error("LEGACY_RUNTIME_REACHED: "+name);};
 try {
  t.mock.method(fs,"mkdtempSync",deny("control state"));
  for(const name of ["execFileSync","execFile","spawn","spawnSync"] as const)t.mock.method(childProcess,name,deny(name));
  t.mock.method(Codex.prototype,"startThread",deny("model"));
  syncBuiltinESMExports();
  process.env.CDO_SDK_STATE_IMAGE="sha256:"+"a".repeat(64);
  process.env.CDO_BROKER_IMAGE="sha256:"+"b".repeat(64);
  process.env.CDO_CERTIFICATION_EVIDENCE="/invalid/cdo-transport-evidence.json";
  const request={role:"coder",working_directory:"/invalid/disposable",prompt:"fixture",output_schema:{},sandbox_scope:{profile:"layer",write_paths:[]}} as RunRequest;
  for(const mode of modes){
   process.env.CDO_SANDBOX_EXECUTION_MODE=mode;
   await assert.rejects(runHardenedCertification(request,value=>emitted.push(value)),/^Error: RUNTIME_TRANSPORT_NOT_CERTIFIED$/);
   await assert.rejects(runHardenedCertification(new Proxy({} as RunRequest,{get(){throw new Error("REQUEST_READ_BEFORE_TRANSPORT_DENIAL");}}),value=>emitted.push(value)),/^Error: RUNTIME_TRANSPORT_NOT_CERTIFIED$/);
  }
  assert.deepEqual(reached,[]);assert.deepEqual(emitted,[]);
 }finally{
  t.mock.restoreAll();syncBuiltinESMExports();
  for(const k of keys)if(previous[k]===undefined)delete process.env[k];else process.env[k]=previous[k];
 }
});
test("host hardened certification preserves disposable fixture admission",async()=>{
 const previousMode=process.env.CDO_SANDBOX_EXECUTION_MODE,previousImage=process.env.CDO_SDK_STATE_IMAGE,previousRoot=process.env.CDO_CERTIFICATION_ROOT;
 const dir=realpathSync(mkdtempSync(path.join(tmpdir(),".cdo-sandbox-certification-transport-test-")));
 try {
  process.env.CDO_SANDBOX_EXECUTION_MODE="hardened-certification";delete process.env.CDO_SDK_STATE_IMAGE;
  process.env.CDO_CERTIFICATION_ROOT=dir;
  const request={role:"coder",working_directory:dir,prompt:"fixture",output_schema:{},sandbox_scope:{profile:"layer",write_paths:[]}} as RunRequest;
  await assert.rejects(runHardenedCertification(request,()=>{}),/SDK_PRIVATE_STATE_RUNTIME_UNAVAILABLE/);
  await assert.rejects(runHardenedCertification({...request,working_directory:path.dirname(dir)},()=>{}),/CERTIFICATION_FIXTURE_ADMISSION_DENIED/);
 }finally{
  if(previousMode===undefined)delete process.env.CDO_SANDBOX_EXECUTION_MODE;else process.env.CDO_SANDBOX_EXECUTION_MODE=previousMode;
  if(previousImage===undefined)delete process.env.CDO_SDK_STATE_IMAGE;else process.env.CDO_SDK_STATE_IMAGE=previousImage;
  if(previousRoot===undefined)delete process.env.CDO_CERTIFICATION_ROOT;else process.env.CDO_CERTIFICATION_ROOT=previousRoot;
  rmSync(dir,{recursive:true,force:true});
 }
});

test("complete runtime keeps broker in owned Node group and temporary state in private root",async t=>{
 const dir=realpathSync(mkdtempSync(path.join(tmpdir(),".cdo-sandbox-certification-owned-group-")));
 const privateRoot=path.join(dir,"private-execution-"+"a".repeat(32));fs.mkdirSync(privateRoot,{mode:0o700});
 const leaseFile=path.join(dir,"lease.json");
 fs.writeFileSync(leaseFile,JSON.stringify({execution_id:"owned-group-fixture",attempt:1,expires_at:Date.now()/1000+60,status:"ACTIVE"}),{mode:0o600});
 fs.writeFileSync(path.join(dir,"host-process-owner.json"),JSON.stringify({version:1,phase:"NODE_RUNNING",no_unknown:true,binding:{execution_id:"owned-group-fixture",attempt:1},node:{pid:process.pid,pgid:process.pid,sid:process.pid,uid:process.geteuid!()}}),{mode:0o600});
 const values:Record<string,string>={CDO_SANDBOX_EXECUTION_MODE:"hardened-certification",CDO_CERTIFICATION_ROOT:dir,CDO_SDK_STATE_IMAGE:"sha256:"+"a".repeat(64),CDO_BROKER_IMAGE:"sha256:"+"b".repeat(64),CDO_CERTIFICATION_EVIDENCE:path.join(dir,"evidence.json"),CDO_RUNTIME_OWNED_CONFIG:path.join(dir,"owned.json"),CDO_RUNTIME_CONTROL_ROOT:dir,CDO_RUNTIME_PRIVATE_ROOT:privateRoot,CDO_RUNTIME_LEASE_FILE:leaseFile,CDO_RUNTIME_APPROVED_COMMANDS_JSON:'[["go","test","./..."]]',CDO_BROKER_PYTHON:realpathSync("/usr/bin/python3")};
 const previous=Object.fromEntries(Object.keys(values).map(k=>[k,process.env[k]]));
 const originalMkdtemp=fs.mkdtempSync;
 let rootParent:string|undefined,detached:boolean|undefined;
 try {
  for(const [key,value] of Object.entries(values))process.env[key]=value;
  t.mock.method(fs,"mkdtempSync",(prefix:string)=>{rootParent=path.dirname(prefix);return originalMkdtemp(prefix);});
  t.mock.method(childProcess,"execFileSync",()=>Buffer.alloc(0));
  t.mock.method(childProcess,"spawn",(...args:unknown[])=>{detached=(args[2] as {detached?:boolean}).detached;throw new Error("MOCKED_BROKER_SPAWN_CAPTURED");});
  t.mock.method(Codex.prototype,"startThread",()=>{throw new Error("MODEL_MUST_NOT_RUN");});
  syncBuiltinESMExports();
  const request={role:"coder",execution_id:"owned-group-fixture",attempt:1,working_directory:dir,prompt:"fixture",output_schema:{},sandbox_scope:{profile:"layer",write_paths:[]}} as RunRequest;
  await assert.rejects(runHardenedCertification(request,()=>{}),/MOCKED_BROKER_SPAWN_CAPTURED/);
  assert.deepEqual({detached,rootParent},{detached:false,rootParent:privateRoot});
 }finally{
  t.mock.restoreAll();syncBuiltinESMExports();
  for(const key of Object.keys(values))if(previous[key]===undefined)delete process.env[key];else process.env[key]=previous[key];
  rmSync(dir,{recursive:true,force:true});
 }
});

test("runtime failure journal is exact-owner bound and cannot assert cleanup or arbitrary causes",()=>{
 const dir=realpathSync(mkdtempSync(path.join(tmpdir(),"cdo-node-outcome-test-")));fs.chmodSync(dir,0o700);
 const binding={execution_id:"outcome-fixture",attempt:1,runtime_context:{plan_id:"fixture"},job_sha256:"a".repeat(64),owned_config_sha256:"b".repeat(64),process_owner:{version:1,controller_id:"c".repeat(64),boot_id:"00000000-0000-4000-8000-000000000001",owner_nonce:"d".repeat(32)}};
 const node={pid:process.pid,pgid:process.pid,sid:process.pid,uid:process.geteuid!()};
 const ledger={version:1,phase:"NODE_RUNNING",no_unknown:true,binding,node};
 const ownerFile=path.join(dir,"host-process-owner.json"),outcomeFile=path.join(dir,"host-node-outcome.json");
 const save=()=>fs.writeFileSync(ownerFile,JSON.stringify(ledger),{mode:0o600});save();
 const env={CDO_RUNTIME_OWNED_CONFIG:path.join(dir,"owned.json"),CDO_RUNTIME_CONTROL_ROOT:dir};
 const request={execution_id:binding.execution_id,attempt:binding.attempt} as RunRequest;
 try {
  recordRuntimeFailure(request,new Error("SDK_PRIVATE_STATE_BYTES_EXCEEDED"),env);
  assert.deepEqual(JSON.parse(fs.readFileSync(outcomeFile,"utf8")),{version:1,binding,node,terminal_cause:"SDK_PRIVATE_STATE_BYTES_EXCEEDED",node_exit_code:1});
  assert.equal(fs.statSync(outcomeFile).mode&0o777,0o600);
  recordRuntimeFailure(request,new Error("untrusted text with SANDBOX_LOST inside"),env);
  assert.equal(JSON.parse(fs.readFileSync(outcomeFile,"utf8")).terminal_cause,"AGENT_PROCESS_FAILED");
  fs.unlinkSync(outcomeFile);
  assert.throws(()=>recordRuntimeFailure({...request,attempt:2},new Error("SANDBOX_LOST"),env),/HOST_NODE_OUTCOME_UNKNOWN/);
  assert.equal(fs.existsSync(outcomeFile),false);
  ledger.node.pid=process.pid+1;save();
  assert.throws(()=>recordRuntimeFailure(request,new Error("SANDBOX_LOST"),env),/HOST_NODE_OUTCOME_UNKNOWN/);
  ledger.node.pid=process.pid;ledger.no_unknown=false;save();
  assert.throws(()=>recordRuntimeFailure(request,new Error("SANDBOX_LOST"),env),/HOST_NODE_OUTCOME_UNKNOWN/);
  ledger.no_unknown=true;save();fs.chmodSync(ownerFile,0o644);
  assert.throws(()=>recordRuntimeFailure(request,new Error("SANDBOX_LOST"),env),/HOST_NODE_OUTCOME_UNKNOWN/);
 }finally{rmSync(dir,{recursive:true,force:true});}
});
