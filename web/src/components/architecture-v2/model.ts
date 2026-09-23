/** Presentation-only model for Architecture V2.  It deliberately accepts the
 * CURRENT catalog as JSON: V2 must remain compatible while presentation fields
 * are rolled out independently by the backend. */
export type Json = Record<string, any>;
/** Platform filter only.  It must never imply that an unclassified service is business. */
export type V2Mode = "all" | "hideTechnical";
export type V2Operation = { manifest: Json };
export type V2Service = {
  source: Json;
  manifest?: Json;
  groups?: Json[];
  ungrouped_operations?: V2Operation[];
  completeness?: Json;
};
export type V2Relation = Json;
export type V2Catalog = {
  fingerprint?: string;
  platform?: {
    services?: V2Service[];
    relations?: V2Relation[];
    completeness?: Json;
  };
};

const technicalSuffixes = [
  "-validator",
  "-runtime-validator",
  "-cache-search-validator",
];
const technicalManifestKinds = new Set([
  "nginx_gateway",
  "go_backend_validator",
  "go_runtime_service",
  "go_storage_service",
  "internal_control_plane",
]);
// `ms-gateway` is the one exact, repository-level gateway naming convention;
// it is a deterministic technical role, not a semantic/business inference.
const technicalExactNames = new Set(["ms-gateway"]);

export function text(value: unknown): string {
  if (typeof value === "string") return value;
  if (value && typeof value === "object" && "value" in value)
    return String((value as Json).value || "");
  return "";
}
export function list<T = Json>(value: unknown): T[] {
  return Array.isArray(value) ? (value as T[]) : [];
}
export function serviceName(service: V2Service) {
  return String(
    service.manifest?.identity?.name ||
      service.source?.project_name ||
      "unknown",
  );
}
export function serviceID(service: V2Service) {
  return String(service.source?.project_id || serviceName(service));
}
export function serviceSlug(service: V2Service) {
  return serviceName(service)
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/(^-|-$)/g, "");
}
export function allOperations(service?: V2Service): V2Operation[] {
  if (!service) return [];
  const values = [
    ...list<Json>(service.groups).flatMap((group) =>
      list<V2Operation>(group.operations),
    ),
    ...list<V2Operation>(service.ungrouped_operations),
  ];
  const seen = new Set<string>();
  return values.filter((item) => {
    const id = String(item.manifest?.id || "");
    if (!id || seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}
export function operationID(operation: V2Operation) {
  return String(operation.manifest?.id || "unknown");
}
export function operationSlug(operation: V2Operation) {
  return operationID(operation)
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/(^-|-$)/g, "");
}
export function operationIdentity(operation: V2Operation) {
  const identity = operation.manifest?.identity || {};
  if (identity.http)
    return `${identity.http.method || "HTTP"} ${identity.http.path || "unknown"}`;
  if (identity.nats)
    return `${identity.nats.subject || "NATS"} · ${identity.nats.role || "unknown"}`;
  if (identity.scheduled)
    return `${identity.scheduled.name || "scheduled"} · ${identity.scheduled.schedule || "unknown"}`;
  return identity.worker?.name || "unknown";
}
export function transport(operation: V2Operation) {
  const type = String(operation.manifest?.type || "unknown");
  return (
    (
      {
        http: "HTTP",
        nats_request_reply: "NATS request/reply",
        nats_event_subscriber: "Events",
        worker: "Worker",
        scheduled: "Scheduled",
      } as Json
    )[type] || "Unknown"
  );
}
export function groupOperations(
  service: V2Service,
  by: "capability" | "transport",
) {
  const groups = new Map<string, V2Operation[]>();
  const explicit = new Map<string, string>();
  list<Json>(service.groups).forEach((group) =>
    list<V2Operation>(group.operations).forEach((item) =>
      explicit.set(
        operationID(item),
        String(group.name || "Unclassified capability"),
      ),
    ),
  );
  allOperations(service).forEach((operation) => {
    const key =
      by === "transport"
        ? transport(operation)
        : explicit.get(operationID(operation)) || "Unclassified capability";
    groups.set(key, [...(groups.get(key) || []), operation]);
  });
  return [...groups.entries()]
    .map(([name, operations]) => ({ name, operations }))
    .sort((a, b) => a.name.localeCompare(b.name));
}
/** Evidence-only classification: explicit presentation metadata wins. The few
 * technical rules are exact repository naming conventions, not AI guesses. */
export function classification(
  service: V2Service,
): "business" | "technical" | "unclassified" {
  const explicit = String(
    service.manifest?.presentation?.classification ||
      service.source?.architecture_classification ||
      "",
  ).toLowerCase();
  if (explicit === "business" || explicit === "technical") return explicit;
  const kind = String(service.manifest?.identity?.kind || "").toLowerCase();
  const name = serviceName(service).toLowerCase();
  if (
    technicalManifestKinds.has(kind) ||
    technicalExactNames.has(name) ||
    technicalSuffixes.some((suffix) => name.endsWith(suffix))
  )
    return "technical";
  return "unclassified";
}
export function domain(service: V2Service) {
  return (
    text(
      service.manifest?.presentation?.domain ||
        service.source?.architecture_domain,
    ) || "Unclassified"
  );
}
export function visibleServices(services: V2Service[], mode: V2Mode) {
  return services.filter(
    (service) => mode === "all" || classification(service) !== "technical",
  );
}
export type SemanticNode = {
  id: string;
  type:
    | "startEnd"
    | "inputOutput"
    | "process"
    | "decision"
    | "datastore"
    | "externalService"
    | "eventQueue";
  label: string;
};
export function operationFlow(operation: V2Operation): SemanticNode[] {
  const value = operation.manifest || {};
  const nodes: SemanticNode[] = [
    { id: "start", type: "startEnd", label: "Start" },
    { id: "input", type: "inputOutput", label: operationIdentity(operation) },
    {
      id: "task",
      type: "process",
      label: text(value.business_task) || "unknown business task",
    },
  ];
  if (list(value.business_rules).length || text(value.access?.authorization))
    nodes.push({
      id: "decision",
      type: "decision",
      label: "Validation / rules",
    });
  if (
    list(value.data_access).length ||
    list(value.implementation?.repositories).length
  )
    nodes.push({ id: "data", type: "datastore", label: "Repository / data" });
  if (list(value.external_interactions).length)
    nodes.push({
      id: "external",
      type: "externalService",
      label: "External service",
    });
  if (list(value.output?.emitted_events).length)
    nodes.push({ id: "event", type: "eventQueue", label: "Event / queue" });
  nodes.push(
    {
      id: "output",
      type: "inputOutput",
      label: text(value.output?.responses?.[0]?.description) || "Result",
    },
    { id: "end", type: "startEnd", label: "End" },
  );
  return nodes;
}
