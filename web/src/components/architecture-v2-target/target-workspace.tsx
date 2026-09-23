"use client";

import { useState } from "react";
import type { TargetWorkspaceProps } from "./types";
import { newTargetChangeDraft, targetHumanSummary } from "./target-adapter";
import { TargetAdvancedJSON } from "./target-advanced-json";
import { TargetChangeEditor } from "./target-change-editor";
import { TargetVisualDiff } from "./target-visual-diff";

function bindingMatches(current: string, bound?: string) { return Boolean(current && bound && current === bound); }

export function ArchitectureV2TargetWorkspace({ catalog, currentFingerprint, target, changes, onChangesChange, onLifecycle, busyAction, lifecycleError, lifecycleResult }: TargetWorkspaceProps) {
  const [selectedChange, setSelectedChange] = useState(0);
  const status = target?.status || "new";
  const bound = !target || bindingMatches(currentFingerprint, target.current_fingerprint);
  const canEdit = status === "draft" && bound;
  const selected = changes[selectedChange] || newTargetChangeDraft();
  const updateChange = (next: typeof selected) => onChangesChange(changes.map((change, index) => index === selectedChange ? next : change));
  const addChange = () => { onChangesChange([...changes, newTargetChangeDraft()]); setSelectedChange(changes.length); };
  const invoke = (action: Parameters<NonNullable<typeof onLifecycle>>[0]) => onLifecycle?.(action);
  return <section className="architecture-v2-target" aria-label="TARGET visual workspace" style={{ display: "grid", gap: 16 }}>
    <section className="panel"><header><p className="eyebrow">TARGET DESIGN</p><h2>Визуальная целевая архитектура</h2><p>Изменения проектируются поверх verified CURRENT. Этот workspace не изменяет CURRENT manifests или исходный код.</p></header><div className="architecture-meta"><span>Status: <strong>{status}</strong></span><span>Bound CURRENT: {bound ? "verified" : "does not match"}</span><span>Revision: {target?.revision ?? "not created"}</span></div>{!bound && <p className="warning">TARGET связан с другой CURRENT snapshot. Сохранение и approval отключены до rescan/rebase.</p>}</section>
    <div style={{ display: "grid", gridTemplateColumns: "minmax(220px, .7fr) minmax(0, 2fr)", gap: 16 }}><section className="panel"><p className="eyebrow">CHANGE LIST</p>{changes.map((change, index) => <button key={change.id || `draft-${index}`} className="button" aria-pressed={index === selectedChange} onClick={() => setSelectedChange(index)} style={{ display: "block", width: "100%", textAlign: "left", marginBottom: 8, whiteSpace: "normal" }}>{targetHumanSummary(change, catalog) || `Change ${index + 1}`}</button>)}{canEdit && <button className="button" onClick={addChange}>Add visual change</button>}</section><div style={{ display: "grid", gap: 16 }}><TargetVisualDiff catalog={catalog} changes={changes} /><TargetChangeEditor catalog={catalog} value={selected} disabled={!canEdit} onChange={updateChange} /></div></div>
    <section className="panel"><div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>{!target && <button className="primary" disabled={!currentFingerprint || busyAction === "create"} onClick={() => invoke("create")}>Create TARGET draft</button>}{target && canEdit && <><button className="button" disabled={busyAction === "save"} onClick={() => invoke("save")}>Save draft</button><button className="primary" disabled={busyAction === "submit"} onClick={() => invoke("submit")}>Submit proposal</button></>}{target?.status === "submitted" && bound && <><button className="primary" disabled={busyAction === "approve"} onClick={() => invoke("approve")}>Approve</button><button className="button" disabled={busyAction === "reject"} onClick={() => invoke("reject")}>Reject</button><button className="button" disabled={busyAction === "request-changes"} onClick={() => invoke("request-changes")}>Request changes</button></>}{target?.status === "approved" && <><button className="primary" disabled={busyAction === "implementation-plan"} onClick={() => invoke("implementation-plan")}>Create issue / project plan</button><button className="button" disabled={busyAction === "verify"} onClick={() => invoke("verify")}>Verify CURRENT vs TARGET</button></>}</div>{lifecycleError && <p className="warning">{lifecycleError}</p>}</section>
    <TargetAdvancedJSON target={target} changes={changes} result={lifecycleResult} />
  </section>;
}
