import { describe, expect, it } from "vitest";
import { architectureCatalogSchema } from "@/lib/schemas";

const completeness = {
  source_count: 1, covered_service_count: 1, uncovered_service_count: 0,
  total_discovered_operations: 0, http_operations: 0, nats_request_reply_operations: 0,
  event_operations: 0, worker_operations: 0, scheduled_operations: 0,
  operations_with_manifests: 0, operations_missing_manifests: 0, operations_blocked: 0,
  declared_operation_manifest_count: 0, parsed_operation_manifest_count: 0,
  missing_declared_operation_manifest_count: 0, unexpected_parsed_operation_manifest_count: 0,
  manifest_operations_without_discovery: 0, group_operation_reference_count: 0,
  missing_group_operation_count: 0, operation_kinds: [],
};

describe("Architecture CURRENT catalog boundary", () => {
  it("normalizes Go nil slices and relation evidence without rejecting the platform", () => {
    const parsed = architectureCatalogSchema.parse({
      mode: "CURRENT", fingerprint: "f".repeat(64),
      platform: {
        completeness,
        relations: [{ id: "r1", source_project_id: "service-1", evidence: [] }],
        services: [{
          source: { project_id: "service-1", project_name: "service", project_status: "analyzed", repository_role: "service", snapshot_id: "snapshot", snapshot_status: "analyzed", commit_sha: "commit", branch: "main", content_checksum: "checksum", discovery_schema_version: 1, source_current: true, is_dirty: false },
          covered: true,
          manifest: {
            schema: "architecture/v1", kind: "service", id: "service", manifest_revision: 1,
            identity: { name: "service", kind: "backend_service" },
            purpose: { value: "purpose", confidence: 1, evidence: null },
            responsibilities: null, capabilities: null, owned_resources: null, inbound_interfaces: null,
            outbound_dependencies: null, endpoint_groups: null, produced_contracts: null, consumed_contracts: null,
            published_events: null, subscribed_events: null, business_rules: null, operation_manifests: null,
            evidence: null, confidence: 1,
          },
          groups: [], ungrouped_operations: [], completeness,
        }],
      },
    });

    expect(parsed.platform.services[0].manifest?.responsibilities).toEqual([]);
    expect(parsed.platform.relations[0].evidence).toEqual([]);
  });
});
