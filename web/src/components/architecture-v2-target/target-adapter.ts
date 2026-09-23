import type {
  ArchitectureCatalog,
  ArchitectureCatalogOperation,
  ArchitectureCatalogService,
} from "@/lib/schemas";
import type { TargetChangeDraft } from "./types";

export type TargetServiceOption = {
  id: string;
  label: string;
  purpose: string;
  capabilities: string[];
};

export type TargetOperationOption = {
  id: string;
  serviceID: string;
  label: string;
  transport: string;
  businessTask: string;
};

export type TargetVisualNode = {
  id: string;
  label: string;
  subtitle: string;
  change: "context" | "add" | "change" | "remove";
  operationLabels: string[];
};

export type TargetVisualEdge = {
  id: string;
  source: string;
  target: string;
  label: string;
  transport: string;
};

export type TargetVisualDiffModel = {
  nodes: TargetVisualNode[];
  edges: TargetVisualEdge[];
};

export const targetChangeKinds = [
  "service",
  "dependency",
  "operation",
  "contract",
  "event",
  "responsibility",
  "business_process",
] as const;

export function newTargetChangeDraft(): TargetChangeDraft {
  return {
    scope: "operation",
    action: "change",
    kind: "operation",
    affected_service_ids: [],
    affected_operation_ids: [],
    rationale: { summary: "", details: "" },
    desired_result: { summary: "", success_criteria: [] },
    evidence: [],
    confidence: 0.8,
    unresolved_areas: [],
  };
}

function operationIdentity(operation: ArchitectureCatalogOperation) {
  const identity = operation.manifest.identity;
  if (identity.http) return `${identity.http.method} ${identity.http.path}`;
  if (identity.nats) return `${identity.nats.subject} · ${identity.nats.role}`;
  if (identity.scheduled)
    return identity.scheduled.schedule
      ? `${identity.scheduled.name} · ${identity.scheduled.schedule}`
      : identity.scheduled.name;
  return identity.worker?.name || "Unknown operation";
}

function transportLabel(operation: ArchitectureCatalogOperation) {
  switch (operation.manifest.type) {
    case "http":
      return "HTTP";
    case "nats_request_reply":
      return "NATS request/reply";
    case "nats_event_subscriber":
      return "Event subscriber";
    case "worker":
      return "Worker";
    case "scheduled":
      return "Scheduled";
  }
}

function allOperations(service: ArchitectureCatalogService) {
  const found = new Set<string>();
  return [
    ...service.groups.flatMap((group) => group.operations),
    ...service.ungrouped_operations,
  ].filter((operation) => {
    if (found.has(operation.manifest.id)) return false;
    found.add(operation.manifest.id);
    return true;
  });
}

export function targetServiceOptions(
  catalog: ArchitectureCatalog,
): TargetServiceOption[] {
  return catalog.platform.services
    .map((service) => ({
      id: service.source.project_id,
      label: service.manifest?.identity.name || service.source.project_name,
      purpose: service.manifest?.purpose.value || "Unknown purpose",
      capabilities:
        service.manifest?.capabilities
          ?.map((item) => item.value)
          .filter(Boolean) || [],
    }))
    .sort((a, b) => a.label.localeCompare(b.label));
}

export function targetOperationOptions(
  catalog: ArchitectureCatalog,
  serviceID: string,
): TargetOperationOption[] {
  const service = catalog.platform.services.find(
    (item) => item.source.project_id === serviceID,
  );
  if (!service) return [];
  return allOperations(service)
    .map((operation) => ({
      id: operation.manifest.id,
      serviceID,
      label: operationIdentity(operation),
      transport: transportLabel(operation),
      businessTask:
        operation.manifest.business_task.value || "Unknown business task",
    }))
    .sort((a, b) => a.label.localeCompare(b.label));
}

function changedOperationLabels(
  catalog: ArchitectureCatalog,
  change: TargetChangeDraft,
) {
  const selectedServiceIDs = new Set(change.affected_service_ids);
  const operationServices = catalog.platform.services.filter(
    (service) =>
      selectedServiceIDs.size === 0 ||
      selectedServiceIDs.has(service.source.project_id),
  );
  return change.affected_operation_ids.flatMap((id) => {
    for (const service of operationServices) {
      const operation = allOperations(service).find(
        (item) => item.manifest.id === id,
      );
      if (operation) {
        return [
          `${transportLabel(operation)} · ${operationIdentity(operation)}`,
        ];
      }
    }
    return ["Selected operation (details unavailable in CURRENT catalog)"];
  });
}

