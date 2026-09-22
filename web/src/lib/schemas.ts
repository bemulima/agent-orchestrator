import { z } from "zod";

export const actionSchema = z.object({
  action: z.string(),
  requires_confirmation: z.boolean(),
  requires_fingerprint: z.boolean(),
});

export const dashboardSchema = z.object({
  generated_at: z.string(),
  counts: z.object({ projects: z.number(), active_plans: z.number(), active_tasks: z.number(), attention_required: z.number() }),
  attention: z.array(z.object({ resource_type: z.string(), resource_id: z.string(), title: z.string(), status: z.string(), reason: z.string(), updated_at: z.string() })),
  active_runs: z.array(z.record(z.string(), z.unknown())),
  recent_activity: z.array(z.record(z.string(), z.unknown())),
});

export const projectSchema = z.object({
  id: z.string(), name: z.string(), status: z.string(), repository_role: z.string(),
  default_branch: z.string(), current_branch: z.string(), head_commit: z.string(), is_dirty: z.boolean(),
  updated_at: z.string(), local_path: z.string().nullable().optional(), git_url: z.string().nullable().optional(),
  archived_at: z.string().nullable().optional(), archived_from_status: z.string().nullable().optional(),
});
export const projectsSchema = z.object({ projects: z.array(projectSchema) });

const architectureEvidenceSchema = z.object({ source_path: z.string().optional().default(""), explanation: z.string().optional().default(""), confidence: z.number().optional().default(0) }).passthrough();
export const architectureServiceSchema = z.object({ project_id: z.string(), name: z.string(), repository_role: z.string(), service_kind: z.string(), purpose: z.string(), stack: z.array(architectureEvidenceSchema).default([]), capabilities: z.array(z.object({ name: z.string(), description: z.string(), source: z.string(), confidence: z.number() })).default([]), ownership: z.array(z.object({ resource_type: z.string(), resource_name: z.string(), source: z.string(), confidence: z.number() })).default([]), contract_count: z.number(), dependency_count: z.number(), consumer_count: z.number(), drift_status: z.string() });
export const architectureRelationSchema = z.object({ id: z.string(), source_project_id: z.string(), target_project_id: z.string(), relation_type: z.string(), contract_code: z.string().optional(), protocol: z.string().optional(), direction: z.string().optional(), version: z.string().optional(), evidence: architectureEvidenceSchema });
export const architectureContractSchema = z.object({ id: z.string(), project_id: z.string(), code: z.string(), type: z.string(), direction: z.string(), version: z.string().optional(), method: z.string().optional(), path: z.string().optional(), subject: z.string().optional(), resource: z.string().optional(), schema_discovered: z.boolean(), evidence: architectureEvidenceSchema, providers: z.array(z.object({ project_id: z.string(), name: z.string() })).default([]), consumers: z.array(z.object({ project_id: z.string(), name: z.string() })).default([]) });
export const architectureCurrentSchema = z.object({ mode: z.literal("CURRENT"), topology_revision_id: z.string(), topology_fingerprint: z.string(), generated_at: z.string(), topology_stale: z.boolean(), services: z.array(architectureServiceSchema), relations: z.array(architectureRelationSchema), contracts: z.array(architectureContractSchema), contract_drift: z.array(z.record(z.string(), z.unknown())).default([]) });
export type ArchitectureCurrent = z.infer<typeof architectureCurrentSchema>;

const architectureManifestEvidenceSchema = z.object({
  source_path: z.string(),
  symbol: z.string().optional(),
  start_line: z.number().int().optional(),
  end_line: z.number().int().optional(),
  checksum: z.string().optional(),
}).strict();

// Go's JSON encoder represents an uninitialised slice as null.  CURRENT is
// intentionally a faithful projection of repository-owned manifests, so the
// UI must render that equivalent empty-list form instead of rejecting the
// entire platform catalog.
const architectureManifestArray = <T extends z.ZodTypeAny>(item: T) => z.preprocess(
  (value) => value === null ? undefined : value,
  z.array(item).default([]),
);

const architectureManifestStatementSchema = z.object({
  value: z.string(),
  confidence: z.number(),
  evidence: architectureManifestArray(architectureManifestEvidenceSchema),
}).strict();

