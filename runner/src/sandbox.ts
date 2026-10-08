import { authorizeInstalledRelease } from "./release.js";
import { lstatSync, realpathSync, readFileSync } from "node:fs";
import path from "node:path";
import type { RunRequest } from "./protocol.js";

export interface SandboxScope {
  profile: "contract" | "layer" | "composition" | "reviewer";
  write_paths: string[];
}

export function parseSandboxScope(value: unknown, role: string): SandboxScope {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error("sandbox_scope must be an object");
  const v = value as Record<string, unknown>;
  if (Object.keys(v).some(k => !["profile", "write_paths"].includes(k))) throw new Error("unknown sandbox scope field");
  if (!["contract", "layer", "composition", "reviewer"].includes(v.profile as string)) throw new Error("unsupported sandbox profile");
  if ((v.profile === "reviewer") !== (role !== "coder")) throw new Error("sandbox profile and role mismatch");
  if (!Array.isArray(v.write_paths) || v.write_paths.length > 128 || v.write_paths.some(p => typeof p !== "string")) throw new Error("invalid sandbox write paths");
  const paths = v.write_paths as string[];
  if (v.profile === "reviewer" ? paths.length !== 0 : paths.length === 0 && v.profile !== "layer") throw new Error("invalid writable scope for role");
  if (new Set(paths).size !== paths.length) throw new Error("duplicate sandbox write paths");
  for (const p of paths) {
    const parts = p.split("/");
    if (!p || p.includes("\\") || /[\x00-\x1f\x7f*?\[\]]/.test(p) || parts.some(x => !x || x === "." || x === ".." || [".git", ".codex", ".agents", ".ssh", ".aws"].includes(x) || x === ".env" || x.startsWith(".env."))) throw new Error("unsafe sandbox write path");
    if (v.profile === "composition" && !p.startsWith("cmd/")) throw new Error("composition scope must be explicit cmd files");
  }
  return {profile: v.profile as SandboxScope["profile"], write_paths: [...paths].sort()};
}

export function scopedPermissionConfig(request: RunRequest): Record<string, unknown> {
  if (!request.sandbox_scope) return {};
  const root = realpathSync(request.working_directory);
  if (root !== path.resolve(request.working_directory)) throw new Error("sandbox workspace must be canonical");
  const scope = parseSandboxScope(request.sandbox_scope, request.role);
  for (const protectedName of [".git", ".agents", ".codex"]) {
    try {
      if (lstatSync(path.join(root, protectedName)).isSymbolicLink()) throw new Error("protected readonly input is a symlink");
    } catch (e) {
      if ((e as NodeJS.ErrnoException).code !== "ENOENT") throw e;
    }
  }
  const writes: Record<string, string> = {};
  for (const relative of scope.write_paths) {
    let current = root;
    const parts = relative.split("/");
    for (let i = 0; i < parts.length; i++) {
      current = path.join(current, parts[i]);
      try {
        const st = lstatSync(current);
        if (st.isSymbolicLink() || (i === parts.length - 1 ? !st.isFile() || st.nlink !== 1 : !st.isDirectory())) throw new Error("sandbox scope contains symlink, hardlink or non-file target");
      } catch (e) {
        if ((e as NodeJS.ErrnoException).code !== "ENOENT") throw e;
      }
    }
    writes[path.join(root, relative)] = "write";
  }
  // CLI config overrides are produced by trusted code, not the prompt. The
  // baseline profile still supplies minimal runtime and offline network rules.
  const name = scope.write_paths.length === 0 ? "cdo-read-only" : "cdo-workspace-write";
  // SDK 0.144.6 flattens object keys without quoting path segments. Supply
  // complete dotted TOML keys with explicit quoted file names instead.
  const config: Record<string, unknown> = {};
  for (const [file, access] of Object.entries(writes)) {
    config[`permissions.${name}.filesystem.${JSON.stringify(file)}`] = access;
  }
  for (const file of [".", ".git", ".git/**", ".codex", ".codex/**", ".agents", ".agents/**"]) {
    config[`permissions.${name}.filesystem.":workspace_roots".${JSON.stringify(file)}`] = "read";
  }
  config[`permissions.${name}.network.enabled`] = false;
  return config;
}

export function assertProductionLaunch(request: RunRequest, mode: string | undefined, status?: string): void {
  if (mode === undefined || mode === "canary") return;
  if (mode !== "production" && mode !== "hardened") throw new Error("unknown sandbox execution mode");
  if (request.thread_id) throw new Error("HARDENED_RESUME_NOT_SUPPORTED");
  if (!request.sandbox_scope) throw new Error("production requires explicit trusted sandbox scope");
  let actual: string;
  try {actual = status ?? readFileSync("/proc/self/status", "utf8");}
  catch {throw new Error("PRODUCTION_SANDBOX_ADMISSION_UNAVAILABLE");}
  for (const [key, wanted] of [["Seccomp", "2"], ["NoNewPrivs", "1"], ["CapEff", "0000000000000000"], ["CapBnd", "0000000000000000"]]) {
    if (new RegExp(`^${key}:\\s+${wanted}$`, "m").test(actual) === false) throw new Error(`production sandbox admission denied: ${key}`);
  }
  // Seccomp presence alone cannot prove mounts, quotas, descendants, Git or
  // command-level confinement. Do not promote an unverified candidate runtime.
  authorizeInstalledRelease(request.sandbox_scope.profile, REQUIRED_PRODUCTION_CERTIFICATIONS);
}

export const REQUIRED_PRODUCTION_CERTIFICATIONS = [
  "temporal_cancellation_certified", "hardened_timeout_cleanup", "late_result_rejection", "unknown_runtime_fails_closed", "lifecycle_reconciliation",
  "broker_enabled", "native_commands_disabled", "native_filesystem_disabled",
  "oci_seccomp", "zero_capabilities", "no_new_privileges", "network_policy",
  "role_scope", "bounded_publication", "isolated_scratch", "lifecycle",
  "cumulative_resources", "sdk_adversarial", "real_model_canary",
  "role_compatibility", "resume_fail_closed", "release_binding",
  "trusted_go_dependency_provisioning", "go_dependency_manifest_verified", "go_worker_network_blocked", "go_dependency_input_readonly",
  "sdk_private_state_isolated", "sdk_private_state_bounded", "sdk_private_state_cleanup", "sdk_private_state_source_escape_blocked",
] as const;
export function productionCertificationFailures(certifications: Record<string,unknown>): string[] {
  return REQUIRED_PRODUCTION_CERTIFICATIONS.filter(key => certifications[key] !== true);
}
