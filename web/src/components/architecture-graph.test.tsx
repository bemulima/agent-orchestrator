import { describe, expect, it } from "vitest";
import { architectureGraphItems } from "@/components/architecture-graph";

describe("architecture graph mapping", () => {
  it("places 50 nodes deterministically and removes duplicate edges", () => {
    const services = Array.from({ length: 51 }, (_, index) => ({ project_id: `p${index}`, name: `service-${index}`, repository_role: "service", service_kind: "backend", purpose: "", stack: [], capabilities: [], ownership: [], contract_count: 0, dependency_count: 0, consumer_count: 0, drift_status: "none" }));
    const relation = { id: "r", source_project_id: "p0", target_project_id: "p1", relation_type: "depends_on", evidence: { source_path: "main.go", explanation: "", confidence: .8 } };
    const graph = architectureGraphItems({ mode: "CURRENT", topology_revision_id: "r", topology_fingerprint: "f", generated_at: new Date().toISOString(), topology_stale: false, services, relations: [relation, { ...relation, id: "r2" }], contracts: [], contract_drift: [] });
    expect(graph.nodes).toHaveLength(51); expect(graph.edges).toHaveLength(1); expect(graph.nodes[50].position).toEqual(architectureGraphItems({ mode: "CURRENT", topology_revision_id: "r", topology_fingerprint: "f", generated_at: "", topology_stale: false, services, relations: [], contracts: [], contract_drift: [] }).nodes[50].position);
  });
});
