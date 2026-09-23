"use client";

import type { ArchitectureTarget } from "@/lib/schemas";
import type { TargetChangeDraft } from "./types";

function pretty(value: unknown) { return JSON.stringify(value, null, 2); }

// Raw IDs and JSON intentionally live in this opt-in disclosure only.  The
// normal visual workflow never renders this component's content.
export function TargetAdvancedJSON({ target, changes, result }: { target?: ArchitectureTarget; changes: TargetChangeDraft[]; result?: unknown }) {
  return <details aria-label="Advanced target debug information"><summary>Advanced / debug</summary><p className="muted">Raw proposal payloads, identifiers and API responses are available for diagnostics only.</p><details><summary>Target payload</summary><pre>{pretty(target || null)}</pre></details><details><summary>Change payload</summary><pre>{pretty(changes)}</pre></details>{result !== undefined && <details><summary>Lifecycle result</summary><pre>{pretty(result)}</pre></details>}</details>;
}