const architectureManifestSchemaRefSchema = z.object({
  name: z.string(),
  schema: z.record(z.string(), z.unknown()).optional(),
  description: architectureManifestStatementSchema,
}).strict();

const architectureManifestContractSchema = z.object({
  transport: z.string(),
  code: z.string(),
  direction: z.string(),
  description: architectureManifestStatementSchema,
}).strict();

const architectureManifestInteractionSchema = z.object({
  id: z.string(),
  transport: z.string(),
  target: z.string(),
  contract: z.string().optional(),
  direction: z.string(),
  description: architectureManifestStatementSchema,
}).strict();

const architectureManifestServiceSchema = z.object({
  schema: z.literal("architecture/v1"),
  kind: z.literal("service"),
  id: z.string(),
  manifest_revision: z.number().int(),
  identity: z.object({ name: z.string(), kind: z.string() }).strict(),
  purpose: architectureManifestStatementSchema,
  responsibilities: architectureManifestArray(architectureManifestStatementSchema),
  capabilities: architectureManifestArray(architectureManifestStatementSchema),
  owned_resources: architectureManifestArray(z.object({ type: z.string(), name: z.string(), description: architectureManifestStatementSchema }).strict()),
  inbound_interfaces: architectureManifestArray(z.object({ transport: z.string(), name: z.string(), description: architectureManifestStatementSchema }).strict()),
  outbound_dependencies: architectureManifestArray(architectureManifestInteractionSchema),
  endpoint_groups: architectureManifestArray(z.object({ id: z.string(), name: z.string(), description: architectureManifestStatementSchema, operations: architectureManifestArray(z.string()) }).strict()),
  produced_contracts: architectureManifestArray(architectureManifestContractSchema),
  consumed_contracts: architectureManifestArray(architectureManifestContractSchema),
  published_events: architectureManifestArray(architectureManifestContractSchema),
  subscribed_events: architectureManifestArray(architectureManifestContractSchema),
  business_rules: architectureManifestArray(architectureManifestStatementSchema),
  operation_manifests: architectureManifestArray(z.string()),
  evidence: architectureManifestArray(architectureManifestEvidenceSchema),
  confidence: z.number(),
}).strict();

const architectureOperationTypeSchema = z.enum(["http", "nats_request_reply", "nats_event_subscriber", "worker", "scheduled"]);

const architectureManifestOperationSchema = z.object({
  schema: z.literal("architecture/v1"),
  kind: z.literal("operation"),
  id: z.string(),
  manifest_revision: z.number().int(),
  service_id: z.string(),
  type: architectureOperationTypeSchema,
  identity: z.object({
    transport: z.string(),
    http: z.object({ method: z.string(), path: z.string() }).strict().optional(),
    nats: z.object({ subject: z.string(), role: z.string(), queue: z.string().optional(), mode: z.string().optional() }).strict().optional(),
    worker: z.object({ name: z.string() }).strict().optional(),
    scheduled: z.object({ name: z.string(), schedule: z.string() }).strict().optional(),
  }).strict(),
  access: z.object({
    audience: architectureManifestStatementSchema,
    authentication: architectureManifestStatementSchema,
    authorization: architectureManifestStatementSchema,
    idempotency: architectureManifestStatementSchema,
  }).strict(),
  trigger: z.object({ description: architectureManifestStatementSchema }).strict(),
  input: z.object({
    path_params: architectureManifestArray(z.object({ name: z.string(), required: z.boolean(), description: architectureManifestStatementSchema }).strict()),
    query: architectureManifestArray(z.object({ name: z.string(), required: z.boolean(), description: architectureManifestStatementSchema }).strict()),
    headers: architectureManifestArray(z.object({ name: z.string(), required: z.boolean(), description: architectureManifestStatementSchema }).strict()),
    body: architectureManifestSchemaRefSchema.optional(),
  }).strict(),
  business_task: architectureManifestStatementSchema,
  business_process: architectureManifestArray(z.object({ id: z.string(), description: architectureManifestStatementSchema, interactions: architectureManifestArray(z.string()) }).strict()),
  business_rules: architectureManifestArray(architectureManifestStatementSchema),
  implementation: z.object({
    router: architectureManifestArray(architectureManifestEvidenceSchema),
    handler: architectureManifestArray(architectureManifestEvidenceSchema),
    use_cases: architectureManifestArray(architectureManifestEvidenceSchema),
    domain_services: architectureManifestArray(architectureManifestEvidenceSchema),
    repositories: architectureManifestArray(architectureManifestEvidenceSchema),
  }).strict(),
  data_access: architectureManifestArray(z.object({ resource: z.string(), access: z.string(), description: architectureManifestStatementSchema }).strict()),
  external_interactions: architectureManifestArray(architectureManifestInteractionSchema),
  side_effects: architectureManifestArray(z.object({ type: z.string(), description: architectureManifestStatementSchema }).strict()),
  output: z.object({
    responses: architectureManifestArray(z.object({ status_code: z.number().int(), description: architectureManifestStatementSchema, body: architectureManifestSchemaRefSchema.optional() }).strict()),
    result: architectureManifestSchemaRefSchema.optional(),
    emitted_events: architectureManifestArray(architectureManifestContractSchema),
  }).strict(),
  errors: architectureManifestArray(z.object({ code: z.string(), status_code: z.number().int().optional(), description: architectureManifestStatementSchema }).strict()),
  evidence: architectureManifestArray(architectureManifestEvidenceSchema),
  confidence: z.number(),
}).strict();

