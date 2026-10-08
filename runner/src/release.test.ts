import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {compareRelease, RELEASE_IDENTITY_KEYS, RUNTIME_TRANSPORT_IDENTITY_KEYS, type ReleaseCertification} from "./release.js";
const actual=Object.fromEntries(RELEASE_IDENTITY_KEYS.map(k=>[k,RUNTIME_TRANSPORT_IDENTITY_KEYS.some(digest=>digest===k)?"a".repeat(64):"exact-"+k]));
function certificate():ReleaseCertification{return {schema_version:1,result:"PASS",issued_at:"2026-10-06T00:00:00Z",identity:{...actual},conditions:{required:true},roles:["contract","layer","composition","reviewer"],evidence:Object.fromEntries(["sdk_private_state","go_dependencies","lifecycle_temporal","adversarial","real_model_canary"].map(k=>[k,"sha256:"+"a".repeat(64)]))};}
test("release admits only literal complete evidence and all four certified roles",()=>{
 for(const r of certificate().roles)assert.doesNotThrow(()=>compareRelease(certificate(),actual,r,["required"]));
 for(const v of [false,null,undefined,"PASS","UNKNOWN",1]){const c=certificate();c.conditions.required=v;assert.throws(()=>compareRelease(c,actual,"layer",["required"]));}
 assert.throws(()=>compareRelease(certificate(),actual,"other",["required"]));
 const c=certificate();delete c.evidence.real_model_canary;assert.throws(()=>compareRelease(c,actual,"layer",["required"]));
});
test("every meaningful independent stale identity denies without recertification",()=>{
 for(const k of RELEASE_IDENTITY_KEYS){const c=certificate();assert.throws(()=>compareRelease(c,{...actual,[k]:"changed"},"layer",["required"]),new RegExp("CERTIFIED_RUNTIME_MISMATCH: "+k));assert.equal(c.identity[k],actual[k]);}
});

test("controller, protocol and request transport are mandatory exact SHA256 identities",()=>{
 const keys=["runtime_controller_sha256","runtime_protocol_sha256","runtime_transport_sha256"];
 for(const k of keys){
  assert.ok(RELEASE_IDENTITY_KEYS.some(identity=>identity===k));
  const missingCertificate=certificate();delete missingCertificate.identity[k];
  assert.throws(()=>compareRelease(missingCertificate,actual,"layer",["required"]),new RegExp("CERTIFIED_RUNTIME_MISMATCH: "+k));
  const missingActual={...actual};delete missingActual[k];
  assert.throws(()=>compareRelease(certificate(),missingActual,"layer",["required"]),new RegExp("CERTIFIED_RUNTIME_MISMATCH: "+k));
  assert.throws(()=>compareRelease(certificate(),{...actual,[k]:"b".repeat(64)},"layer",["required"]),new RegExp("CERTIFIED_RUNTIME_MISMATCH: "+k));
  for(const value of ["UNKNOWN","","a".repeat(63),"A".repeat(64),"sha256:"+"a".repeat(64)]){
   const invalid=certificate();invalid.identity[k]=value;
   assert.throws(()=>compareRelease(invalid,{...actual,[k]:value},"layer",["required"]),new RegExp("CERTIFIED_RUNTIME_MISMATCH: "+k));
  }
 }
 const legacy=certificate(),legacyActual={...actual};
 for(const k of keys){delete legacy.identity[k];delete legacyActual[k];}
 assert.throws(()=>compareRelease(legacy,legacyActual,"layer",["required"]),/CERTIFIED_RUNTIME_MISMATCH: runtime_controller_sha256/);
});

