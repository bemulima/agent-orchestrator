const groups: Record<string, string> = {
  completed: "success", analyzed: "success", published: "success", approved: "success",
  running: "active", scanning: "active", verification: "active", review: "active",
  awaiting_approval: "attention", paused: "attention", blocked: "attention", changes_requested: "attention",
  failed: "danger", cancelled: "muted", rejected: "muted", expired: "muted",
};

const labels: Record<string, string> = {
  completed: "Завершено", analyzed: "Проанализирован", published: "Опубликовано", approved: "Одобрено",
  running: "Выполняется", scanning: "Сканирование", verification: "Проверка", review: "Ревью",
  awaiting_approval: "Ждёт согласования", paused: "Пауза", blocked: "Заблокировано",
  changes_requested: "Нужны изменения", failed: "Ошибка", cancelled: "Отменено",
  planned: "Запланировано", ready: "Готово к запуску", discussion: "Обсуждение", pending: "Ожидает",
  proposed: "Предложено", proposal_ready: "Предложение готово", applying: "Применяется",
};

export function StatusBadge({ status }: { status: string }) {
  return <span className={`status status-${groups[status] ?? "waiting"}`}><i />{labels[status] ?? status}</span>;
}
