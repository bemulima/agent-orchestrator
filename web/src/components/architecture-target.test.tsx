import { describe, expect, it } from "vitest";
import { architectureTargetListSchema, architectureTargetResponseSchema } from "@/lib/schemas";
import { targetBindingMatches, targetChangeToDraft, targetChangesPayload, targetDecisionAllowed } from "@/components/architecture-target";

describe("architecture TARGET proposal boundary", () => {
  it("accepts the backend targets envelope and proposal fields", () => {
    const list = architectureTargetListSchema.parse({ targets: [{ id: "t1", current_fingerprint: "a".repeat(64), fingerprint: "b".repeat(64), revision: 1, status: "draft", changes: [] }] });
    expect(list.items[0].revision).toBe(1);
    const detail = architectureTargetResponseSchema.parse({ id: "t1", current_fingerprint: "a".repeat(64), fingerprint: "b".repeat(64), revision: 2, changes: [] });
    expect(detail.current_fingerprint).toHaveLength(64);
  });

  it("serializes only the typed operation-scope change contract", () => {
    const draft = targetChangeToDraft({ id: "change-1", scope: "operation", action: "change", kind: "operation", affected_service_ids: ["teacher"], affected_operation_ids: ["health"], rationale: { summary: "Document endpoint", details: "Details" }, desired_result: { summary: "Visible flow", success_criteria: ["Reviewable"] }, evidence: [{ source_path: "internal/handler.go", symbol: "Handle" }], confidence: .8, unresolved_areas: [] });
    expect(draft.kind).toBe("operation");
    expect(targetChangesPayload([draft])).toEqual([expect.objectContaining({ scope: "operation", action: "change", kind: "operation", affected_service_ids: ["teacher"], affected_operation_ids: ["health"] })]);
    expect(targetChangesPayload([draft])[0]).not.toHaveProperty("manifest");
  });

  it("requires exact CURRENT binding and submitted status for decisions", () => {
    const target = { status: "submitted", current_fingerprint: "a".repeat(64), fingerprint: "b".repeat(64) } as const;
    expect(targetBindingMatches(target, "a".repeat(64))).toBe(true);
    expect(targetDecisionAllowed(target, "a".repeat(64))).toBe(true);
    expect(targetDecisionAllowed(target, "c".repeat(64))).toBe(false);
    expect(targetDecisionAllowed({ ...target, status: "approved" }, "a".repeat(64))).toBe(false);
  });
});
