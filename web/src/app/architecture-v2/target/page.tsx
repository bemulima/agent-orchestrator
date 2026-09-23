"use client";

import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { apiGet, apiPost } from "@/lib/api";
import { ArchitectureV2TargetWorkspace, newTargetChangeDraft, type TargetChangeDraft, type TargetLifecycleAction } from "@/components/architecture-v2-target";
import { architectureCatalogSchema, architectureTargetListSchema, architectureTargetResponseSchema } from "@/lib/schemas";
import { Failure, Loading } from "@/components/page-state";

const unknown = z.unknown();
const key = () => globalThis.crypto?.randomUUID?.() || `v2-target-${Date.now()}`;
const payload = (changes: TargetChangeDraft[]) => changes.map(change => ({ ...change, evidence: change.evidence.filter(item => item.source_path), unresolved_areas: change.unresolved_areas.filter(item => item.area && item.reason) }));

export default function ArchitectureV2TargetPage() {
  const cache = useQueryClient(); const [selected, setSelected] = useState(""); const [changes, setChanges] = useState<TargetChangeDraft[]>([newTargetChangeDraft()]); const [result, setResult] = useState<unknown>(); const [error, setError] = useState(""); const [busy, setBusy] = useState<TargetLifecycleAction>();
  const catalog = useQuery({ queryKey: ["architecture-v2", "platform"], queryFn: () => apiGet("/api/v1/architecture/platform", architectureCatalogSchema) });
  const targets = useQuery({ queryKey: ["architecture", "targets"], queryFn: () => apiGet("/api/v1/architecture/targets", architectureTargetListSchema) });
  const detail = useQuery({ queryKey: ["architecture", "target", selected], enabled: Boolean(selected), queryFn: () => apiGet(`/api/v1/architecture/targets/${encodeURIComponent(selected)}`, architectureTargetResponseSchema) });
  useEffect(() => { if (!selected && targets.data?.items[0]) setSelected(targets.data.items[0].id); }, [selected, targets.data]);
  useEffect(() => { if (detail.data) setChanges(detail.data.changes.map(change => ({ id: change.id, scope: change.scope === "service" ? "service" : "operation", action: change.action === "add" || change.action === "remove" ? change.action : "change", kind: change.kind as TargetChangeDraft["kind"], affected_service_ids: change.affected_service_ids, affected_operation_ids: change.affected_operation_ids, rationale: change.rationale, desired_result: change.desired_result, evidence: change.evidence.map(item => ({ source_path: String(item.source_path || ""), symbol: typeof item.symbol === "string" ? item.symbol : undefined })), confidence: change.confidence, unresolved_areas: change.unresolved_areas.map(item => ({ area: String(item.area || ""), reason: String(item.reason || "") })) }))); }, [detail.data]);
  const refresh = () => { cache.invalidateQueries({ queryKey: ["architecture", "targets"] }); cache.invalidateQueries({ queryKey: ["architecture", "target", selected] }); };
  const lifecycle = async (action: TargetLifecycleAction) => { const target = detail.data; const fingerprint = catalog.data?.fingerprint || ""; setBusy(action); setError(""); try { let value: unknown; if (action === "create") { value = await apiPost("/api/v1/architecture/targets", { current_fingerprint: fingerprint, idempotency_key: key(), changes: payload(changes) }); const created = architectureTargetResponseSchema.parse(value); setSelected(created.id); } else if (!target) throw new Error("Select a TARGET first"); else if (action === "save") value = await apiPost(`/api/v1/architecture/targets/${target.id}/changes`, { expected_revision: target.revision, current_fingerprint: fingerprint, changes: payload(changes) }); else if (action === "submit") value = await apiPost(`/api/v1/architecture/targets/${target.id}/submit`, { expected_revision: target.revision, actor: "owner", comment: "V2 visual proposal" }); else if (["approve", "reject", "request-changes"].includes(action)) value = await apiPost(`/api/v1/architecture/targets/${target.id}/${action}`, { expected_revision: target.revision, expected_fingerprint: target.fingerprint, actor: "owner", comment: "V2 visual decision" }); else if (action === "implementation-plan") value = await apiPost(`/api/v1/architecture/targets/${target.id}/implementation-plan`, { expected_fingerprint: target.fingerprint, actor: "owner", comment: "V2 visual plan request" }); else value = await apiGet(`/api/v1/architecture/targets/${target.id}/verification?expected_fingerprint=${encodeURIComponent(target.fingerprint)}`, unknown); setResult(value); refresh(); } catch (value) { setError(value instanceof Error ? value.message : "TARGET request failed"); } finally { setBusy(undefined); } };
  if (catalog.isLoading || targets.isLoading) return <Loading />; if (catalog.error || !catalog.data) return <Failure error={catalog.error || new Error("CURRENT catalog unavailable")} />;
  return <main className="page architecture-page"><ArchitectureV2TargetWorkspace catalog={catalog.data} currentFingerprint={catalog.data.fingerprint} target={detail.data} changes={changes} onChangesChange={setChanges} onLifecycle={lifecycle} busyAction={busy} lifecycleError={error} lifecycleResult={result} /></main>;
}
