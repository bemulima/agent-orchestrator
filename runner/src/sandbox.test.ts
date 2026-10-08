import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, mkdirSync, writeFileSync, symlinkSync, linkSync, rmSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { assertProductionLaunch, parseSandboxScope, scopedPermissionConfig } from "./sandbox.js";
import { parseRequest } from "./protocol.js";
const req = (dir: string, scope: unknown) => parseRequest({role: "coder", working_directory: dir, prompt: "fixture", output_schema: {}, sandbox_scope: scope});
test("rejects traversal, absolute, globs, protected files and unknown flags", () => {
  for (const p of ["/etc/passwd", "../outside", "a/../b", "a//b", "./a", "a/", ".git/config", ".codex/config.toml", ".env", "x/.env.local", "a*", "x\\y", "a\u0000b"]) assert.throws(() => parseSandboxScope({profile: "layer", write_paths:[p]}, "coder"), /unsafe/);
  assert.throws(() => parseSandboxScope({profile:"layer", write_paths:["ok"], network:true},"coder"), /unknown/);
});
test("role profiles fail closed and reviewers have no write paths", () => {
  assert.throws(() => parseSandboxScope({profile:"reviewer", write_paths:["ok"]},"reviewer"));
  assert.throws(() => parseSandboxScope({profile:"layer", write_paths:["ok"]},"reviewer"));
  assert.deepEqual(parseSandboxScope({profile:"layer", write_paths:[]},"coder"),{profile:"layer",write_paths:[]});
  assert.throws(() => parseSandboxScope({profile:"composition", write_paths:["internal/usecase/a.go"]},"coder"));
  assert.deepEqual(parseSandboxScope({profile:"reviewer", write_paths:[]},"reviewer"),{profile:"reviewer",write_paths:[]});
});
test("scope config reads workspace and grants only exact owned files", () => {
  const dir=realpathSync(mkdtempSync(path.join(tmpdir(),"cdo-scope-")));
  try {
    mkdirSync(path.join(dir,"internal"));writeFileSync(path.join(dir,"internal/a.go"),"fixture");
    const config=scopedPermissionConfig(req(dir,{profile:"layer",write_paths:["internal/a.go","internal/new_test.go"]}));
    const prefix="permissions.cdo-workspace-write.filesystem.";
    assert.equal(config[prefix+'":workspace_roots"."."'],"read");
    assert.equal(config[prefix+JSON.stringify(path.join(dir,"internal/a.go"))],"write");
    assert.equal(config[prefix+JSON.stringify(path.join(dir,"internal/new_test.go"))],"write");
    assert.equal(config[prefix+JSON.stringify(dir)],undefined);
    assert.equal(config["permissions.cdo-workspace-write.network.enabled"],false);
  } finally {rmSync(dir,{recursive:true,force:true});}
});
test("symlink parent, symlink file and hardlink are denied", () => {
  const dir=realpathSync(mkdtempSync(path.join(tmpdir(),"cdo-scope-")));
  try {
    writeFileSync(path.join(dir,"original"),"fixture");symlinkSync("original",path.join(dir,"link"));symlinkSync("/tmp",path.join(dir,"escape"));linkSync(path.join(dir,"original"),path.join(dir,"hard"));
    for (const p of ["link","escape/file","hard"]) assert.throws(() => scopedPermissionConfig(req(dir,{profile:"layer",write_paths:[p]})), /symlink, hardlink/);
  } finally {rmSync(dir,{recursive:true,force:true});}
});
test("production never admits legacy or uncertified runtime", () => {
  const legacy=parseRequest({role:"coder",working_directory:"/tmp",prompt:"fixture",output_schema:{}});
  assert.throws(() => assertProductionLaunch(legacy,"production",""),/explicit trusted/);
  const scoped=req("/tmp",{profile:"layer",write_paths:["file"]});
  assert.throws(() => assertProductionLaunch(scoped,"production","Seccomp:\t0\n"),/Seccomp/);
  const good="Seccomp:\t2\nNoNewPrivs:\t1\nCapEff:\t0000000000000000\nCapBnd:\t0000000000000000\n";
  assert.throws(() => assertProductionLaunch(scoped,"production",good),/NOT_READY/);
  assert.throws(() => assertProductionLaunch(scoped,"unsafe"),/unknown/);
  assert.doesNotThrow(() => assertProductionLaunch(legacy,"canary"));
});


test("protected readonly input cannot import an external symlink", () => {
  const dir=realpathSync(mkdtempSync(path.join(tmpdir(),"cdo-scope-")));
  try {
    writeFileSync(path.join(dir,"owned"),"fixture");symlinkSync("/outside/credential",path.join(dir,".codex"));
    assert.throws(() => scopedPermissionConfig(req(dir,{profile:"layer",write_paths:["owned"]})),/protected readonly/);
  } finally {rmSync(dir,{recursive:true,force:true});}
});

import { productionCertificationFailures, REQUIRED_PRODUCTION_CERTIFICATIONS } from "./sandbox.js";
test("production certification never promotes unknown or partial evidence",()=>{
 assert.equal(productionCertificationFailures({}).length,REQUIRED_PRODUCTION_CERTIFICATIONS.length);
 assert.ok(productionCertificationFailures({broker_enabled:true}).includes("real_model_canary"));
 assert.ok(productionCertificationFailures({real_model_canary:"PASS"}).includes("real_model_canary"));
});

test("explicit hardened mode cannot fall back to canary",()=>{
 const scoped=req("/tmp",{profile:"layer",write_paths:["file"]});
 const good="Seccomp:\t2\nNoNewPrivs:\t1\nCapEff:\t0000000000000000\nCapBnd:\t0000000000000000\n";
 assert.throws(()=>assertProductionLaunch(scoped,"hardened",good),/PRODUCTION_SANDBOX_NOT_READY/);
});

test("production resume is typed terminal even before runtime inspection",()=>{
 const scoped={...req("/tmp",{profile:"layer",write_paths:["file"]}),thread_id:"old-thread"};
 assert.throws(()=>assertProductionLaunch(scoped,"hardened"),/HARDENED_RESUME_NOT_SUPPORTED/);
});

test("SDK private state has separate admission conditions and never authorizes production",()=>{
 const passed={sdk_private_state_isolated:true,sdk_private_state_bounded:true,sdk_private_state_cleanup:true,sdk_private_state_source_escape_blocked:true};
 const missing=productionCertificationFailures(passed);
 assert.ok(missing.includes("release_binding"));
 assert.ok(!missing.includes("sdk_private_state_bounded"));
 assert.ok(productionCertificationFailures({...passed,sdk_private_state_bounded:"PASS"}).includes("sdk_private_state_bounded"));
});

test("Go dependency evidence does not authorize production",()=>{
 const passed={trusted_go_dependency_provisioning:true,go_dependency_manifest_verified:true,go_worker_network_blocked:true,go_dependency_input_readonly:true};
 const missing=productionCertificationFailures(passed);
 assert.ok(missing.includes("release_binding"));
 assert.ok(!missing.includes("go_dependency_input_readonly"));
 assert.ok(productionCertificationFailures({...passed,go_dependency_manifest_verified:"YES"}).includes("go_dependency_manifest_verified"));
});
