"use client";

import { useEffect, useMemo, useState } from "react";
import { Background, Controls, MiniMap, ReactFlow, type Edge, type Node } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import type { ArchitectureCatalog, ArchitectureCatalogOperation, ArchitectureCatalogRelation, ArchitectureCatalogService } from "@/lib/schemas";

const operationLabels: Record<ArchitectureCatalogOperation["manifest"]["type"], string> = {
  http: "HTTP endpoint", nats_request_reply: "NATS request/reply", nats_event_subscriber: "NATS/event subscriber", worker: "Worker/background consumer", scheduled: "Scheduled operation",
};

async function getMermaid(path: string): Promise<string> {
  const response = await fetch(path, { headers: { Accept: "text/plain" }, cache: "no-store" });
  if (!response.ok) throw new Error(`Mermaid unavailable: HTTP ${response.status}`);
  return response.text();
}

function statement(value: { value: string; confidence: number }) { return <span>{value.value || "unknown"} <small>({Math.round(value.confidence * 100)}%)</small></span>; }

function EvidenceList({ values }: { values: Array<{ source_path: string; symbol?: string; start_line?: number; end_line?: number }> }) {
  if (!values.length) return <span>unknown</span>;
  return <span className="architecture-evidence-list">{values.map((value, index) => {
    const location = `${value.source_path}${value.symbol ? `#${value.symbol}` : ""}${value.start_line ? `:${value.start_line}` : ""}`;
    const href = value.source_path ? `vscode://file/${value.source_path.replace(/^\//, "")}${value.start_line ? `:${value.start_line}` : ""}` : undefined;
    return <span key={`${location}-${index}`}>{index > 0 ? ", " : ""}{href ? <a href={href} title="Open source evidence" target="_blank" rel="noreferrer">{location}</a> : location}</span>;
  })}</span>;
}

function operationIdentity(operation: ArchitectureCatalogOperation) {
  const identity = operation.manifest.identity;
  if (identity.http) return `${identity.http.method} ${identity.http.path}`;
  if (identity.nats) return `${identity.nats.subject} · ${identity.nats.role}`;
  if (identity.scheduled) return `${identity.scheduled.name} · ${identity.scheduled.schedule}`;
  return identity.worker?.name ?? "unknown";
}

function CopyMermaid({ value }: { value?: string }) {
  const [copied, setCopied] = useState(false);
  if (!value) return null;
  return <div className="architecture-mermaid-actions"><button className="button" onClick={async () => { await navigator.clipboard?.writeText(value); setCopied(true); }}>{copied ? "Скопировано" : "Скопировать Mermaid"}</button><button className="button" onClick={() => { const blob = new Blob([value], { type: "text/vnd.mermaid; charset=utf-8" }); const url = URL.createObjectURL(blob); const anchor = document.createElement("a"); anchor.href = url; anchor.download = "architecture.mmd"; anchor.click(); URL.revokeObjectURL(url); }}>Скачать .mmd</button></div>;
}

type DiagramNode = { id: string; label: string };
function parseMermaid(source: string): { nodes: DiagramNode[]; edges: Array<[string, string]> } {
  const nodes = new Map<string, DiagramNode>(); const edges: Array<[string, string]> = [];
  for (const line of source.split(/\r?\n/)) {
    const node = line.match(/(?:^|\s)([A-Za-z_][\w-]*)\s*[\[({]([^\]})]+)[\])}]/); if (node) nodes.set(node[1], { id: node[1], label: node[2].replace(/<br\s*\/?>(\s*)/gi, " ").trim() });
    const edge = line.match(/([A-Za-z_][\w-]*)\s*[-.]+>\s*([A-Za-z_][\w-]*)/); if (edge) edges.push([edge[1], edge[2]]);
  }
  for (const [from, to] of edges) { if (!nodes.has(from)) nodes.set(from, { id: from, label: from }); if (!nodes.has(to)) nodes.set(to, { id: to, label: to }); }
  return { nodes: [...nodes.values()], edges };
}