const architectureCatalogOperationSchema = z.object({ manifest: architectureManifestOperationSchema }).strict();
const architectureCatalogCompletenessSchema = z.object({
  source_count: z.number().int(),
  covered_service_count: z.number().int(),
  uncovered_service_count: z.number().int(),
  total_discovered_operations: z.number().int(),
  http_operations: z.number().int(),
  nats_request_reply_operations: z.number().int().optional().default(0),
  event_operations: z.number().int(),
  worker_operations: z.number().int(),
  scheduled_operations: z.number().int(),
  operations_with_manifests: z.number().int(),
  operations_missing_manifests: z.number().int(),
  operations_blocked: z.number().int(),
  declared_operation_manifest_count: z.number().int(),
  parsed_operation_manifest_count: z.number().int(),
  missing_declared_operation_manifest_count: z.number().int(),
  unexpected_parsed_operation_manifest_count: z.number().int(),
  manifest_operations_without_discovery: z.number().int(),
  group_operation_reference_count: z.number().int(),
  missing_group_operation_count: z.number().int(),
  operation_kinds: z.array(z.object({ type: architectureOperationTypeSchema, count: z.number().int() }).strict()),
}).strict();

const architectureCatalogServiceSchema = z.object({
  source: z.object({
    project_id: z.string(), project_name: z.string(), project_status: z.string(), repository_role: z.string(),
    snapshot_id: z.string(), snapshot_status: z.string(), commit_sha: z.string(), branch: z.string(), content_checksum: z.string(),
    discovery_schema_version: z.number().int(), source_current: z.boolean(), is_dirty: z.boolean(),
  }).strict(),
  covered: z.boolean(),
  manifest: architectureManifestServiceSchema.optional(),
  groups: z.array(z.object({
    id: z.string(), name: z.string(), description: architectureManifestStatementSchema,
    operations: z.array(architectureCatalogOperationSchema), missing_operation_ids: z.array(z.string()),
  }).strict()),
  ungrouped_operations: z.array(architectureCatalogOperationSchema),
  completeness: architectureCatalogCompletenessSchema,
}).strict();

// Platform relations are intentionally permissive: discovery can prove an
// internal service edge, or only an external target.  CURRENT must preserve
// both cases instead of dropping a relation because one optional field is
// unavailable in a particular scanner/runtime.
const architectureCatalogRelationSchema = z.object({
  id: z.string().optional(),
  source_project_id: z.string().optional(),
  target_project_id: z.string().optional(),
  target_external: z.string().optional(),
  relation_type: z.string().optional(),
  protocol: z.string().optional(),
  transport: z.string().optional(),
  contract_code: z.string().optional(),
  contract: z.string().optional(),
  direction: z.string().optional(),
  evidence: z.union([architectureEvidenceSchema, architectureManifestArray(architectureManifestEvidenceSchema)]).nullish(),
}).passthrough();