function strongestChange(
  changes: TargetChangeDraft[],
): TargetVisualNode["change"] {
  if (changes.some((change) => change.action === "remove")) return "remove";
  if (changes.some((change) => change.action === "add")) return "add";
  return changes.length ? "change" : "context";
}

export function targetVisualDiffModel(
  catalog: ArchitectureCatalog,
  changes: TargetChangeDraft[],
): TargetVisualDiffModel {
  const touched = new Set(
    changes.flatMap((change) => change.affected_service_ids),
  );
  for (const change of changes) {
    const selectedServiceIDs = new Set(change.affected_service_ids);
    for (const service of catalog.platform.services) {
      if (
        (selectedServiceIDs.size === 0 ||
          selectedServiceIDs.has(service.source.project_id)) &&
        allOperations(service).some((operation) =>
          change.affected_operation_ids.includes(operation.manifest.id),
        )
      )
        touched.add(service.source.project_id);
    }
  }
  const relationNeighbors = new Set<string>();
  for (const relation of catalog.platform.relations) {
    if (
      relation.source_project_id &&
      touched.has(relation.source_project_id) &&
      relation.target_project_id
    )
      relationNeighbors.add(relation.target_project_id);
    if (
      relation.target_project_id &&
      touched.has(relation.target_project_id) &&
      relation.source_project_id
    )
      relationNeighbors.add(relation.source_project_id);
  }
  const included = new Set([...touched, ...relationNeighbors]);
  const serviceByID = new Map(
    catalog.platform.services.map((service) => [
      service.source.project_id,
      service,
    ]),
  );
  const nodes = [...included].flatMap((id) => {
    const service = serviceByID.get(id);
    if (!service) return [];
    const ownChanges = changes.filter(
      (change) =>
        change.affected_service_ids.includes(id) ||
        (change.affected_service_ids.length === 0 &&
          allOperations(service).some((operation) =>
            change.affected_operation_ids.includes(operation.manifest.id),
          )),
    );
    return [
      {
        id,
        label: service.manifest?.identity.name || service.source.project_name,
        subtitle: ownChanges.length
          ? ownChanges
              .map((change) => `${change.action} ${change.kind}`)
              .join(" · ")
          : "Impacted context",
        change: strongestChange(ownChanges),
        operationLabels: ownChanges.flatMap((change) =>
          changedOperationLabels(catalog, change),
        ),
      },
    ];
  });
  const nodeIDs = new Set(nodes.map((node) => node.id));
  const edges = catalog.platform.relations.flatMap((relation, index) => {
    if (
      !relation.source_project_id ||
      !relation.target_project_id ||
      !nodeIDs.has(relation.source_project_id) ||
      !nodeIDs.has(relation.target_project_id)
    )
      return [];
    return [
      {
        id:
          relation.id ||
          `${relation.source_project_id}-${relation.target_project_id}-${index}`,
        source: relation.source_project_id,
        target: relation.target_project_id,
        label:
          [
            relation.relation_type,
            relation.transport || relation.protocol,
            relation.contract_code || relation.contract,
          ]
            .filter(Boolean)
            .join(" · ") || "CURRENT relation",
        transport:
          relation.transport || relation.protocol || "unknown transport",
      },
    ];
  });
  return { nodes, edges };
}

export function targetHumanSummary(
  change: TargetChangeDraft,
  catalog: ArchitectureCatalog,
) {
  const services = new Map(
    targetServiceOptions(catalog).map((service) => [service.id, service.label]),
  );
  const serviceNames = change.affected_service_ids.map(
    (id) => services.get(id) || "Unknown service",
  );
  const operationNames = change.affected_service_ids
    .flatMap((id) => targetOperationOptions(catalog, id))
    .filter((operation) => change.affected_operation_ids.includes(operation.id))
    .map((operation) => operation.label);
  return [change.action, change.kind, ...serviceNames, ...operationNames]
    .filter(Boolean)
    .join(" · ");
}
