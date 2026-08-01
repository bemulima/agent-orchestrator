"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { apiGet, formatDate, shortID } from "@/lib/api";
import { plansPageSchema, type PlanSummary } from "@/lib/schemas";
import { Failure, Loading } from "@/components/page-state";
import { ResourceActions } from "@/components/resource-actions";
import { StatusBadge } from "@/components/status-badge";
import { groupPlans, publicationCopy, riskLabel, sourceLabel, statusHint } from "./plan-list-view";

function PlanScope({ plan }: { plan: PlanSummary }) {
  const publication = publicationCopy(plan);
  return <div className="plan-scope"><span className={`plan-scope-label scope-${publication.tone}`}>{publication.label}</span><small>{sourceLabel(plan.source_kind)}</small><small>{publication.detail}</small></div>;
}

function PlanActions({ plan, gateway, externalWrites }: { plan: PlanSummary; gateway?: string; externalWrites: boolean }) {
  const remaining = Math.max(0, plan.issue_count - plan.published_issues);
  const publicationLabel = gateway === "fake" ? `Создать ${remaining} fake issues` : `Опубликовать ${remaining} issues`;
  const publicationWarning = gateway === "fake"
    ? "Будут созданы только записи-симуляции на github.example.test. Внешних запросов не будет. При частичном результате повторный запуск продолжит оставшиеся issues."
    : externalWrites
      ? "Будут созданы реальные issues во внешнем GitHub. Проверьте состав и fingerprint плана."
      : "Включён dry-run: запрос будет проверен настроенным GitHub gateway без реальной внешней публикации.";
  return <ResourceActions
    resourceType="plan" resourceID={plan.id} fingerprint={plan.fingerprint} actions={plan.allowed_actions}
    actionLabels={{ publish_issues: publicationLabel }}
    confirmations={{
      approve: "Будет одобрен точный fingerprint. План перейдёт к этапу публикации; issues и выполнение автоматически не запускаются.",
      publish_issues: publicationWarning,
      run: "Будет создан локальный запуск orchestrator. После старта следите за задачами и причинами остановки.",
      prepare_issues: "Orchestrator подготовит локальные issue-предложения для каждой задачи. Внешних публикаций на этом шаге нет.",
      submit: "Текущая версия и её issue-предложения будут зафиксированы fingerprint и отправлены владельцу на решение.",
    }}
  />;
}

function PlanCard({ plan, gateway, externalWrites }: { plan: PlanSummary; gateway?: string; externalWrites: boolean }) {
  return <article className="plan-list-card">
    <div className="plan-card-heading">
      <Link title={plan.summary} href={`/plans/${plan.id}`}><strong>{plan.summary}</strong><small>{shortID(plan.id)} · версия {plan.version}</small></Link>
      <StatusBadge status={plan.status} />
    </div>
    <div className="plan-card-grid">
      <div><span className="plan-card-label">Контур</span><PlanScope plan={plan} /></div>
      <div><span className="plan-card-label">Следующий шаг</span><p>{statusHint(plan)}</p></div>
      <div><span className="plan-card-label">Технический риск</span><span className={`plan-risk risk-${plan.risk_level}`}>{riskLabel(plan.risk_level)}<small>максимум по задачам, не приоритет</small></span></div>
      <div><span className="plan-card-label">Прогресс</span><strong>{plan.completed_tasks} / {plan.task_count}</strong><small>{plan.attention_tasks ? `${plan.attention_tasks} требуют внимания` : "нет задач, требующих внимания"}</small></div>
    </div>
    <div className="plan-card-footer"><small>Обновлён {formatDate(plan.updated_at)}</small><PlanActions plan={plan} gateway={gateway} externalWrites={externalWrites} /></div>
  </article>;
}

function PlanCards({ items, gateway, externalWrites }: { items: PlanSummary[]; gateway?: string; externalWrites: boolean }) {
  return <div className="plan-card-list">{items.map(plan => <PlanCard key={plan.id} plan={plan} gateway={gateway} externalWrites={externalWrites} />)}</div>;
}

function PlanSection({ title, description, items, gateway, externalWrites }: { title: string; description: string; items: PlanSummary[]; gateway?: string; externalWrites: boolean }) {
  if (!items.length) return null;
  return <section className="panel plan-list-section"><div className="panel-title"><div><h2>{title}</h2><p>{description}</p></div><strong>{items.length}</strong></div><PlanCards items={items} gateway={gateway} externalWrites={externalWrites} /></section>;
}

