"use client";

import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { apiGet, apiPost, shortID } from "@/lib/api";
import { architectureTargetListSchema, architectureTargetResponseSchema, type ArchitectureTarget, type ArchitectureTargetChange } from "@/lib/schemas";

type TargetDraftChange = {
  id?: string; scope: "service" | "operation"; action: "add" | "change" | "remove";
  kind: "service" | "dependency" | "operation" | "contract" | "event" | "responsibility" | "business_process";
  affected_service_ids: string[]; affected_operation_ids: string[];
  rationale: { summary: string; details: string }; desired_result: { summary: string; success_criteria: string[] };
  evidence: Array<{ source_path: string; symbol?: string }>; confidence: number; unresolved_areas: Array<{ area: string; reason: string }>;
};

const newDraftChange = (): TargetDraftChange => ({
  scope: "operation", action: "change", kind: "operation", affected_service_ids: [], affected_operation_ids: [],
  rationale: { summary: "", details: "" }, desired_result: { summary: "", success_criteria: [] }, evidence: [], confidence: .8, unresolved_areas: [],
});

export function targetChangeToDraft(change: ArchitectureTargetChange): TargetDraftChange {
  return {
    id: change.id, scope: change.scope === "service" ? "service" : "operation", action: change.action === "add" || change.action === "remove" ? change.action : "change",
    kind: ["service", "dependency", "operation", "contract", "event", "responsibility", "business_process"].includes(change.kind) ? change.kind as TargetDraftChange["kind"] : "operation",
    affected_service_ids: change.affected_service_ids, affected_operation_ids: change.affected_operation_ids,
    rationale: change.rationale, desired_result: change.desired_result, evidence: change.evidence.map(item => ({ source_path: String(item.source_path || ""), symbol: typeof item.symbol === "string" ? item.symbol : undefined })),
    confidence: change.confidence, unresolved_areas: change.unresolved_areas.map(item => ({ area: String(item.area || "unknown"), reason: String(item.reason || "unknown") })),
  };
}

export function targetChangesPayload(changes: TargetDraftChange[]) { return changes.map(change => ({ ...change, evidence: change.evidence.filter(item => item.source_path), unresolved_areas: change.unresolved_areas.filter(item => item.area && item.reason) })); }

export function targetBindingMatches(target: Pick<ArchitectureTarget, "current_fingerprint">, currentFingerprint: string) { return Boolean(currentFingerprint && target.current_fingerprint && target.current_fingerprint === currentFingerprint); }
export function targetDecisionAllowed(target: Pick<ArchitectureTarget, "status" | "current_fingerprint">, currentFingerprint: string) { return target.status === "submitted" && targetBindingMatches(target, currentFingerprint); }

function ids(value: string) { return value.split(",").map(item => item.trim()).filter(Boolean); }
function idempotencyKey() { return globalThis.crypto?.randomUUID?.() || `target-${Date.now()}-${Math.random().toString(36).slice(2)}`; }
function json(value: unknown) { return JSON.stringify(value, null, 2); }

function ChangeEditor({ value, disabled, onChange }: { value: TargetDraftChange; disabled: boolean; onChange: (value: TargetDraftChange) => void }) {
  const set = <K extends keyof TargetDraftChange>(key: K, next: TargetDraftChange[K]) => onChange({ ...value, [key]: next });
  return <section className="panel" style={{ padding: 14, display: "grid", gap: 8 }}>
    <div className="architecture-filters"><label>Scope<select disabled={disabled} value={value.scope} onChange={event => set("scope", event.target.value as TargetDraftChange["scope"])}><option value="operation">operation</option><option value="service">service</option></select></label><label>Action<select disabled={disabled} value={value.action} onChange={event => set("action", event.target.value as TargetDraftChange["action"])}><option value="change">change</option><option value="add">add</option><option value="remove">remove</option></select></label><label>Kind<select disabled={disabled} value={value.kind} onChange={event => set("kind", event.target.value as TargetDraftChange["kind"])}>{["service", "dependency", "operation", "contract", "event", "responsibility", "business_process"].map(kind => <option key={kind}>{kind}</option>)}</select></label></div>
    <label>Service IDs<input disabled={disabled} value={value.affected_service_ids.join(", ")} onChange={event => set("affected_service_ids", ids(event.target.value))} placeholder="course-dev-orchestrator" /></label>
    {value.scope === "operation" && <label>Operation IDs<input disabled={disabled} value={value.affected_operation_ids.join(", ")} onChange={event => set("affected_operation_ids", ids(event.target.value))} placeholder="get-health" /></label>}
    <label>Rationale<input disabled={disabled} value={value.rationale.summary} onChange={event => set("rationale", { ...value.rationale, summary: event.target.value })} /></label>
    <label>Desired result<input disabled={disabled} value={value.desired_result.summary} onChange={event => set("desired_result", { ...value.desired_result, summary: event.target.value, success_criteria: value.desired_result.success_criteria.length ? value.desired_result.success_criteria : [event.target.value] })} /></label>
    <label>Success criteria (one per line)<textarea disabled={disabled} value={value.desired_result.success_criteria.join("\n")} onChange={event => set("desired_result", { ...value.desired_result, success_criteria: event.target.value.split("\n").map(item => item.trim()).filter(Boolean) })} /></label>
    <label>Evidence source path<input disabled={disabled} value={value.evidence[0]?.source_path || ""} onChange={event => set("evidence", [{ source_path: event.target.value }])} placeholder="internal/adapters/http/router.go" /></label>
  </section>;
}