export const architectureCatalogSchema = z.object({
  mode: z.literal("CURRENT"),
  fingerprint: z.string(),
  platform: z.object({
    services: z.array(architectureCatalogServiceSchema),
    relations: z.array(architectureCatalogRelationSchema).optional().default([]),
    completeness: architectureCatalogCompletenessSchema,
  }).strict(),
}).strict();

export type ArchitectureCatalog = z.infer<typeof architectureCatalogSchema>;
export type ArchitectureCatalogService = z.infer<typeof architectureCatalogServiceSchema>;
export type ArchitectureCatalogOperation = z.infer<typeof architectureCatalogOperationSchema>;
export type ArchitectureCatalogRelation = z.infer<typeof architectureCatalogRelationSchema>;

// TARGET is deliberately a separate, proposal-only read model.  The backend
// is introduced after the CURRENT catalog, so these schemas accept additive
// fields and the common {target|proposal} response envelopes while retaining
// the fields needed to bind every draft to the CURRENT fingerprint.
export const architectureTargetChangeSchema = z.object({
  id: z.string().optional(),
  scope: z.string().optional().default("operation"),
  action: z.string().optional().default("change"),
  kind: z.string().optional().default("operation"),
  affected_service_ids: z.array(z.string()).optional().default([]),
  affected_operation_ids: z.array(z.string()).optional().default([]),
  rationale: z.object({ summary: z.string().optional().default(""), details: z.string().optional().default("") }).passthrough().optional().default({ summary: "", details: "" }),
  desired_result: z.object({ summary: z.string().optional().default(""), success_criteria: z.array(z.string()).optional().default([]) }).passthrough().optional().default({ summary: "", success_criteria: [] }),
  evidence: z.array(z.record(z.string(), z.unknown())).optional().default([]),
  confidence: z.number().optional().default(0.8),
  unresolved_areas: z.array(z.record(z.string(), z.unknown())).optional().default([]),
}).passthrough();

export const architectureTargetSchema = z.object({
  id: z.string(),
  status: z.string().optional().default("draft"),
  current_fingerprint: z.string().optional().default(""),
  fingerprint: z.string().optional().default(""),
  revision: z.number().int().optional().default(1),
  idempotency_key: z.string().optional(),
  submitted_at: z.string().optional(),
  decided_at: z.string().optional(),
  superseded_at: z.string().optional(),
  supersedes_proposal_id: z.string().optional(),
  decided_by: z.string().optional(),
  decision_comment: z.string().optional(),
  changes_requested_by: z.string().optional(),
  changes_requested_at: z.string().optional(),
  changes_request_comment: z.string().optional(),
  decision: z.string().optional(),
  created_at: z.string().optional(),
  updated_at: z.string().optional(),
  changes: z.array(architectureTargetChangeSchema).optional().default([]),
  diff: z.unknown().optional(),
  impact: z.unknown().optional(),
  current: z.unknown().optional(),
  target: z.unknown().optional(),
  allowed_actions: z.array(actionSchema).optional().default([]),
}).passthrough();

function unwrapArchitectureTarget(value: unknown): unknown {
  if (!value || typeof value !== "object" || Array.isArray(value)) return value;
  const record = value as Record<string, unknown>;
  for (const key of ["target", "proposal"]) {
    const nested = record[key];
    if (nested && typeof nested === "object" && !Array.isArray(nested)) return { ...record, ...(nested as Record<string, unknown>) };
  }
  return record;
}

export const architectureTargetResponseSchema = z.preprocess(unwrapArchitectureTarget, architectureTargetSchema);
export const architectureTargetListSchema = z.preprocess((value) => {
  if (Array.isArray(value)) return { items: value };
  if (!value || typeof value !== "object") return value;
  const record = value as Record<string, unknown>;
  const values = Array.isArray(record.items) ? record.items : Array.isArray(record.targets) ? record.targets : Array.isArray(record.proposals) ? record.proposals : [];
  return { ...record, items: values.map(unwrapArchitectureTarget) };
}, z.object({
  items: z.array(architectureTargetSchema).default([]),
  next_cursor: z.string().optional(),
  has_more: z.boolean().optional().default(false),
}).passthrough());

