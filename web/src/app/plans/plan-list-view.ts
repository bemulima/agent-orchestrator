import type { PlanSummary } from "@/lib/schemas";

export type PlanGroups = {
  decisions: PlanSummary[];
  preparation: PlanSummary[];
  approved: PlanSummary[];
  executions: PlanSummary[];
  archive: PlanSummary[];
};

const riskLabels: Record<string, string> = {
  low: "Низкий",
  medium: "Средний",
  high: "Высокий",
  critical: "Критический",
};

export function groupPlans(items: PlanSummary[]): PlanGroups {
  const groups: PlanGroups = { decisions: [], preparation: [], approved: [], executions: [], archive: [] };
  for (const item of items) {
    if (item.superseded_by_plan_id || item.status === "cancelled" || item.status === "completed") groups.archive.push(item);
    else if (item.run_id || ["running", "paused", "failed"].includes(item.status)) groups.executions.push(item);
    else if (item.status === "awaiting_approval") groups.decisions.push(item);
    else if (item.status === "approved") groups.approved.push(item);
    else groups.preparation.push(item);
  }
  return groups;
}

export function riskLabel(value: string) {
  return riskLabels[value] ?? value;
}

export function sourceLabel(value: string) {
  return value === "issue" ? "Из существующего issue" : "Из запроса владельца";
}

export function publicationCopy(plan: PlanSummary) {
  switch (plan.issue_publication) {
    case "draft":
      return {
        label: "Локальный черновик",
        detail: `${plan.issue_count} предложений issues · не опубликованы`,
        tone: "draft",
      };
    case "simulation":
      return {
        label: "Симуляция",
        detail: `${plan.published_issues} fake issues · github.example.test`,
        tone: "simulation",
      };
    case "external":
      return {
        label: "Внешняя публикация",
        detail: `${plan.published_issues} issues опубликовано во внешнюю систему`,
        tone: "external",
      };
    default:
      return plan.status === "awaiting_approval"
        ? { label: "Legacy-план", detail: "Создан до обязательных issue-предложений", tone: "legacy" }
        : { label: "Только план", detail: "Предложения issues не создавались", tone: "none" };
  }
}

export function statusHint(plan: PlanSummary) {
  if (plan.superseded_by_plan_id) return "Заменён явно выбранным новым планом";
  if (plan.run_error) return runErrorCopy(plan.run_error);
  if (plan.run_id) return "Сохранённый запуск: откройте план, чтобы увидеть задачи";
  if (plan.status === "awaiting_approval") return "Нужно подтвердить или отклонить";
  if (plan.status === "discussion") return "Можно уточнить план и подготовить issues";
  if (plan.status === "approved" && plan.issue_count !== plan.task_count) return "Legacy: публикация невозможна без issue-предложения для каждой задачи";
  if (plan.status === "approved" && plan.published_issues < plan.issue_count) return "Одобрен; следующий шаг — публикация issues";
  if (plan.status === "approved") return "Issues опубликованы; план готов к запуску";
  return "Выполнение ещё не запускалось";
}

export function runErrorCopy(error: string) {
  if (error.includes("review changes remain after the maximum review count")) {
    return "Reviewer всё ещё требует изменений после лимита проверок. Нужна корректировка плана.";
  }
  if (error.includes("completed result cannot request dependent tasks")) {
    return "Агент вернул противоречивый результат; запуск остановлен проверкой протокола.";
  }
  return `Причина запуска: ${error}`;
}
