import { describe, expect, it } from "vitest";
import type { ArchitectureCatalog } from "@/lib/schemas";
import {
  newTargetChangeDraft,
  targetOperationOptions,
  targetServiceOptions,
  targetVisualDiffModel,
} from "./target-adapter";

const statement = { value: "documented", confidence: 1, evidence: [] };
const service = (
  id: string,
  name: string,
  operationID: string,
  path: string,
) => ({
  source: {
    project_id: id,
    project_name: name,
    project_status: "active",
    repository_role: "backend",
    snapshot_id: "snapshot",
    snapshot_status: "current",
    commit_sha: "sha",
    branch: "main",
    content_checksum: "checksum",
    discovery_schema_version: 1,
    source_current: true,
    is_dirty: false,
  },
  covered: true,
  manifest: {
    schema: "architecture/v1",
    kind: "service",
    id: name,
    manifest_revision: 1,
    identity: { name, kind: "backend" },
    purpose: { ...statement, value: `${name} purpose` },
    responsibilities: [],
    capabilities: [{ ...statement, value: "Identity" }],
    owned_resources: [],
    inbound_interfaces: [],
    outbound_dependencies: [],
    endpoint_groups: [],
    produced_contracts: [],
    consumed_contracts: [],
    published_events: [],
    subscribed_events: [],
    business_rules: [],
    operation_manifests: [operationID],
    evidence: [],
    confidence: 1,
  },
  groups: [
    {
      id: "api",
      name: "API",
      description: statement,
      missing_operation_ids: [],
      operations: [
        {
          manifest: {
            schema: "architecture/v1",
            kind: "operation",
            id: operationID,
            manifest_revision: 1,
            service_id: name,
            type: "http",
            identity: { transport: "http", http: { method: "GET", path } },
            access: {
              audience: statement,
              authentication: statement,
              authorization: statement,
              idempotency: statement,
            },
            trigger: { description: statement },
            input: { path_params: [], query: [], headers: [] },
            business_task: { ...statement, value: `Read ${name}` },
            business_process: [],
            business_rules: [],
            implementation: {
              router: [],
              handler: [],
              use_cases: [],
              domain_services: [],
              repositories: [],
            },
            data_access: [],
            external_interactions: [],
            side_effects: [],
            output: { responses: [], emitted_events: [] },
            errors: [],
            evidence: [],
            confidence: 1,
          },
        },
      ],
    },
  ],
  ungrouped_operations: [],
  completeness: {
    source_count: 1,
    covered_service_count: 1,
    uncovered_service_count: 0,
    total_discovered_operations: 1,
    http_operations: 1,
    nats_request_reply_operations: 0,
    event_operations: 0,
    worker_operations: 0,
    scheduled_operations: 0,
    operations_with_manifests: 1,
    operations_missing_manifests: 0,
    operations_blocked: 0,
    declared_operation_manifest_count: 1,
    parsed_operation_manifest_count: 1,
    missing_declared_operation_manifest_count: 0,
    unexpected_parsed_operation_manifest_count: 0,
    manifest_operations_without_discovery: 0,
    group_operation_reference_count: 1,
    missing_group_operation_count: 0,
    operation_kinds: [{ type: "http", count: 1 }],
  },
});

const catalog = {
  mode: "CURRENT",
  fingerprint: "current-fingerprint",
  platform: {
    services: [
      service(
        "service-internal-id-1",
        "Identity service",
        "operation-internal-id-1",
        "/auth/me",
      ),
      service(
        "service-internal-id-2",
        "Course service",
        "operation-internal-id-2",
        "/courses",
      ),
    ],
    relations: [
      {
        id: "relation-1",
        source_project_id: "service-internal-id-1",
        target_project_id: "service-internal-id-2",
        relation_type: "calls",
        transport: "HTTP",
        contract: "course.read",
      },
    ],
    completeness: {
      source_count: 2,
      covered_service_count: 2,
      uncovered_service_count: 0,
      total_discovered_operations: 2,
      http_operations: 2,
      nats_request_reply_operations: 0,
      event_operations: 0,
      worker_operations: 0,
      scheduled_operations: 0,
      operations_with_manifests: 2,
      operations_missing_manifests: 0,
      operations_blocked: 0,
      declared_operation_manifest_count: 2,
      parsed_operation_manifest_count: 2,
      missing_declared_operation_manifest_count: 0,
      unexpected_parsed_operation_manifest_count: 0,
      manifest_operations_without_discovery: 0,
      group_operation_reference_count: 2,
      missing_group_operation_count: 0,
      operation_kinds: [{ type: "http", count: 2 }],
    },
  },
} as unknown as ArchitectureCatalog;

describe("V2 TARGET presentation adapter", () => {
  it("offers human-readable service and operation choices while retaining IDs only as values", () => {
    expect(targetServiceOptions(catalog)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          label: "Identity service",
          purpose: "Identity service purpose",
        }),
      ]),
    );
    expect(targetOperationOptions(catalog, "service-internal-id-1")).toEqual([
      expect.objectContaining({
        label: "GET /auth/me",
        transport: "HTTP",
        businessTask: "Read Identity service",
      }),
    ]);
  });

  it("renders a contextual visual diff from changed services and their CURRENT relation neighbors", () => {
    const change = newTargetChangeDraft();
    change.affected_service_ids = ["service-internal-id-1"];
    change.affected_operation_ids = ["operation-internal-id-1"];
    change.action = "change";
    const model = targetVisualDiffModel(catalog, [change]);
    expect(model.nodes.map((node) => node.label)).toEqual(
      expect.arrayContaining(["Identity service", "Course service"]),
    );
    expect(
      model.nodes.find((node) => node.label === "Identity service")
        ?.operationLabels,
    ).toContain("HTTP · GET /auth/me");
    expect(model.edges).toEqual([
      expect.objectContaining({ label: "calls · HTTP · course.read" }),
    ]);
  });

  it("does not merge same-named operation IDs from other services into a contextual change", () => {
    const duplicateOperationCatalog = {
      ...catalog,
      platform: {
        ...catalog.platform,
        services: [
          service(
            "service-internal-id-1",
            "Identity service",
            "validate",
            "/auth/me",
          ),
          service(
            "service-internal-id-2",
            "Course service",
            "validate",
            "/courses",
          ),
        ],
      },
    } as unknown as ArchitectureCatalog;
    const change = newTargetChangeDraft();
    change.affected_service_ids = ["service-internal-id-1"];
    change.affected_operation_ids = ["validate"];

    const model = targetVisualDiffModel(duplicateOperationCatalog, [change]);

    expect(
      model.nodes.find((node) => node.label === "Identity service")
        ?.operationLabels,
    ).toEqual(["HTTP · GET /auth/me"]);
    expect(
      model.nodes.find((node) => node.label === "Course service")?.subtitle,
    ).toBe("Impacted context");
  });

  it("does not invent graph context for an empty draft", () => {
    expect(targetVisualDiffModel(catalog, [newTargetChangeDraft()])).toEqual({
      nodes: [],
      edges: [],
    });
  });
});