export function ArchitectureTarget({ currentFingerprint }: { currentFingerprint: string }) {
  const cache = useQueryClient(); const [selectedID, setSelectedID] = useState(""); const [changes, setChanges] = useState<TargetDraftChange[]>([newDraftChange()]); const [actor, setActor] = useState("owner"); const [comment, setComment] = useState(""); const [result, setResult] = useState<unknown>();
  const list = useQuery({ queryKey: ["architecture", "targets"], queryFn: () => apiGet("/api/v1/architecture/targets", architectureTargetListSchema) });
  const detail = useQuery({ queryKey: ["architecture", "target", selectedID], enabled: Boolean(selectedID), queryFn: () => apiGet(`/api/v1/architecture/targets/${encodeURIComponent(selectedID)}`, architectureTargetResponseSchema) });
  const target = detail.data;
  useEffect(() => { if (!selectedID && list.data?.items[0]) setSelectedID(list.data.items[0].id); }, [list.data, selectedID]);
  useEffect(() => { if (target) setChanges(target.changes.map(targetChangeToDraft)); }, [target]);
  const refresh = () => { cache.invalidateQueries({ queryKey: ["architecture", "targets"] }); cache.invalidateQueries({ queryKey: ["architecture", "target", selectedID] }); };
  const create = useMutation({ mutationFn: () => apiPost("/api/v1/architecture/targets", { current_fingerprint: currentFingerprint, idempotency_key: idempotencyKey(), changes: targetChangesPayload(changes) }), onSuccess: (value: unknown) => { const proposal = architectureTargetResponseSchema.parse(value); setSelectedID(proposal.id); refresh(); } });
  const save = useMutation({ mutationFn: () => apiPost(`/api/v1/architecture/targets/${encodeURIComponent(selectedID)}/changes`, { expected_revision: target?.revision, current_fingerprint: currentFingerprint, changes: targetChangesPayload(changes) }), onSuccess: refresh });
  const submit = useMutation({ mutationFn: () => apiPost(`/api/v1/architecture/targets/${encodeURIComponent(selectedID)}/submit`, { expected_revision: target?.revision, actor, comment }), onSuccess: refresh });
  const decide = useMutation({ mutationFn: (action: "approve" | "reject" | "request-changes") => apiPost(`/api/v1/architecture/targets/${encodeURIComponent(selectedID)}/${action}`, { expected_revision: target?.revision, expected_fingerprint: target?.fingerprint, actor, comment }), onSuccess: refresh });
  const integrate = useMutation({ mutationFn: () => apiPost(`/api/v1/architecture/targets/${encodeURIComponent(selectedID)}/implementation-plan`, { expected_fingerprint: target?.fingerprint, actor, comment }), onSuccess: setResult });
  const verify = useMutation({ mutationFn: () => apiGet(`/api/v1/architecture/targets/${encodeURIComponent(selectedID)}/verification?expected_fingerprint=${encodeURIComponent(target?.fingerprint || "")}`, z.unknown()), onSuccess: setResult });
  const canEdit = target?.status === "draft" && targetBindingMatches(target, currentFingerprint); const error = create.error || save.error || submit.error || decide.error || integrate.error || verify.error;
  return <section className="panel" style={{ marginTop: 16, display: "grid", gap: 14 }}><header><p className="eyebrow">TARGET / PROPOSAL / DIFF / IMPACT</p><h2>Целевая архитектура</h2><p>Draft хранится отдельно от immutable CURRENT и не меняет manifests или исходный код.</p></header><p>Bound CURRENT: <code>{shortID(currentFingerprint || "unknown")}</code></p><div className="architecture-filters"><label>Actor<input value={actor} onChange={event => setActor(event.target.value)} /></label><label style={{ flex: 1 }}>Comment<input value={comment} onChange={event => setComment(event.target.value)} placeholder="Required for request changes" /></label></div><div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}><button className="primary" disabled={!currentFingerprint || create.isPending} onClick={() => create.mutate()}>Create TARGET draft</button>{list.data?.items.map(item => <button key={item.id} className="button" aria-pressed={item.id === selectedID} onClick={() => setSelectedID(item.id)}>{shortID(item.id)} · {item.status}</button>)}</div>{target && <><p>Status: <strong>{target.status}</strong> · revision {target.revision} · fingerprint <code>{shortID(target.fingerprint)}</code></p><ChangeEditor value={changes[0] || newDraftChange()} disabled={!canEdit} onChange={next => setChanges([next])} /><div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}><button className="button" disabled={!canEdit || save.isPending} onClick={() => save.mutate()}>Save draft</button><button className="primary" disabled={!canEdit || submit.isPending} onClick={() => submit.mutate()}>Submit proposal</button>{targetDecisionAllowed(target, currentFingerprint) && <><button className="primary" disabled={decide.isPending} onClick={() => decide.mutate("approve")}>Approve</button><button className="button" disabled={decide.isPending} onClick={() => decide.mutate("reject")}>Reject</button><button className="button" disabled={!comment.trim() || decide.isPending} onClick={() => decide.mutate("request-changes")}>Request changes</button></>}{target.status === "approved" && <><button className="primary" disabled={integrate.isPending} onClick={() => integrate.mutate()}>Create issue / project plan</button><button className="button" disabled={verify.isPending} onClick={() => verify.mutate()}>Verify CURRENT vs TARGET</button></>}</div><details open><summary>Diff</summary><pre>{json(target.diff)}</pre></details><details><summary>Impact</summary><pre>{json(target.impact)}</pre></details></>}{Boolean(error) && <p className="warning">{error instanceof Error ? error.message : "TARGET request failed"}</p>}{result !== undefined && <details open><summary>S7/S8 result</summary><pre>{json(result)}</pre></details>}</section>;
}
