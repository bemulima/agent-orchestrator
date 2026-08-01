import { expect, test } from "@playwright/test";

test("owner can navigate from dashboard to projects", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Обзор" })).toBeVisible();
  await page.getByRole("link", { name: "Проекты" }).click();
  await expect(page.getByRole("heading", { name: "Проекты" })).toBeVisible();
  await expect(page.locator("tbody tr").first()).toBeVisible();
});

test("owner can inspect a plan DAG", async ({ page }) => {
  await page.goto("/plans");
  await expect(page.getByRole("heading", { name: "История текущей локальной базы orchestrator" })).toBeVisible();
  await expect(page.getByText("Технический риск ≠ важность")).toBeVisible();
  await expect(page.getByText("Без внешних записей", { exact: true })).toBeVisible();
  await expect(page.locator(".plan-list-card").first()).toBeVisible();
  await page.locator(".plan-list-card").first().getByRole("link").click();
  await expect(page.getByRole("heading", { name: "Порядок выполнения" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "От цели до выполнения" })).toBeVisible();
  await expect(page.getByText("Стрелка идёт от обязательной задачи", { exact: false })).toBeVisible();
  await expect(page.locator(".graph")).toBeVisible();
  const graphTask = page.locator(".graph-task-card").first();
  await expect(graphTask).toHaveAttribute("href", /\/tasks\//);
  await graphTask.click();
  await expect(page).toHaveURL(/\/tasks\//);
});

test("owner can inspect run tasks and task attempts", async ({ page }) => {
  await page.goto("/runs");
  await expect(page.locator("tbody tr").first()).toBeVisible();
  await page.locator("tbody tr").first().getByRole("link").click();
  await expect(page.getByRole("heading", { name: "Задачи выполнения" })).toBeVisible();
  const task = page.locator(".list-row").first();
  await expect(task).toBeVisible();
  await task.click();
  await expect(page.getByRole("heading", { name: "Попытки" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Артефакты" })).toBeVisible();
});

test("owner can open approvals", async ({ page }) => {
  await page.goto("/approvals");
  await expect(page.getByRole("heading", { name: "Согласования" })).toBeVisible();
  await expect(page.locator(".approval-row").first()).toBeVisible();
});

test("dashboard remains usable on a mobile viewport", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Обзор" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Основная навигация" })).toBeVisible();
  await expect(page.getByText("Требует внимания", { exact: true }).first()).toBeVisible();
});

test("owner can connect a project and inspect discovery result", async ({ page }) => {
  await page.route("**/api/v1/projects/connect", route => route.fulfill({ json: {
    project: projectFixture,
    snapshot: { service_kind: "backend_service", language: "go", framework: "chi" },
    report: { status: "completed", warnings: [] },
  } }));
  await page.goto("/projects/connect");
  await page.getByLabel("Путь внутри контейнера").fill("/projects/microservices/orders");
  await page.getByRole("button", { name: "Подключить и сканировать" }).click();
  await expect(page.getByRole("heading", { name: "Проект подключён" })).toBeVisible();
  await expect(page.getByText("orders", { exact: true })).toBeVisible();
});

test("owner can generate a plan for selected projects", async ({ page }) => {
  const draft = planFixture("plan-new", 1, "discussion");
  await page.route("**/api/v1/projects", route => route.fulfill({ json: { projects: [projectFixture] } }));
  await page.route("**/api/v1/plans?limit=100", route => route.fulfill({ json: {
    items: [], has_more: false, work_item_gateway: "fake", external_writes_enabled: false,
  } }));
  await page.route("**/api/v1/commands", route => route.fulfill({ status: 201, json: { id: "command-new", text: "Создать безопасный план", status: "received" } }));
  await page.route("**/api/v1/commands/command-new/plan", route => route.fulfill({ status: 201, json: draft }));
  await page.route("**/api/v1/plans/plan-new", route => route.fulfill({ json: draft }));
  await page.goto("/plans/new");
  await page.getByLabel("Что нужно изменить").fill("Добавить безопасную проверку проекта без изменения публичного API");
  await page.getByText("orders", { exact: true }).click();
  await page.getByRole("button", { name: "Сгенерировать черновик" }).click();
  await expect(page).toHaveURL(/\/plans\/plan-new$/);
  await expect(page.getByRole("heading", { name: "План изменений" })).toBeVisible();
  await expect(page.getByText("Проверенный план", { exact: true })).toBeVisible();
});

test("owner can create an immutable plan revision", async ({ page }) => {
  const first = planFixture("plan-v1", 1, "discussion");
  const second = planFixture("plan-v2", 2, "discussion");
  await page.route("**/api/v1/plans/plan-v1", route => route.fulfill({ json: first }));
  await page.route("**/api/v1/plans/plan-v1/revisions", route => route.fulfill({ status: 201, json: second }));
  await page.route("**/api/v1/plans/plan-v2", route => route.fulfill({ json: second }));
  await page.goto("/plans/plan-v1/revise");
  await page.getByLabel("Что изменить").fill("Уточнить критерии приёмки и сохранить ограничения scope");
  await page.getByRole("button", { name: "Создать версию 2" }).click();
  await expect(page).toHaveURL(/\/plans\/plan-v2$/);
  await expect(page.getByText("версия 2", { exact: false }).first()).toBeVisible();
});

test("owner can discuss platform state and reject an action proposal", async ({ page }) => {
  const conversation = { id: "conversation-1", title: "Состояние платформы", scope_type: "workspace", scope_id: null, agent_thread_id: "thread-1", message_count: 0, created_at: "2026-07-26T10:00:00Z", updated_at: "2026-07-26T10:00:00Z" };
  const ownerMessage = { id: "message-owner", conversation_id: conversation.id, role: "owner", status: "completed", content: "Почему план остановлен?", references: [], created_at: "2026-07-26T10:01:00Z", completed_at: "2026-07-26T10:01:00Z" };
  const assistantMessage = { id: "message-assistant", conversation_id: conversation.id, role: "assistant", status: "completed", content: "План приостановлен после исчерпания review.", references: [{ resource_type: "run", resource_id: "run-1", label: "Остановленный run" }], created_at: "2026-07-26T10:01:01Z", completed_at: "2026-07-26T10:01:02Z" };
  const proposal = { id: "proposal-1", conversation_id: conversation.id, message_id: assistantMessage.id, action: "resume", resource_type: "run", resource_id: "run-1", title: "Продолжить run", description: "Возобновить после проверки причины.", risk_level: "high", fingerprint: null, status: "pending", created_at: "2026-07-26T10:01:02Z", decided_at: null };
  let detail: { conversation: typeof conversation; messages: Array<Record<string, unknown>>; proposals: Array<Record<string, unknown>> } = { conversation, messages: [], proposals: [] };
  await page.route("**/api/v1/conversations", async route => {
    if (route.request().method() === "GET") await route.fulfill({ json: { items: [conversation] } });
    else await route.continue();
  });
  await page.route("**/api/v1/conversations/conversation-1", route => route.fulfill({ json: detail }));
  await page.route("**/api/v1/conversations/conversation-1/messages", route => {
    detail = { conversation: { ...conversation, message_count: 2 }, messages: [ownerMessage, assistantMessage], proposals: [proposal] };
    return route.fulfill({ json: detail });
  });
  await page.route("**/api/v1/action-proposals/proposal-1/decision", route => {
    detail = { ...detail, proposals: [{ ...proposal, status: "rejected", decided_at: "2026-07-26T10:02:00Z" }] };
    return route.fulfill({ json: detail.proposals[0] });
  });
  await page.goto("/control/conversation-1");
  await page.getByLabel("Сообщение владельца").fill("Почему план остановлен?");
  await page.getByRole("button", { name: "Отправить" }).click();
  await expect(page.getByText("План приостановлен после исчерпания review.")).toBeVisible();
  await expect(page.getByRole("link", { name: "Остановленный run" })).toBeVisible();
  await page.getByRole("button", { name: "Отклонить предложение" }).click();
  await expect(page.getByText("rejected")).toBeVisible();
});

test("owner can inspect economic model routing and budget", async ({ page }) => {
  const emptyWindow = { since: "2026-07-26T00:00:00Z", runs: 0, failed_runs: 0, by_model: [], by_role: [] };
  await page.route("**/api/v1/agent-usage", route => route.fulfill({ json: {
    generated_at: "2026-07-26T12:00:00Z", five_hours: emptyWindow, seven_days: emptyWindow, thirty_days: emptyWindow,
    budget: { mode: "enforce", deep_model: "gpt-5.6-sol", deep_runs_five_hours: 2, deep_run_limit: 20, utilization_percent: 10, xhigh_allowed: false },
    routing: { coder_model: "gpt-5.3-codex-spark", routine_review_model: "gpt-5.6-terra", fast_model: "gpt-5.6-luna", standard_model: "gpt-5.6-terra", deep_model: "gpt-5.6-sol", work_item_draft_mode: "template" },
  } }));
  await page.goto("/usage");
  await expect(page.getByRole("heading", { name: "Модели и лимиты" })).toBeVisible();
  await expect(page.getByText("gpt-5.3-codex-spark")).toBeVisible();
  await expect(page.getByText("2/20")).toBeVisible();
});

const projectFixture = {
  id: "project-orders", name: "orders", status: "connected", repository_role: "service",
  default_branch: "main", current_branch: "main", head_commit: "0123456789abcdef", is_dirty: false,
  updated_at: "2026-07-26T10:00:00Z", local_path: "/projects/microservices/orders", git_url: null,
};

function planFixture(id: string, version: number, status: string) {
  return {
    plan: {
      id, command_id: "command-new", status, version, summary: "Проверенный план", risk_level: "medium",
      fingerprint: `fingerprint-${version}`, approved_fingerprint: null, discussion_revision: 0,
      updated_at: "2026-07-26T10:00:00Z",
    },
    tasks: [{
      id: `task-${version}`, plan_id: id, project_id: projectFixture.id, title: "Проверить orders",
      description: "Безопасно проверить проект", status: "planned", priority: 1, depth: 0, risk_level: "medium",
      acceptance_criteria: ["Проверки проходят"], write_scope: ["internal/**"], verification_commands: ["go test ./..."],
    }],
    dependencies: [], work_items: [], discussion: [], run: null,
  };
}