const buildScript=path.resolve(path.dirname(fileURLToPath(import.meta.url)),"../../scripts/release-build-identity.py");
const descriptor={version:1,operations:["create","inspect"]};
const controllerSource="import hashlib,json\nPROTOCOL_DESCRIPTOR="+JSON.stringify(descriptor)+"\ndef protocol_sha256():\n return hashlib.sha256(json.dumps(PROTOCOL_DESCRIPTOR,sort_keys=True,separators=(',',':')).encode()).hexdigest()\n";
function installedBuild(root:string,cwd=root):{identity:Record<string,string>;installed_files:Record<string,string>} {
 const code="import json,pathlib,runpy,sys\nfrom unittest.mock import patch\nmodule=runpy.run_path(sys.argv[1])\nwith patch.object(module['subprocess'],'check_output',side_effect=['codex 0.144.6','go version go1.25.1 linux/amd64']):\n print(json.dumps(module['build_identity'](pathlib.Path(sys.argv[2])),sort_keys=True))\n";
 return JSON.parse(execFileSync("python3",["-c",code,buildScript,root],{cwd,env:{...process.env,PYTHONPATH:cwd},encoding:"utf8",stdio:["ignore","pipe","pipe"]}));
}
test("build identity hashes installed controller and transport bytes and canonical protocol",()=>{
 const root=mkdtempSync(path.join(tmpdir(),"cdo-release-inventory-"));
 try {
  const contents:Record<string,string>={
   "course-dev-orchestrator":"binary","release/profile.v1.json":"{}","release/role-policy.json":"{}","release/source-manifest.json":"{}",
   "runner/bin/cdo_broker.py":"broker","runner/bin/cdo_go_dependencies.py":"preparer","runner/bin/cdo_reconcile.py":"reconcile",
   "runner/bin/cdo_runtime_controller.py":controllerSource,"runner/bin/cdo_runtime_transport.py":"request transport bytes",
   "runner/node_modules/@openai/codex-sdk/package.json":JSON.stringify({version:"0.144.6"}),
  };
  for(const [name,bytes] of Object.entries(contents)){mkdirSync(path.dirname(path.join(root,name)),{recursive:true});writeFileSync(path.join(root,name),bytes);}
  const shadow=path.join(root,"shadow");mkdirSync(shadow);
  writeFileSync(path.join(shadow,"cdo_runtime_controller.py"),"raise RuntimeError('SHADOW_MODULE_LOADED')\n");
  const build=installedBuild(root,shadow);
  for(const [key,name] of [["runtime_controller_sha256","runner/bin/cdo_runtime_controller.py"],["runtime_transport_sha256","runner/bin/cdo_runtime_transport.py"]]){
   const digest=createHash("sha256").update(readFileSync(path.join(root,name))).digest("hex");
   assert.equal(build.identity[key],digest);assert.equal(build.installed_files[name],digest);
  }
  assert.equal(build.identity.runtime_protocol_sha256,createHash("sha256").update(JSON.stringify({operations:descriptor.operations,version:descriptor.version})).digest("hex"));
  assert.equal(existsSync(path.join(root,"runner/bin/__pycache__")),false);
  writeFileSync(path.join(root,"runner/bin/cdo_runtime_transport.py"),"changed transport bytes");
  assert.notEqual(installedBuild(root).identity.runtime_transport_sha256,build.identity.runtime_transport_sha256);
  writeFileSync(path.join(root,"runner/bin/cdo_runtime_controller.py"),controllerSource+"# changed controller bytes\n");
  const changedController=installedBuild(root);
  assert.notEqual(changedController.identity.runtime_controller_sha256,build.identity.runtime_controller_sha256);
  assert.equal(changedController.identity.runtime_protocol_sha256,build.identity.runtime_protocol_sha256);
  writeFileSync(path.join(root,"runner/bin/cdo_runtime_controller.py"),controllerSource.replace('"version":1','"version":2'));
  const changedProtocol=installedBuild(root);
  assert.notEqual(changedProtocol.identity.runtime_protocol_sha256,build.identity.runtime_protocol_sha256);
  assert.notEqual(changedProtocol.identity.runtime_controller_sha256,build.identity.runtime_controller_sha256);
  for(const name of ["runner/bin/cdo_runtime_controller.py","runner/bin/cdo_runtime_transport.py"]){
   rmSync(path.join(root,name));assert.throws(()=>installedBuild(root),new RegExp(path.basename(name)));
   symlinkSync(path.join(shadow,"cdo_runtime_controller.py"),path.join(root,name));
   assert.throws(()=>installedBuild(root),new RegExp(path.basename(name)));
   rmSync(path.join(root,name));writeFileSync(path.join(root,name),contents[name]);
  }
  writeFileSync(path.join(root,"runner/bin/cdo_runtime_controller.py"),controllerSource+"\ndef protocol_sha256():\n return 'UNKNOWN'\n");
  assert.throws(()=>installedBuild(root),/RUNTIME_PROTOCOL_IDENTITY_MISMATCH/);
 }finally{rmSync(root,{recursive:true,force:true});}
});
