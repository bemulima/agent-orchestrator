"use client";

import { useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { apiGet } from "@/lib/api";
import { Failure, Loading } from "@/components/page-state";

const evidence = z.object({ source_path: z.string(), symbol: z.string().optional() }).passthrough();
const processSchema = z.object({ id: z.string(), identity: z.object({ name: z.string() }), goal: z.object({ value: z.string() }), trigger: z.object({ value: z.string() }), outcome: z.object({ value: z.string() }), provenance: z.enum(["owner-authored", "evidence-backed", "candidate/unverified"]), confidence: z.number(), steps: z.array(z.object({ id: z.string(), name: z.string(), description: z.object({ value: z.string() }), service_id: z.string(), operation_id: z.string(), evidence: z.array(evidence).default([]) })).default([]), evidence: z.array(evidence).default([]) }).passthrough();
const processesSchema = z.object({ mode: z.literal("CURRENT"), processes: z.array(processSchema) });

export default function ArchitectureV2ProcessesPage() {
  const query = useQuery({ queryKey: ["architecture-v2", "processes"], queryFn: () => apiGet("/api/v1/architecture/processes", processesSchema) });
  if (query.isLoading) return <Loading />;
  if (query.error || !query.data) return <Failure error={query.error || new Error("Process catalog unavailable")} />;
  return <main className="page architecture-page"><header><p className="eyebrow">CURRENT · BUSINESS PROCESSES</p><h1>Evidence-backed business processes</h1><p>Only version-controlled owner-authored or evidence-backed process manifests appear here. Candidate/unverified processes are excluded from CURRENT.</p></header>{query.data.processes.map(process => <section key={process.id} className="panel"><p className="eyebrow">{process.provenance}</p><h2>{process.identity.name}</h2><p><strong>Goal:</strong> {process.goal.value}</p><p><strong>Trigger:</strong> {process.trigger.value}</p><p><strong>Outcome:</strong> {process.outcome.value}</p><ol>{process.steps.map(step => <li key={step.id}><strong>{step.name}</strong> — {step.description.value}<br /><small>{step.service_id} · {step.operation_id}</small></li>)}</ol><p><small>Evidence: {process.evidence.map(item => item.source_path).join(", ")} · confidence {Math.round(process.confidence * 100)}%</small></p></section>)}{!query.data.processes.length && <section className="panel"><p>No confirmed CURRENT business process has been authored yet.</p></section>}</main>;
}