export type ArchitectureTarget = z.infer<typeof architectureTargetSchema>;
export type ArchitectureTargetChange = z.infer<typeof architectureTargetChangeSchema>;

export const discoveryReportSchema = z.object({
  id: z.string().optional(),
  project_id: z.string().optional(),
  status: z.string().optional(),
  summary: z.string().optional(),
  warnings: z.array(z.string()).optional().default([]),
}).loose();

export const connectProjectResultSchema = z.object({
  project: projectSchema,
  snapshot: z.object({
    service_kind: z.string().optional(),
    language: z.string().optional(),
    framework: z.string().optional(),
  }).loose(),
  report: discoveryReportSchema,
});

export const commandSchema = z.object({
  id: z.string(),
  text: z.string(),
  status: z.string(),
}).loose();

export const conversationSchema = z.object({
  id: z.string(), title: z.string(), scope_type: z.string(), scope_id: z.string().nullable().optional(),
  agent_thread_id: z.string().nullable().optional(), message_count: z.number(), created_at: z.string(), updated_at: z.string(),
});
export const conversationsSchema = z.object({ items: z.array(conversationSchema) });
export const resourceReferenceSchema = z.object({ resource_type: z.string(), resource_id: z.string(), label: z.string() });
export const conversationMessageSchema = z.object({
  id: z.string(), conversation_id: z.string(), role: z.string(), status: z.string(), content: z.string(),
  references: z.array(resourceReferenceSchema), error: z.string().nullable().optional(), created_at: z.string(),
  completed_at: z.string().nullable().optional(),
});
export const actionProposalSchema = z.object({
  id: z.string(), conversation_id: z.string(), message_id: z.string(), action: z.string(), resource_type: z.string(),
  resource_id: z.string(), title: z.string(), description: z.string(), risk_level: z.string(), fingerprint: z.string().nullable().optional(),
  status: z.string(), created_at: z.string(), decided_at: z.string().nullable().optional(),
});
export const conversationDetailSchema = z.object({
  conversation: conversationSchema,
  messages: z.array(conversationMessageSchema),
  proposals: z.array(actionProposalSchema),
});

const usageBreakdownSchema = z.object({
  key: z.string(), runs: z.number(), failed_runs: z.number(), input_tokens: z.number(),
  cached_input_tokens: z.number(), output_tokens: z.number(), reasoning_output_tokens: z.number(),
});
const usageWindowSchema = z.object({
  since: z.string(), runs: z.number(), failed_runs: z.number(),
  by_model: z.array(usageBreakdownSchema), by_role: z.array(usageBreakdownSchema),
});
export const agentUsageDashboardSchema = z.object({
  generated_at: z.string(), five_hours: usageWindowSchema, seven_days: usageWindowSchema, thirty_days: usageWindowSchema,
  budget: z.object({ mode: z.string(), deep_model: z.string(), deep_runs_five_hours: z.number(), deep_run_limit: z.number(), utilization_percent: z.number(), xhigh_allowed: z.boolean() }),
  routing: z.object({ coder_model: z.string(), routine_review_model: z.string(), fast_model: z.string(), standard_model: z.string(), deep_model: z.string(), work_item_draft_mode: z.string() }),
});

export const planSummarySchema = z.object({
  id: z.string(), command_id: z.string(), summary: z.string(), status: z.string(), version: z.number(),
  risk_level: z.string(), source_kind: z.string(), fingerprint: z.string(), task_count: z.number(),
  completed_tasks: z.number(), attention_tasks: z.number(), issue_count: z.number(), published_issues: z.number(),
  issue_publication: z.enum(["none", "draft", "simulation", "external"]),
  run_id: z.string().nullable().optional(), run_status: z.string().nullable().optional(), run_error: z.string().nullable().optional(),
  supersedes_plan_id: z.string().nullable().optional(), superseded_by_plan_id: z.string().nullable().optional(), updated_at: z.string(),
  allowed_actions: z.array(actionSchema),
});

