"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { apiGet, formatDate, shortID } from "@/lib/api";
import { planBundleSchema } from "@/lib/schemas";
import { Failure, Loading } from "@/components/page-state";
import { PlanGraph } from "@/components/plan-graph";
import { StatusBadge } from "@/components/status-badge";

export default function PlanDetailPage() {
  const id = String(useParams().id);
  const query = useQuery({ queryKey: ["plan", id], queryFn: () => apiGet(`/api/v1/plans/${id}`, planBundleSchema) });
  if (query.isLoading) return <Loading />;
  if (query.error) return <Failure error={query.error} />;
  const bundle = query.data!;
  return <div className="page"><header><div><p className="eyebrow">План {shortID(bundle.plan.id)} · версия {bundle.plan.version}</p><h1>{bundle.plan.summary}</h1><p>Риск: {bundle.plan.risk_level} · обновлён {formatDate(bundle.plan.updated_at)}</p></div><StatusBadge status={bundle.plan.status} /></header><section className="panel"><div className="panel-title"><div><h2>DAG задач</h2><p>Зависимости и текущее состояние.</p></div></div><PlanGraph tasks={bundle.tasks} dependencies={bundle.dependencies} /></section><section className="panel"><h2>Задачи</h2><div className="list">{bundle.tasks.map(task => <Link className="list-row" href={`/tasks/${task.id}`} key={task.id}><div><strong>{task.title}</strong><small>{shortID(task.project_id)} · волна {task.depth}</small></div><StatusBadge status={task.status} /></Link>)}</div></section><section className="detail-grid"><article className="panel"><h2>Fingerprint</h2><code className="breakable">{bundle.plan.fingerprint}</code><p className="muted">Approved: {bundle.plan.approved_fingerprint ?? "—"}</p></article><article className="panel"><h2>Work items</h2><strong>{bundle.work_items.length}</strong><p className="muted">Discussion revision: {bundle.plan.discussion_revision}</p></article></section></div>;
}
