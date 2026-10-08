import { createHash } from "node:crypto";
import { lstatSync, readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import path from "node:path";

export const RUNTIME_TRANSPORT_IDENTITY_KEYS = ["runtime_controller_sha256", "runtime_protocol_sha256", "runtime_transport_sha256"] as const;
export const RELEASE_IDENTITY_KEYS = ["source_manifest_sha256", "orchestrator_sha256", "runner_manifest_sha256", "runner_image", "command_image", "sdk_image", "broker_sha256", "publisher_sha256", "dependency_preparer_sha256", "reconciliation_sha256", ...RUNTIME_TRANSPORT_IDENTITY_KEYS, "profile_sha256", "role_policy_sha256", "sdk_version", "cli_version", "go_version", "docker_version", "security_policy_sha256", "resource_policy_sha256"] as const;
export interface ReleaseCertification {schema_version:number; result:string; issued_at:string; identity:Record<string,string>; conditions:Record<string,unknown>; roles:string[]; evidence:Record<string,string>;}
export function compareRelease(cert:ReleaseCertification, actual:Record<string,string>, role:string, required:readonly string[]):void {
  if(cert.schema_version!==1 || cert.result!=="PASS" || !Number.isFinite(Date.parse(cert.issued_at)) || !Array.isArray(cert.roles) || !cert.roles.includes(role))throw new Error("PRODUCTION_SANDBOX_NOT_READY: RELEASE_CERTIFICATION_INCOMPLETE");
  if(!cert.conditions || required.some(k=>cert.conditions[k]!==true))throw new Error("PRODUCTION_SANDBOX_NOT_READY: CERTIFICATION_INCOMPLETE");
  if(!cert.evidence || ["sdk_private_state","go_dependencies","lifecycle_temporal","adversarial","real_model_canary"].some(k=>!/^sha256:[a-f0-9]{64}$/.test(cert.evidence[k] || "")))throw new Error("PRODUCTION_SANDBOX_NOT_READY: CERTIFICATION_EVIDENCE_INCOMPLETE");
  for(const k of RELEASE_IDENTITY_KEYS)if(!cert.identity?.[k] || !actual[k] || actual[k]!==cert.identity[k])throw new Error("CERTIFIED_RUNTIME_MISMATCH: "+k);
  for(const k of RUNTIME_TRANSPORT_IDENTITY_KEYS)if(!/^[a-f0-9]{64}$/.test(actual[k]))throw new Error("CERTIFIED_RUNTIME_MISMATCH: "+k);
}
export function authorizeInstalledRelease(role:string, required:readonly string[]):void {
  const file=process.env.CDO_RELEASE_CERTIFICATION; const pin=process.env.CDO_RELEASE_CERTIFICATION_SHA256;
  if(!file || !pin || !/^[a-f0-9]{64}$/.test(pin))throw new Error("PRODUCTION_SANDBOX_NOT_READY: RELEASE_CERTIFICATION_ABSENT");
  const st=lstatSync(file);if(!st.isFile() || st.isSymbolicLink() || st.nlink!==1 || st.size>1048576 || (st.mode&0o222)!==0)throw new Error("CERTIFICATION_STALE: UNTRUSTED_ARTIFACT");
  const bytes=readFileSync(file);if(createHash("sha256").update(bytes).digest("hex")!==pin)throw new Error("CERTIFICATION_STALE: ARTIFACT_HASH");
  const observer=path.resolve(path.dirname(new URL(import.meta.url).pathname),"../bin/cdo_release_identity.py");
  let actual:Record<string,string>;
  try {actual=JSON.parse(execFileSync("python3",[observer],{timeout:60000,maxBuffer:1048576,stdio:["ignore","pipe","pipe"]}).toString());}
  catch {throw new Error("CERTIFIED_RUNTIME_MISMATCH: RUNTIME_IDENTITY_UNKNOWN");}
  compareRelease(JSON.parse(bytes.toString()),actual,role,required);
}
