"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet, formatDate } from "@/lib/api";
import { agentUsageDashboardSchema } from "@/lib/schemas";
import { Empty, Failure, Loading } from "@/components/page-state";

function tokens(value: number) { return new Intl.NumberFormat("ru-RU").format(value); }

export default function UsagePage() {
  const query = useQuery({ queryKey: ["agent-usage"], queryFn: () => apiGet("/api/v1/agent-usage", agentUsageDashboardSchema), refetchInterval: 30_000 });
  if (query.isLoading) return <Loading />;
  if (query.error) return <Failure error={query.error} />;
  const data = query.data!;
  const windows = [["5 часов", data.five_hours], ["7 дней", data.seven_days], ["30 дней", data.thirty_days]] as const;
  return <div className="page">
    <header><div><p className="eyebrow">Budget control</p><h1>Модели и лимиты</h1><p>Фактический расход Codex по моделям и ролям без сохранения промптов.</p></div><span className="updated">{formatDate(data.generated_at)}</span></header>
    <section className="stats">{windows.map(([label, window]) => <article key={label}><span>{label}</span><strong>{window.runs}</strong><small>запусков · {window.failed_runs} ошибок</small></article>)}</section>
    <section className="panel"><div className="panel-title"><div><h2>Активная маршрутизация</h2><p>Модель выбирается сервером до запуска агента.</p></div></div><div className="stats"><article><span>Coder</span><strong>{data.routing.coder_model}</strong></article><article><span>Обычное review</span><strong>{data.routing.routine_review_model}</strong></article><article><span>Лёгкие операции</span><strong>{data.routing.fast_model}</strong></article><article><span>Сложный анализ</span><strong>{data.routing.deep_model}</strong></article><article><span>Issue / PR</span><strong>{data.routing.work_item_draft_mode}</strong></article></div></section>
    <section className="panel"><div className="panel-title"><div><h2>Budget Guard</h2><p>Sol ограничен в скользящем пятичасовом окне; xhigh требует отдельного разрешения.</p></div></div><div className="stats"><article><span>Режим</span><strong>{data.budget.mode}</strong></article><article><span>Sol за 5 часов</span><strong>{data.budget.deep_runs_five_hours}/{data.budget.deep_run_limit}</strong></article><article><span>Использовано</span><strong>{data.budget.utilization_percent}%</strong></article><article><span>xhigh</span><strong>{data.budget.xhigh_allowed ? "разрешён" : "запрещён"}</strong></article></div></section>
    <section className="panel"><div className="panel-title"><div><h2>Последние 30 дней по моделям</h2><p>Reasoning tokens показывают основной источник дорогих запусков.</p></div></div>
      {!data.thirty_days.by_model.length ? <Empty>Usage появится после следующего запуска агента</Empty> : <div className="list">{data.thirty_days.by_model.map(item => <div className="list-row" key={item.key}><div><strong>{item.key}</strong><small>{item.runs} запусков · {item.failed_runs} ошибок</small></div><span>in {tokens(item.input_tokens)}</span><span>cached {tokens(item.cached_input_tokens)}</span><span>out {tokens(item.output_tokens)}</span><span>reasoning {tokens(item.reasoning_output_tokens)}</span></div>)}</div>}
    </section>
    <section className="panel"><div className="panel-title"><div><h2>По ролям</h2><p>Помогает находить дорогие planner/reviewer/manager вызовы.</p></div></div><div className="list">{data.thirty_days.by_role.map(item => <div className="list-row" key={item.key}><div><strong>{item.key}</strong><small>{item.runs} запусков</small></div><span>{tokens(item.input_tokens + item.output_tokens + item.reasoning_output_tokens)} токенов</span></div>)}</div></section>
  </div>;
}
