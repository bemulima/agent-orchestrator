import { describe, expect, it } from "vitest";
import type { PlanSummary } from "@/lib/schemas";
import { groupPlans, publicationCopy, riskLabel, runErrorCopy, sourceLabel } from "./plan-list-view";

function plan(overrides: Partial<PlanSummary> = {}): PlanSummary {
  return {
    id: "plan-1", command_id: "command-1", summary: "План", status: "discussion", version: 1,
    risk_level: "medium", source_kind: "discussion", fingerprint: "fingerprint", task_count: 2,
    completed_tasks: 0, attention_tasks: 0, issue_count: 0, published_issues: 0,
    issue_publication: "none", run_id: null, run_status: null, updated_at: "2026-07-27T00:00:00Z",
    allowed_actions: [], ...overrides,
  };
}

describe("plan list presentation", () => {
  it("separates preparation, decisions, approved plans, runs and archive", () => {
    const groups = groupPlans([
      plan({ id: "approval", status: "awaiting_approval" }),
      plan({ id: "draft" }),
      plan({ id: "approved", status: "approved" }),
      plan({ id: "run", status: "failed", run_id: "run-1" }),
      plan({ id: "archive", status: "cancelled" }),
    ]);

    expect(groups.decisions.map(item => item.id)).toEqual(["approval"]);
    expect(groups.preparation.map(item => item.id)).toEqual(["draft"]);
    expect(groups.approved.map(item => item.id)).toEqual(["approved"]);
    expect(groups.executions.map(item => item.id)).toEqual(["run"]);
    expect(groups.archive.map(item => item.id)).toEqual(["archive"]);
  });

  it("distinguishes local drafts, simulations and external publications", () => {
    expect(publicationCopy(plan({ issue_publication: "draft", issue_count: 4 })).label).toBe("Локальный черновик");
    expect(publicationCopy(plan({ issue_publication: "simulation", published_issues: 9 })).detail).toContain("github.example.test");
    expect(publicationCopy(plan({ issue_publication: "external", published_issues: 2 })).label).toBe("Внешняя публикация");
    expect(publicationCopy(plan({ status: "awaiting_approval" })).label).toBe("Legacy-план");
  });

  it("explains source and technical risk in Russian", () => {
    expect(sourceLabel("discussion")).toBe("Из запроса владельца");
    expect(sourceLabel("issue")).toBe("Из существующего issue");
    expect(riskLabel("critical")).toBe("Критический");
  });

  it("translates known run failures into owner-facing explanations", () => {
    expect(runErrorCopy("review changes remain after the maximum review count")).toContain("лимита проверок");
    expect(runErrorCopy("completed result cannot request dependent tasks")).toContain("проверкой протокола");
  });
});
