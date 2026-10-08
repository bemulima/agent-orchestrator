import test from "node:test";
import assert from "node:assert/strict";
import { brokerOnlyConfig, assertCertificationFixture, runHardenedCertification } from "./hardened.js";
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