function MermaidGraphic({ source }: { source: string }) {
  const graph = useMemo(() => parseMermaid(source), [source]);
  if (!graph.nodes.length) return <p className="muted">Графическое представление недоступно для этого Mermaid. Исходник сохранён ниже.</p>;
  const width = Math.max(560, Math.min(900, graph.nodes.length * 190)); const positions = new Map(graph.nodes.map((node, index) => [node.id, { x: 24 + (index % 4) * 210, y: 32 + Math.floor(index / 4) * 92 }]));
  return <svg className="architecture-mermaid-svg" role="img" aria-label="Rendered Mermaid architecture diagram" viewBox={`0 0 ${width} ${Math.max(140, Math.ceil(graph.nodes.length / 4) * 92)}`}><defs><marker id="architecture-arrow" markerWidth="8" markerHeight="8" refX="7" refY="3" orient="auto"><path d="M0,0 L0,6 L7,3 z" fill="currentColor" /></marker></defs>{graph.edges.map(([from, to], index) => { const a = positions.get(from); const b = positions.get(to); if (!a || !b) return null; return <line key={`${from}-${to}-${index}`} x1={a.x + 72} y1={a.y + 24} x2={b.x + 72} y2={b.y + 24} stroke="currentColor" markerEnd="url(#architecture-arrow)" opacity=".65" />; })}{graph.nodes.map(node => { const position = positions.get(node.id)!; return <g key={node.id}><rect x={position.x} y={position.y} width="145" height="48" rx="8" fill="var(--surface)" stroke="var(--primary)" /><text x={position.x + 72} y={position.y + 28} textAnchor="middle" fontSize="11" fill="currentColor">{node.label.slice(0, 24)}</text></g>; })}</svg>;
}

function MermaidPreview({ query }: { query: UseQueryResult<string> }) {
  if (query.isLoading) return <p className="muted">Загружается Mermaid…</p>;
  if (query.error) return <p className="warning">{query.error.message}</p>;
  if (!query.data) return <p className="muted">Mermaid не сгенерирован.</p>;
  return <><CopyMermaid value={query.data} /><MermaidGraphic source={query.data} /><details><summary>Исходник Mermaid</summary><pre style={{ overflowX: "auto", maxHeight: 360, padding: 12, background: "var(--surface-soft)", borderRadius: 8, whiteSpace: "pre" }}>{query.data}</pre></details></>;
}

function Completeness({ value }: { value: ArchitectureCatalog["platform"]["completeness"] }) {
  return <div className="architecture-meta" aria-label="Полнота архитектурного каталога"><span>Сервисов: {value.source_count}</span><span>Покрыто: {value.covered_service_count}</span><span className={value.uncovered_service_count ? "warning" : ""}>Без service manifest: {value.uncovered_service_count}</span><span>Обнаружено operations: {value.total_discovered_operations}</span><span>С manifest: {value.operations_with_manifests}</span><span className={value.operations_missing_manifests ? "warning" : ""}>Без manifest: {value.operations_missing_manifests}</span><span className={value.operations_blocked ? "warning" : ""}>Blocked: {value.operations_blocked}</span><span>HTTP: {value.http_operations}; NATS request/reply: {value.nats_request_reply_operations}; event: {value.event_operations}; worker: {value.worker_operations}; scheduled: {value.scheduled_operations}</span><span className={value.missing_declared_operation_manifest_count ? "warning" : ""}>Declared, but missing: {value.missing_declared_operation_manifest_count}</span><span className={value.manifest_operations_without_discovery ? "warning" : ""}>Manifest without discovery: {value.manifest_operations_without_discovery}</span>{value.operation_kinds.map(item => <span key={item.type}>{operationLabels[item.type]}: {item.count}</span>)}</div>;
}

function relationLabel(relation: ArchitectureCatalogRelation) { return [relation.relation_type, relation.transport || relation.protocol, relation.contract_code || relation.contract].filter(Boolean).join(" · ") || "relation"; }