export default function PlansPage() {
  const query = useQuery({ queryKey: ["plans"], queryFn: () => apiGet("/api/v1/plans?limit=100", plansPageSchema) });
  const gateway = query.data?.work_item_gateway;
  const externalWrites = query.data?.external_writes_enabled ?? false;
  if (query.isLoading) return <Loading />;
  if (query.error) return <Failure error={query.error} />;
  const plans = query.data!.items;
  const groups = groupPlans(plans);
  const localOnly = plans.filter(item => item.issue_publication === "none" || item.issue_publication === "draft").length;
  const simulations = plans.filter(item => item.issue_publication === "simulation").length;
  const external = plans.filter(item => item.issue_publication === "external").length;
  const publicationMode = gateway === "fake"
    ? { label: "Симуляция", text: "Публикация создаёт только fake issues на github.example.test. Реальных внешних записей нет.", tone: "safe" }
    : externalWrites
      ? { label: "Внешние записи включены", text: "Кнопка публикации создаёт реальные issues в настроенном GitHub.", tone: "danger" }
      : { label: "GitHub dry-run", text: "Gateway проверяет публикацию, но реальные внешние issues не создаёт.", tone: "safe" };

  return <div className="page plans-page">
    <header><div><p className="eyebrow">Планирование</p><h1>Планы</h1><p>Сохранённые решения, черновики и история локальных запусков.</p></div><Link className="primary action-link" href="/plans/new">Создать план</Link></header>
    <section className="panel plans-explainer" aria-labelledby="plans-origin-title">
      <div><p className="section-kicker">Что это за записи</p><h2 id="plans-origin-title">История текущей локальной базы orchestrator</h2><p>План появляется из запроса владельца или существующего issue. Автотесты работают в отдельной временной базе и не добавляют сюда строки. Локальный черновик ещё ничего не меняет снаружи, симуляция использует только fake GitHub, а метка «Legacy-план» показывает старую неполную запись без обязательных issue-предложений.</p></div>
      <div className="risk-explainer"><strong>Технический риск ≠ важность</strong><p><code>critical</code> означает максимальную сложность и опасность изменений среди задач плана. Это не срочность, не бизнес-приоритет и не команда немедленно запускать работу.</p></div>
    </section>
    <section className={`panel publication-mode mode-${publicationMode.tone}`} aria-label="Режим внешних записей">
      <div><p className="section-kicker">Текущий режим</p><h2>{publicationMode.label}</h2></div><p>{publicationMode.text}</p>
    </section>
    <section className="panel plan-lifecycle" aria-labelledby="plan-lifecycle-title">
      <div className="panel-title"><div><p className="section-kicker">Правила переходов</p><h2 id="plan-lifecycle-title">Как план доходит до выполнения</h2></div></div>
      <ol>
        <li><strong>1. Подготовка</strong><span>Черновик плана → по одному локальному issue-предложению на задачу.</span></li>
        <li><strong>2. Решение владельца</strong><span>Подтверждается точный fingerprint. Это ещё ничего не публикует.</span></li>
        <li><strong>3. Публикация issues</strong><span>Создаются fake, dry-run или реальные issues — согласно режиму выше.</span></li>
        <li><strong>4. Выполнение</strong><span>Только после полной публикации появляется отдельная кнопка запуска.</span></li>
      </ol>
    </section>
    <section className="stats plans-stats" aria-label="Сводка по планам">
      <article className={groups.decisions.length ? "stat-attention" : ""}><span>Требуют решения</span><strong>{groups.decisions.length}</strong><small>подтвердить или отклонить</small></article>
      <article><span>Без внешних записей</span><strong>{localOnly}</strong><small>только планы и локальные issue-черновики</small></article>
      <article><span>Симуляции</span><strong>{simulations}</strong><small>публикации в github.example.test</small></article>
      <article><span>Внешние публикации</span><strong>{external}</strong><small>реальные issues вне orchestrator</small></article>
    </section>
    <PlanSection title="Требуют вашего решения" description="Эти версии зафиксированы fingerprint и ждут явного подтверждения или отклонения." items={groups.decisions} gateway={gateway} externalWrites={externalWrites} />
    <PlanSection title="Подготовка" description="Здесь план обсуждается и получает локальные issue-предложения. Ничего внешнего ещё не создаётся." items={groups.preparation} gateway={gateway} externalWrites={externalWrites} />
    <PlanSection title="Одобрены — ждут следующего шага" description="Одобрение не запускает работу. Сначала публикуются все issues; затем отдельно появляется запуск." items={groups.approved} gateway={gateway} externalWrites={externalWrites} />
    <PlanSection title="История выполнения" description="Сохранённые запуски orchestrator. В поле «Следующий шаг» показана конкретная причина паузы или ошибки." items={groups.executions} gateway={gateway} externalWrites={externalWrites} />
    {groups.archive.length > 0 && <details className="panel plans-archive"><summary><span><strong>Архив</strong><small>Отменённые, заменённые и завершённые версии не требуют действий.</small></span><b>{groups.archive.length}</b></summary><PlanCards items={groups.archive} gateway={gateway} externalWrites={externalWrites} /></details>}
  </div>;
}
