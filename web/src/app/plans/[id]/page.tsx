"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { apiGet, formatDate, shortID } from "@/lib/api";
import { planBundleSchema } from "@/lib/schemas";
import { Failure, Loading } from "@/components/page-state";
import { PlanGraph } from "@/components/plan-graph";
import { StatusBadge } from "@/components/status-badge";

const riskLabels: Record<string, string> = {
  low: "низкий",
  medium: "средний",
  high: "высокий",
  critical: "критический",
};

function editGuidance(status: string) {
  if (status === "discussion") return "Исправления вносятся кнопкой «Создать новую версию»: планировщик заново собирает весь DAG, а текущая версия остаётся в истории.";
  if (status === "awaiting_approval") return "Эта версия уже отправлена на согласование и зафиксирована fingerprint. Чтобы изменить её, отклоните согласование в списке планов и создайте заменяющий план.";
  return "Состав этой версии зафиксирован. Новые требования оформляются отдельным планом, чтобы не менять уже согласованный или запущенный DAG.";
}

export default function PlanDetailPage() {
  const id = String(useParams().id);
  const query = useQuery({ queryKey: ["plan", id], queryFn: () => apiGet(`/api/v1/plans/${id}`, planBundleSchema) });
  if (query.isLoading) return <Loading />;
  if (query.error) return <Failure error={query.error} />;
  const bundle = query.data!;
  const taskOrder = new Map(bundle.tasks.map((task, index) => [task.id, index + 1]));
  const prerequisites = new Map<string, number[]>();
  for (const dependency of bundle.dependencies) {
    prerequisites.set(dependency.task_id, [
      ...(prerequisites.get(dependency.task_id) ?? []),
      taskOrder.get(dependency.depends_on_task_id) ?? 0,
    ]);
  }

  return <div className="page plan-page">
    <header className="plan-header">
      <div className="plan-intro">
        <p className="eyebrow">План {shortID(bundle.plan.id)} · версия {bundle.plan.version}</p>
        <h1 className="plan-title">План изменений</h1>
        <p className="plan-summary">{bundle.plan.summary}</p>
        <p className="plan-meta">Риск: {riskLabels[bundle.plan.risk_level] ?? bundle.plan.risk_level} · обновлён {formatDate(bundle.plan.updated_at)}</p>
      </div>
      <div className="header-actions">
        <StatusBadge status={bundle.plan.status} />
        {bundle.plan.status === "discussion" && <Link className="button action-link" href={`/plans/${bundle.plan.id}/revise`}>Создать новую версию</Link>}
        {bundle.plan.status === "awaiting_approval" && <Link className="button action-link" href="/plans">Действия с планом</Link>}
      </div>
    </header>

    <section className="panel plan-guide" aria-labelledby="plan-guide-title">
      <div className="panel-title"><div><p className="section-kicker">Как это работает</p><h2 id="plan-guide-title">От цели до выполнения</h2></div></div>
      <div className="plan-guide-grid">
        <article><span>1</span><div><strong>Планировщик создаёт задачи</strong><p>Вы задаёте цель и выбираете репозитории. Для каждого репозитория создаётся отдельная задача с критериями приёмки, областью записи и проверками.</p></div></article>
        <article><span>2</span><div><strong>Зависимости задают порядок</strong><p>Стрелка идёт от обязательной задачи к той, которая её ждёт. Волна 0 выполняется первой; независимые задачи одной волны могут идти параллельно.</p></div></article>
        <article><span>3</span><div><strong>Правки создают новую версию</strong><p>Отдельные задачи не редактируются вручную: fingerprint связывает точный DAG и предложения issues. {editGuidance(bundle.plan.status)}</p></div></article>
      </div>
    </section>

    <section className="panel plan-graph-panel" aria-labelledby="plan-graph-title">
      <div className="panel-title"><div><p className="section-kicker">DAG задач</p><h2 id="plan-graph-title">Порядок выполнения</h2><p>Это порядок разработки, а не карта runtime-вызовов между сервисами.</p></div></div>
      <div className="graph-legend" aria-label="Обозначения схемы"><span><i className="legend-arrow">→</i> нужно завершить до</span><span><i className="legend-wave" /> одна волна выполняется параллельно</span><span><i className="legend-card" /> карточка открывает детали задачи</span></div>
      <PlanGraph tasks={bundle.tasks} dependencies={bundle.dependencies} />
    </section>

    <section className="panel" aria-labelledby="plan-tasks-title">
      <div className="panel-title"><div><h2 id="plan-tasks-title">Задачи плана</h2><p>Нажмите строку, чтобы увидеть критерии приёмки, write scope и команды проверки.</p></div><strong>{bundle.tasks.length}</strong></div>
      <div className="list">{bundle.tasks.map((task, index) => {
        const required = (prerequisites.get(task.id) ?? []).filter(Boolean);
        const dependencyText = required.length ? `После ${required.map(order => `задачи ${String(order).padStart(2, "0")}`).join(", ")}` : "Стартовая задача — обязательных предшественников нет";
        return <Link className="list-row plan-task-row" href={`/tasks/${task.id}`} key={task.id}>
          <span className="task-order">{String(index + 1).padStart(2, "0")}</span>
          <div><strong>{task.title}</strong><small>Волна {task.depth} · {dependencyText}</small></div>
          <StatusBadge status={task.status} />
        </Link>;
      })}</div>
    </section>

    <section className="detail-grid plan-technical-details">
      <article className="panel"><h2>Контроль версии</h2><p className="muted">Fingerprint фиксирует точный состав задач, зависимостей и предложений issues. После согласования он не меняется.</p><details><summary>Показать fingerprint</summary><code className="breakable">{bundle.plan.fingerprint}</code><p className="muted">Одобренный fingerprint: {bundle.plan.approved_fingerprint ?? "ещё нет"}</p></details></article>
      <article className="panel"><h2>Предложения задач</h2><strong className="detail-number">{bundle.work_items.length}</strong><p className="muted">По одному проекту issue на задачу; они готовятся до отправки плана на согласование.</p><small>Правок в обсуждении: {bundle.plan.discussion_revision}</small></article>
    </section>
  </div>;
}
