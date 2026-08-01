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
});
export const projectsSchema = z.object({ projects: z.array(projectSchema) });

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