export const runSummarySchema = z.object({
  id: z.string(), plan_id: z.string(), plan_summary: z.string(), status: z.string(), workflow_id: z.string(),
  max_parallel_tasks: z.number(), task_count: z.number(), completed_tasks: z.number(), active_tasks: z.number(),
  error: z.string().nullable().optional(), created_at: z.string(), started_at: z.string().nullable().optional(),
  completed_at: z.string().nullable().optional(), updated_at: z.string(), allowed_actions: z.array(actionSchema),
});

export const taskSummarySchema = z.object({
  id: z.string(), plan_id: z.string(), project_id: z.string(), project_name: z.string(), plan_summary: z.string(),
  title: z.string(), status: z.string(), risk_level: z.string(), priority: z.number(), depth: z.number(),
  attempt_count: z.number(), last_attempt_status: z.string().nullable().optional(), created_at: z.string(),
  started_at: z.string().nullable().optional(), completed_at: z.string().nullable().optional(), updated_at: z.string(),
  allowed_actions: z.array(actionSchema),
});

export const approvalSchema = z.object({
  id: z.string(), resource_type: z.string(), resource_id: z.string(), resource_name: z.string(), action: z.string(),
  status: z.string(), fingerprint: z.string().optional(), risk_level: z.string().optional(), requested_at: z.string(),
  decided_at: z.string().nullable().optional(),
});

const page = <T extends z.ZodType>(schema: T) => z.object({ items: z.array(schema), next_cursor: z.string().optional(), has_more: z.boolean() });
export const plansPageSchema = page(planSummarySchema).extend({
  work_item_gateway: z.string(), external_writes_enabled: z.boolean(),
});
export const runsPageSchema = page(runSummarySchema);
export const tasksPageSchema = page(taskSummarySchema);
export const approvalsPageSchema = page(approvalSchema);

export const planBundleSchema = z.object({
  plan: z.object({
    id: z.string(), command_id: z.string(), status: z.string(), version: z.number(), summary: z.string(),
    risk_level: z.string(), fingerprint: z.string(), approved_fingerprint: z.string().nullable().optional(),
    discussion_revision: z.number(), updated_at: z.string(),
  }).loose(),
  tasks: z.array(z.object({
    id: z.string(), plan_id: z.string(), project_id: z.string(), title: z.string(), description: z.string(),
    status: z.string(), priority: z.number(), depth: z.number(), risk_level: z.string(),
    acceptance_criteria: z.array(z.string()), write_scope: z.array(z.string()), verification_commands: z.array(z.string()),
  }).loose()),
  dependencies: z.array(z.object({ task_id: z.string(), depends_on_task_id: z.string(), dependency_type: z.string() })),
  work_items: z.array(z.record(z.string(), z.unknown())),
  discussion: z.array(z.record(z.string(), z.unknown())),
  run: z.record(z.string(), z.unknown()).nullable().optional(),
}).loose();

export const taskDetailSchema = z.object({
  id: z.string(), plan_id: z.string(), project_id: z.string(), title: z.string(), description: z.string(), status: z.string(),
  risk_level: z.string(), priority: z.number(), depth: z.number(), model_profile: z.string(), acceptance_criteria: z.array(z.string()),
  write_scope: z.array(z.string()), verification_commands: z.array(z.string()), started_at: z.string().nullable().optional(),
  completed_at: z.string().nullable().optional(),
}).loose();

export const runDetailSchema = z.object({ id: z.string(), plan_id: z.string(), status: z.string(), workflow_id: z.string(), error: z.string().nullable().optional(), updated_at: z.string() }).loose();
export const attemptsSchema = z.object({ task_id: z.string(), attempts: z.array(z.record(z.string(), z.unknown())) });
export const artifactsSchema = z.object({ task_id: z.string(), artifacts: z.array(z.record(z.string(), z.unknown())) });

export type ResourceAction = z.infer<typeof actionSchema>;
export type PlanSummary = z.infer<typeof planSummarySchema>;
export type RunSummary = z.infer<typeof runSummarySchema>;
export type TaskSummary = z.infer<typeof taskSummarySchema>;
export type Conversation = z.infer<typeof conversationSchema>;
export type ActionProposal = z.infer<typeof actionProposalSchema>;