function PlatformRelationGraph({ services, relations, onService }: { services: ArchitectureCatalogService[]; relations: ArchitectureCatalogRelation[]; onService: (id: string) => void }) {
  const items = useMemo(() => { const ids = new Set(services.map(service => service.source.project_id)); const nodes: Node[] = services.map((service, index) => ({ id: service.source.project_id, position: { x: (index % 4) * 245, y: Math.floor(index / 4) * 130 }, data: { label: `${service.source.project_name}\n${service.manifest?.identity.kind ?? "service"}` }, style: { width: 190, whiteSpace: "pre-line" }, className: "architecture-node" })); const edges: Edge[] = relations.flatMap((relation, index) => { const source = relation.source_project_id; const target = relation.target_project_id; if (!source || !target || !ids.has(source) || !ids.has(target)) return []; return [{ id: relation.id || `${source}-${target}-${index}`, source, target, label: relationLabel(relation), type: "smoothstep" }]; }); return { nodes, edges }; }, [services, relations]);
  if (!relations.length) return <p className="muted">Глобальные relations пока не предоставлены CURRENT catalog. После rescan здесь появится граф взаимодействия.</p>;
  return <div className="graph architecture-platform-graph"><ReactFlow nodes={items.nodes} edges={items.edges} fitView minZoom={0.2} maxZoom={1.7} nodesDraggable nodesConnectable={false} onNodeClick={(_, node) => onService(node.id)} deleteKeyCode={null} aria-label="Global service relations graph"><Background /><Controls showInteractive={false} /><MiniMap zoomable pannable /></ReactFlow></div>;
}

function ServiceDetails({ service, operation, serviceMermaid, operationMermaid }: { service: ArchitectureCatalogService; operation?: ArchitectureCatalogOperation; serviceMermaid: UseQueryResult<string>; operationMermaid: UseQueryResult<string> }) {
  const manifest = service.manifest;
  if (!manifest) return <><h2>{service.source.project_name}</h2><p className="warning">Service manifest отсутствует. CURRENT не дополняется догадками.</p><Completeness value={service.completeness} /></>;
  if (!operation) return <><h2>{manifest.identity.name}</h2><p>{statement(manifest.purpose)}</p><p><strong>Ответственность:</strong> {manifest.responsibilities.length ? manifest.responsibilities.map(item => item.value).join("; ") : "unknown"}</p><p><strong>Capabilities:</strong> {manifest.capabilities.length ? manifest.capabilities.map(item => item.value).join("; ") : "unknown"}</p><p><strong>Owned resources:</strong> {manifest.owned_resources.length ? manifest.owned_resources.map(item => `${item.type}: ${item.name}`).join(", ") : "unknown"}</p><p><strong>Inbound:</strong> {manifest.inbound_interfaces.length ? manifest.inbound_interfaces.map(item => `${item.transport}: ${item.name}`).join(", ") : "unknown"}</p><p><strong>Outbound:</strong> {manifest.outbound_dependencies.length ? manifest.outbound_dependencies.map(item => `${item.transport}: ${item.target}`).join(", ") : "unknown"}</p><p><strong>Contracts:</strong> produced {manifest.produced_contracts.length}; consumed {manifest.consumed_contracts.length}; events published {manifest.published_events.length}; subscribed {manifest.subscribed_events.length}</p><p><small>Evidence: <EvidenceList values={manifest.evidence} /> · confidence {Math.round(manifest.confidence * 100)}%</small></p><Completeness value={service.completeness} /><details open><summary>Service Mermaid</summary><MermaidPreview query={serviceMermaid} /></details></>;
  const value = operation.manifest;
  return <><h2>{operationLabels[value.type]}</h2><p><code>{operationIdentity(operation)}</code></p><p><strong>Trigger:</strong> {statement(value.trigger.description)}</p><p><strong>Business task:</strong> {statement(value.business_task)}</p><p><strong>Input:</strong> path {value.input.path_params.map(item => item.name).join(", ") || "—"}; query {value.input.query.map(item => item.name).join(", ") || "—"}; headers {value.input.headers.map(item => item.name).join(", ") || "—"}; body {value.input.body?.name ?? "—"}</p><p><strong>Access:</strong> audience {value.access.audience.value}; authentication {value.access.authentication.value}; authorization {value.access.authorization.value}; idempotency {value.access.idempotency.value}</p><details open><summary>Business process</summary><ol>{value.business_process.length ? value.business_process.map(item => <li key={item.id}>{statement(item.description)}</li>) : <li>unknown</li>}</ol></details><details><summary>Rules and implementation flow</summary><p><strong>Rules:</strong> {value.business_rules.map(item => item.value).join("; ") || "unknown"}</p><p><strong>Router:</strong> <EvidenceList values={value.implementation.router} /></p><p><strong>Handler:</strong> <EvidenceList values={value.implementation.handler} /></p><p><strong>Use case:</strong> <EvidenceList values={value.implementation.use_cases} /></p><p><strong>Domain:</strong> <EvidenceList values={value.implementation.domain_services} /></p><p><strong>Repository:</strong> <EvidenceList values={value.implementation.repositories} /></p></details><details><summary>Data, interactions and side effects</summary><p><strong>Data access:</strong> {value.data_access.map(item => `${item.access} ${item.resource}`).join(", ") || "unknown"}</p><p><strong>External:</strong> {value.external_interactions.map(item => `${item.transport}: ${item.target}`).join(", ") || "unknown"}</p><p><strong>Side effects:</strong> {value.side_effects.map(item => `${item.type}: ${item.description.value}`).join("; ") || "unknown"}</p><p><strong>Emitted events:</strong> {value.output.emitted_events.map(item => item.code).join(", ") || "—"}</p></details><details><summary>Output and errors</summary><p><strong>Responses:</strong> {value.output.responses.map(item => `${item.status_code}: ${item.description.value}`).join("; ") || "unknown"}</p><p><strong>Result:</strong> {value.output.result?.name ?? "unknown"}</p><p><strong>Errors:</strong> {value.errors.map(item => `${item.code}${item.status_code ? ` (${item.status_code})` : ""}: ${item.description.value}`).join("; ") || "unknown"}</p></details><p><small>Evidence: <EvidenceList values={value.evidence} /> · confidence {Math.round(value.confidence * 100)}%</small></p><details open><summary>Operation Mermaid</summary><MermaidPreview query={operationMermaid} /></details></>;
}

