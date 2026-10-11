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
 const keys=["runtime_controller_sha256","runtime_protocol_sha256","runtime_transport_sha256","agent_runtime_controller_sha256","agent_runtime_protocol_sha256","agent_runtime_transport_sha256","active_host_attestation_sha256"];
 assert.equal(RELEASE_IDENTITY_KEYS.length,25);
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
 const finite=certificate(),finiteActual={...actual};
 for(const k of keys.slice(3)){delete finite.identity[k];delete finiteActual[k];}
 assert.throws(()=>compareRelease(finite,finiteActual,"layer",["required"]),/CERTIFIED_RUNTIME_MISMATCH: agent_runtime_controller_sha256/);
});

const buildScript=path.resolve(path.dirname(fileURLToPath(import.meta.url)),"../../scripts/release-build-identity.py");
const descriptor={version:1,operations:["create","inspect"]};
const controllerSource="import hashlib,json\nPROTOCOL_DESCRIPTOR="+JSON.stringify(descriptor)+"\ndef protocol_sha256():\n return hashlib.sha256(json.dumps(PROTOCOL_DESCRIPTOR,sort_keys=True,separators=(',',':')).encode()).hexdigest()\n";
const controllerSources={finite_controller:"cdo_runtime_controller.py",agent_controller:"cdo_agent_runtime.py",owned_runtime:"cdo_runtime_owned.py",coordinator:"cdo_runtime_coordinator.py",attestation:"cdo_runtime_attestation.py",broker:"cdo_broker.py"};
const transportSources={backend_client:"cdo_runtime_backend.py",http_transport:"cdo_runtime_transport.py",sdk_relay:"cdo_sdk_relay.py",sdk_state:"cdo_sdk_state.py"};
const agentSource="import hashlib,json\nfrom pathlib import Path\nimport cdo_runtime_backend as backend\nCONTROLLER_SOURCE_FILES="+JSON.stringify(controllerSources)+"\nTRANSPORT_SOURCE_FILES="+JSON.stringify(transportSources)+"\ndef identity(files):\n values={name:hashlib.sha256((Path(__file__).parent / file).read_bytes()).hexdigest() for name,file in files.items()}\n return hashlib.sha256(json.dumps(values,sort_keys=True,separators=(',',':')).encode()).hexdigest()\ndef controller_identity():\n return identity(CONTROLLER_SOURCE_FILES)\ndef transport_identity():\n return identity(TRANSPORT_SOURCE_FILES)\ndef agent_protocol_sha256():\n return backend.protocol_sha256()\n";
function installedBuild(root:string,cwd=root):{identity:Record<string,string>;installed_files:Record<string,string>;installed_symlinks:Record<string,string>} {
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
   "runner/bin/cdo_agent_runtime.py":agentSource,"runner/bin/cdo_runtime_backend.py":controllerSource,
   "runner/bin/cdo_runtime_owned.py":"owned runtime bytes","runner/bin/cdo_runtime_coordinator.py":"coordinator bytes",
   "runner/bin/cdo_runtime_attestation.py":"# attestation bytes\n","runner/bin/cdo_sdk_relay.py":"SDK relay bytes","runner/bin/cdo_sdk_state.py":"SDK state bytes",
   "runner/node_modules/@openai/codex-sdk/package.json":JSON.stringify({version:"0.144.6"}),
  };
  for(const [name,bytes] of Object.entries(contents)){mkdirSync(path.dirname(path.join(root,name)),{recursive:true});writeFileSync(path.join(root,name),bytes);}
  const shadow=path.join(root,"shadow");mkdirSync(shadow);
  writeFileSync(path.join(shadow,"cdo_runtime_controller.py"),"raise RuntimeError('SHADOW_MODULE_LOADED')\n");
  writeFileSync(path.join(shadow,"cdo_agent_runtime.py"),"raise RuntimeError('SHADOW_MODULE_LOADED')\n");
  writeFileSync(path.join(shadow,"cdo_runtime_backend.py"),"raise RuntimeError('SHADOW_MODULE_LOADED')\n");
  writeFileSync(path.join(shadow,"cdo_runtime_attestation.py"),"raise RuntimeError('SHADOW_MODULE_LOADED')\n");
  const build=installedBuild(root,shadow);
  for(const [key,name] of [["runtime_controller_sha256","runner/bin/cdo_runtime_controller.py"],["runtime_transport_sha256","runner/bin/cdo_runtime_transport.py"]]){
   const digest=createHash("sha256").update(readFileSync(path.join(root,name))).digest("hex");
   assert.equal(build.identity[key],digest);assert.equal(build.installed_files[name],digest);
  }
  assert.equal(build.identity.runtime_protocol_sha256,createHash("sha256").update(JSON.stringify({operations:descriptor.operations,version:descriptor.version})).digest("hex"));
  assert.equal(Object.keys(build.identity).length,20);
  assert.equal(build.identity.active_host_attestation_sha256,undefined);
  const composite=(sources:Record<string,string>)=>createHash("sha256").update(JSON.stringify(Object.fromEntries(Object.entries(sources).sort(([a],[b])=>a.localeCompare(b)).map(([name,file])=>[name,build.installed_files["runner/bin/"+file]])))).digest("hex");
  assert.equal(build.identity.agent_runtime_controller_sha256,composite(controllerSources));
  assert.equal(build.identity.agent_runtime_transport_sha256,composite(transportSources));
  assert.equal(build.identity.agent_runtime_protocol_sha256,build.identity.runtime_protocol_sha256);
  assert.equal(build.identity.runner_manifest_sha256,createHash("sha256").update(JSON.stringify({installed_files:build.installed_files,installed_symlinks:build.installed_symlinks})).digest("hex"));
  const alias=path.join(root,"runner/bin/trusted-alias");
  symlinkSync("cdo_runtime_backend.py",alias);const firstLink=installedBuild(root);
  rmSync(alias);symlinkSync("cdo_runtime_controller.py",alias);const secondLink=installedBuild(root);
  assert.deepEqual(firstLink.installed_files,secondLink.installed_files);
  assert.notEqual(firstLink.identity.runner_manifest_sha256,secondLink.identity.runner_manifest_sha256);
  rmSync(alias);
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
  writeFileSync(path.join(root,"runner/bin/cdo_runtime_backend.py"),controllerSource.replace('"version":1','"version":2'));
  const changedAgentProtocol=installedBuild(root);
  assert.notEqual(changedAgentProtocol.identity.agent_runtime_protocol_sha256,build.identity.agent_runtime_protocol_sha256);
  assert.notEqual(changedAgentProtocol.identity.agent_runtime_transport_sha256,build.identity.agent_runtime_transport_sha256);
  writeFileSync(path.join(root,"runner/bin/cdo_runtime_backend.py"),contents["runner/bin/cdo_runtime_backend.py"]);
  for(const name of ["runner/bin/cdo_runtime_controller.py","runner/bin/cdo_runtime_transport.py","runner/bin/cdo_runtime_backend.py","runner/bin/cdo_runtime_owned.py","runner/bin/cdo_sdk_relay.py","runner/bin/cdo_sdk_state.py"]){
   rmSync(path.join(root,name));assert.throws(()=>installedBuild(root),new RegExp(path.basename(name)));
   symlinkSync(path.join(shadow,"cdo_runtime_controller.py"),path.join(root,name));
   assert.throws(()=>installedBuild(root),new RegExp(path.basename(name)));
   rmSync(path.join(root,name));writeFileSync(path.join(root,name),contents[name]);
  }
  writeFileSync(path.join(root,"runner/bin/cdo_runtime_controller.py"),controllerSource+"\ndef protocol_sha256():\n return 'UNKNOWN'\n");
  assert.throws(()=>installedBuild(root),/RUNTIME_PROTOCOL_IDENTITY_MISMATCH/);
 }finally{rmSync(root,{recursive:true,force:true});}
});
