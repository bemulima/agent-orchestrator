"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { apiGet, formatDate } from "@/lib/api";
import { artifactsSchema, attemptsSchema, taskDetailSchema, tasksPageSchema } from "@/lib/schemas";
import { Empty, Failure, Loading } from "@/components/page-state";
import { ResourceActions } from "@/components/resource-actions";
import { StatusBadge } from "@/components/status-badge";

function field(value: unknown) { return typeof value === "string" && value ? value : "—"; }

export default function TaskDetailPage() {
  const id = String(useParams().id);
  const detailQuery = useQuery({ queryKey: ["task", id], queryFn: () => apiGet(`/api/v1/tasks/${id}`, taskDetailSchema) });
  const attemptsQuery = useQuery({ queryKey: ["task-attempts", id], queryFn: () => apiGet(`/api/v1/tasks/${id}/attempts`, attemptsSchema) });
  const artifactsQuery = useQuery({ queryKey: ["task-artifacts", id], queryFn: () => apiGet(`/api/v1/tasks/${id}/artifacts`, artifactsSchema) });
  const summaryQuery = useQuery({ queryKey: ["tasks", "all"], queryFn: () => apiGet("/api/v1/tasks?limit=100", tasksPageSchema) });
  if (detailQuery.isLoading) return <Loading />;
  if (detailQuery.error) return <Failure error={detailQuery.error} />;
  const task = detailQuery.data!;
  const summary = summaryQuery.data?.items.find(item => item.id === id);
  return <div className="page"><header><div><p className="eyebrow">Задача</p><h1>{task.title}</h1><p>{task.description}</p></div><StatusBadge status={task.status} /></header>{summary && <ResourceActions resourceType="task" resourceID={task.id} actions={summary.allowed_actions} />}<section className="detail-grid"><article className="panel"><h2>Параметры</h2><dl><dt>Проект</dt><dd><Link href={`/projects/${task.project_id}`}>{task.project_id}</Link></dd><dt>План</dt><dd><Link href={`/plans/${task.plan_id}`}>{task.plan_id}</Link></dd><dt>Риск</dt><dd>{task.risk_level}</dd><dt>Модель</dt><dd>{task.model_profile}</dd><dt>Волна</dt><dd>{task.depth}</dd><dt>Начато</dt><dd>{formatDate(task.started_at)}</dd></dl></article><article className="panel"><h2>Критерии приёмки</h2><ul>{task.acceptance_criteria.map(item => <li key={item}>{item}</li>)}</ul><h2>Write scope</h2><ul>{task.write_scope.map(item => <li key={item}><code>{item}</code></li>)}</ul></article></section><section className="panel"><h2>Попытки</h2>{attemptsQuery.isLoading ? <Loading /> : attemptsQuery.error ? <Failure error={attemptsQuery.error} /> : !attemptsQuery.data!.attempts.length ? <Empty /> : <div className="attempts">{attemptsQuery.data!.attempts.map((attempt, index) => <details key={String(attempt.id ?? index)} open={index === attemptsQuery.data!.attempts.length - 1}><summary>Попытка {String(attempt.attempt_number ?? index + 1)} · {field(attempt.status)}</summary><dl><dt>Agent thread</dt><dd>{field(attempt.agent_thread_id)}</dd><dt>Branch</dt><dd>{field(attempt.branch_name)}</dd><dt>Commit</dt><dd>{field(attempt.commit_sha)}</dd><dt>Ошибка</dt><dd>{field(attempt.error)}</dd></dl></details>)}</div>}</section><section className="panel"><h2>Артефакты</h2>{artifactsQuery.isLoading ? <Loading /> : artifactsQuery.error ? <Failure error={artifactsQuery.error} /> : !artifactsQuery.data!.artifacts.length ? <Empty /> : <div className="list">{artifactsQuery.data!.artifacts.map((artifact, index) => <div className="list-row" key={String(artifact.id ?? index)}><div><strong>{field(artifact.name)}</strong><small>{field(artifact.type)}</small></div><code>{field(artifact.checksum)}</code></div>)}</div>}</section></div>;
}