export function ArchitecturePlatform({ catalog }: { catalog: ArchitectureCatalog }) {
  const initial = typeof window === "undefined" ? { service: "", operation: "" } : (() => { const params = new URLSearchParams(window.location.search); return { service: params.get("service") || "", operation: params.get("operation") || "" }; })();
  const [projectID, setProjectID] = useState(initial.service); const [operationID, setOperationID] = useState(initial.operation); const [serviceSearch, setServiceSearch] = useState(""); const [operationSearch, setOperationSearch] = useState(""); const [operationType, setOperationType] = useState("");
  const service = catalog.platform.services.find(item => item.source.project_id === projectID);
  const operations = useMemo(() => { if (!service) return [] as ArchitectureCatalogOperation[]; const seen = new Set<string>(); return [...service.groups.flatMap(group => group.operations), ...service.ungrouped_operations].filter(item => { if (seen.has(item.manifest.id)) return false; seen.add(item.manifest.id); return true; }); }, [service]);
  const visibleServices = useMemo(() => catalog.platform.services.filter(item => !serviceSearch || `${item.source.project_name} ${item.manifest?.purpose.value || ""}`.toLowerCase().includes(serviceSearch.toLowerCase())), [catalog.platform.services, serviceSearch]);
  const visibleOperations = useMemo(() => operations.filter(item => (!operationSearch || `${operationIdentity(item)} ${item.manifest.business_task.value}`.toLowerCase().includes(operationSearch.toLowerCase())) && (!operationType || item.manifest.type === operationType)), [operations, operationSearch, operationType]);
  const operation = operations.find(item => item.manifest.id === operationID);
  const serviceMermaid = useQuery({ queryKey: ["architecture", "platform", projectID, "mermaid"], queryFn: () => getMermaid(`/api/v1/architecture/platform/services/${encodeURIComponent(projectID)}/mermaid`), enabled: Boolean(service?.manifest) && !operation });
  const operationMermaid = useQuery({ queryKey: ["architecture", "platform", projectID, "operations", operationID, "mermaid"], queryFn: () => getMermaid(`/api/v1/architecture/platform/services/${encodeURIComponent(projectID)}/operations/${encodeURIComponent(operationID)}/mermaid`), enabled: Boolean(operation) });
  const platformMermaid = useQuery({ queryKey: ["architecture", "platform", "mermaid"], queryFn: () => getMermaid("/api/v1/architecture/platform/mermaid"), enabled: true });
  useEffect(() => { const onPopState = () => { const params = new URLSearchParams(window.location.search); setProjectID(params.get("service") || ""); setOperationID(params.get("operation") || ""); }; window.addEventListener("popstate", onPopState); return () => window.removeEventListener("popstate", onPopState); }, []);
  useEffect(() => { const params = new URLSearchParams(window.location.search); if (projectID) params.set("service", projectID); else params.delete("service"); if (operationID) params.set("operation", operationID); else params.delete("operation"); window.history.replaceState(null, "", `${window.location.pathname}${params.toString() ? `?${params}` : ""}`); }, [projectID, operationID]);
  const selectService = (id: string) => { setProjectID(id); setOperationID(""); };
  return <section className="architecture-platform" aria-label="Manifest-backed CURRENT architecture"><section className="panel architecture-platform-toolbar"><div className="architecture-meta"><strong>PLATFORM</strong><span>Manifest-backed CURRENT</span><span>{catalog.platform.services.length} services</span></div><div className="architecture-filters"><input aria-label="Поиск сервиса" placeholder="Поиск сервиса или purpose" value={serviceSearch} onChange={event => setServiceSearch(event.target.value)} /><button className="button" onClick={() => { setServiceSearch(""); setOperationSearch(""); setOperationType(""); selectService(""); }}>Сбросить</button></div><Completeness value={catalog.platform.completeness} /></section><section className="panel"><h3>Global service relations</h3><PlatformRelationGraph services={visibleServices} relations={catalog.platform.relations || []} onService={selectService} /><details><summary>Global Mermaid</summary><MermaidPreview query={platformMermaid} /></details></section><section className="architecture-workspace"><aside className="panel architecture-detail"><p className="eyebrow">SERVICES</p><div style={{ display: "grid", gap: 6 }}>{visibleServices.map(item => <button key={item.source.project_id} className="button" aria-pressed={item.source.project_id === projectID} onClick={() => selectService(item.source.project_id)} style={{ textAlign: "left", whiteSpace: "normal" }}>{item.source.project_name} {item.covered ? "" : "· manifest missing"}</button>)}</div>{!visibleServices.length && <p className="muted">Сервисы не найдены.</p>}</aside><aside className="panel architecture-detail">{service ? <><p className="eyebrow">SERVICE · {service.source.project_name}</p><p><small>{service.source.source_current ? "CURRENT source" : "Source requires rescan"}{service.source.is_dirty ? " · dirty checkout" : ""}</small></p>{service.manifest ? <><div className="architecture-filters"><input aria-label="Поиск operation" placeholder="Поиск operation" value={operationSearch} onChange={event => setOperationSearch(event.target.value)} /><select aria-label="Фильтр по типу operation" value={operationType} onChange={event => setOperationType(event.target.value)}><option value="">Все operation types</option>{Object.entries(operationLabels).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</select></div><div style={{ display: "grid", gap: 6 }}>{visibleOperations.map(item => <button key={item.manifest.id} className="button" aria-pressed={item.manifest.id === operationID} onClick={() => setOperationID(item.manifest.id)} style={{ textAlign: "left", whiteSpace: "normal" }}>{operationLabels[item.manifest.type]} · {operationIdentity(item)}</button>)}</div>{!visibleOperations.length && <p className="muted">Operations не найдены.</p>}{service.groups.some(group => group.missing_operation_ids.length) && <p className="warning">Group references missing operation manifests: {service.groups.flatMap(group => group.missing_operation_ids).join(", ")}</p>}</> : null}</> : <><p className="eyebrow">SERVICE</p><h2>Выберите сервис</h2><p>Переходите от PLATFORM к сервису и затем к конкретной operation.</p></>}</aside><aside className="panel architecture-detail">{service ? <ServiceDetails service={service} operation={operation} serviceMermaid={serviceMermaid} operationMermaid={operationMermaid} /> : <><p className="eyebrow">OPERATION</p><h2>Детали появятся здесь</h2><p>Unknown и missing состояния показываются явно, без синтеза архитектурных фактов.</p></>}</aside></section></section>;
}
