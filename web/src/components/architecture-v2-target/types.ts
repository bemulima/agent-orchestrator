import type { ArchitectureCatalog, ArchitectureTarget } from "@/lib/schemas";

export type TargetChangeAction = "add" | "change" | "remove";
export type TargetChangeScope = "service" | "operation";
export type TargetChangeKind = "service" | "dependency" | "operation" | "contract" | "event" | "responsibility" | "business_process";

// This is deliberately the same transport shape accepted by the existing
// TARGET API.  V2 only improves the way a person selects and describes it.
export type TargetChangeDraft = {
  id?: string;
  scope: TargetChangeScope;
  action: TargetChangeAction;
  kind: TargetChangeKind;
  affected_service_ids: string[];
  affected_operation_ids: string[];
  rationale: { summary: string; details: string };
  desired_result: { summary: string; success_criteria: string[] };
  evidence: Array<{ source_path: string; symbol?: string }>;
  confidence: number;
  unresolved_areas: Array<{ area: string; reason: string }>;
};

export type TargetLifecycleAction = "create" | "save" | "submit" | "approve" | "reject" | "request-changes" | "implementation-plan" | "verify";

export type TargetWorkspaceProps = {
  catalog: ArchitectureCatalog;
  currentFingerprint: string;
  target?: ArchitectureTarget;
  changes: TargetChangeDraft[];
  onChangesChange: (changes: TargetChangeDraft[]) => void;
  onLifecycle?: (action: TargetLifecycleAction) => void;
  busyAction?: TargetLifecycleAction;
  lifecycleError?: string;
  lifecycleResult?: unknown;
};
