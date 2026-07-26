"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { apiGet, formatDate } from "@/lib/api";
import { runDetailSchema, runsPageSchema, tasksPageSchema } from "@/lib/schemas";
import { Failure, Loading } from "@/components/page-state";
import { ResourceActions } from "@/components/resource-actions";
import { StatusBadge } from "@/components/status-badge";

export default function RunDetailPage() {
  const id = String(useParams().id);
  const runQuery = useQuery({ queryKey: ["run", id], queryFn: () => apiGet(`/api/v1/runs/${id}`, runDetailSchema) });
  const summaryQuery = useQuery({ queryKey: ["runs"], queryFn: () => apiGet("/api/v1/runs?limit=100", runsPageSchema) });
  const planID = runQuery.data?.plan_id;
  const tasksQuery = useQuery({ queryKey: ["tasks", planID], enabled: Boolean(planID), queryFn: () => apiGet(`/api/v1/tasks?limit=100&plan_id=${planID}`, tasksPageSchema) });
  if (runQuery.isLoading) return <Loading />;
  if (runQuery.error) return <Failure error={runQuery.error} />;
  const run = runQuery.data!;
  const summary = summaryQuery.data?.items.find(item => item.id === id);
  return <div className="page"><header><div><p className="eyebrow">Workflow</p><h1>{run.workflow_id}</h1><p>Обновлён {formatDate(run.updated_at)}</p></div><StatusBadge status={run.status} /></header>{summary && <ResourceActions resourceType="run" resourceID={run.id} actions={summary.allowed_actions} />} {run.error && <div className="alert"><strong>Причина остановки</strong><p>{run.error}</p></div>}<section className="panel"><div className="panel-title"><div><h2>Задачи выполнения</h2><p>План {run.plan_id}</p></div><Link href={`/plans/${run.plan_id}`}>Открыть план</Link></div>{tasksQuery.isLoading ? <Loading /> : tasksQuery.error ? <Failure error={tasksQuery.error} /> : <div className="list">{tasksQuery.data!.items.map(task => <Link className="list-row" href={`/tasks/${task.id}`} key={task.id}><div><strong>{task.project_name}</strong><small>{task.title}</small></div><StatusBadge status={task.status} /><span>{task.attempt_count} попыток</span></Link>)}</div>}</section></div>;
}
