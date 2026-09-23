"use client";

import { useMemo } from "react";
import type { ArchitectureCatalog } from "@/lib/schemas";
import type { TargetChangeDraft } from "./types";
import { targetChangeKinds, targetHumanSummary, targetOperationOptions, targetServiceOptions } from "./target-adapter";

const titleCase = (value: string) => value.replaceAll("_", " ").replace(/\b\w/g, char => char.toUpperCase());

export function TargetChangeEditor({ catalog, value, disabled, onChange }: { catalog: ArchitectureCatalog; value: TargetChangeDraft; disabled?: boolean; onChange: (change: TargetChangeDraft) => void }) {
  const services = useMemo(() => targetServiceOptions(catalog), [catalog]);
  const serviceID = value.affected_service_ids[0] || "";
  const operations = useMemo(() => targetOperationOptions(catalog, serviceID), [catalog, serviceID]);
  const operationID = value.affected_operation_ids[0] || "";
  const update = (patch: Partial<TargetChangeDraft>) => onChange({ ...value, ...patch });
  return <section className="panel" aria-label="Human-readable target change editor" style={{ display: "grid", gap: 12 }}>
    <header><p className="eyebrow">CHANGE</p><h3>{targetHumanSummary(value, catalog) || "Describe a proposed change"}</h3><p>Селекторы показывают имена и архитектурный контекст. Технические IDs не отображаются в обычном workflow.</p></header>
    <div className="architecture-filters"><label>Scope<select disabled={disabled} value={value.scope} onChange={event => update({ scope: event.target.value as TargetChangeDraft["scope"], affected_operation_ids: event.target.value === "service" ? [] : value.affected_operation_ids })}><option value="operation">Operation</option><option value="service">Service</option></select></label><label>Action<select disabled={disabled} value={value.action} onChange={event => update({ action: event.target.value as TargetChangeDraft["action"] })}><option value="add">Add</option><option value="change">Change</option><option value="remove">Remove</option></select></label><label>Change type<select disabled={disabled} value={value.kind} onChange={event => update({ kind: event.target.value as TargetChangeDraft["kind"] })}>{targetChangeKinds.map(kind => <option key={kind} value={kind}>{titleCase(kind)}</option>)}</select></label></div>
    <label>Service<select aria-label="Service" disabled={disabled} value={serviceID} onChange={event => update({ affected_service_ids: event.target.value ? [event.target.value] : [], affected_operation_ids: [] })}><option value="">Select a service…</option>{services.map(service => <option key={service.id} value={service.id}>{service.label} — {service.purpose}</option>)}</select></label>
    {value.scope === "operation" && <label>Operation<select aria-label="Operation" disabled={disabled || !serviceID} value={operationID} onChange={event => update({ affected_operation_ids: event.target.value ? [event.target.value] : [] })}><option value="">Select an operation…</option>{operations.map(operation => <option key={operation.id} value={operation.id}>{operation.transport} · {operation.label} — {operation.businessTask}</option>)}</select></label>}
    <label>What changes?<input disabled={disabled} value={value.rationale.summary} onChange={event => update({ rationale: { ...value.rationale, summary: event.target.value } })} placeholder="Describe the intended architectural change" /></label>
    <label>Why is it needed?<textarea disabled={disabled} value={value.rationale.details} onChange={event => update({ rationale: { ...value.rationale, details: event.target.value } })} placeholder="Business or technical rationale, supported by evidence" /></label>
    <label>Expected result<input disabled={disabled} value={value.desired_result.summary} onChange={event => update({ desired_result: { ...value.desired_result, summary: event.target.value } })} placeholder="Observable result after implementation" /></label>
    <label>Success criteria (one per line)<textarea disabled={disabled} value={value.desired_result.success_criteria.join("\n")} onChange={event => update({ desired_result: { ...value.desired_result, success_criteria: event.target.value.split("\n").map(item => item.trim()).filter(Boolean) } })} /></label>
    <label>Evidence source path<input disabled={disabled} value={value.evidence[0]?.source_path || ""} onChange={event => update({ evidence: event.target.value ? [{ source_path: event.target.value }] : [] })} placeholder="Repository file or manifest evidence" /></label>
  </section>;
}
