"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { apiGet, formatDate } from "@/lib/api";
import { dashboardSchema } from "@/lib/schemas";
import { Empty, Failure, Loading } from "@/components/page-state";
import { StatusBadge } from "@/components/status-badge";

function resourceLink(type: string, id: string) {
  if (type === "plan") return `/plans/${id}`;
  if (type === "task") return `/tasks/${id}`;
  return "/approvals";
}

export default function DashboardPage() {
  const query = useQuery({ queryKey: ["dashboard"], queryFn: () => apiGet("/api/v1/dashboard", dashboardSchema) });
  if (query.isLoading) return <Loading />;
  if (query.error) return <Failure error={query.error} />;
  const data = query.data!;
  return (
    <div className="page">
      <header><div><p className="eyebrow">Центр управления</p><h1>Обзор</h1><p>Текущее состояние проектов и выполняющихся планов.</p></div><span className="updated">{formatDate(data.generated_at)}</span></header>
      <section className="stats">
        <article><span>Проекты</span><strong>{data.counts.projects}</strong></article>
        <article><span>Активные планы</span><strong>{data.counts.active_plans}</strong></article>
        <article><span>Активные задачи</span><strong>{data.counts.active_tasks}</strong></article>
        <article className={data.counts.attention_required ? "stat-attention" : ""}><span>Требует внимания</span><strong>{data.counts.attention_required}</strong></article>
      </section>
      <section className="panel">
        <div className="panel-title"><div><h2>Требует внимания</h2><p>Паузы, ошибки и решения владельца.</p></div></div>
        {!data.attention.length ? <Empty>Нет элементов, требующих решения</Empty> : <div className="list">
          {data.attention.map(item => <Link className="list-row" href={resourceLink(item.resource_type, item.resource_id)} key={`${item.resource_type}-${item.resource_id}`}>
            <div><strong>{item.title}</strong><small>{item.reason}</small></div><StatusBadge status={item.status} /><time>{formatDate(item.updated_at)}</time>
          </Link>)}
        </div>}
      </section>
      <section className="panel">
        <div className="panel-title"><div><h2>Активные выполнения</h2><p>Выполняющиеся и приостановленные workflow.</p></div><Link href="/runs">Все выполнения</Link></div>
        {!data.active_runs.length ? <Empty>Сейчас нет активных выполнений</Empty> : <div className="list">{data.active_runs.map(run => {
          const item = run as Record<string, unknown>;
          return <Link className="list-row" href={`/runs/${String(item.id)}`} key={String(item.id)}><div><strong>{String(item.plan_summary)}</strong><small>{String(item.completed_tasks)} из {String(item.task_count)} задач</small></div><StatusBadge status={String(item.status)} /></Link>;
        })}</div>}
      </section>
    </div>
  );
}
