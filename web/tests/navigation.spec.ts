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
  await expect(page.locator("tbody tr").first()).toBeVisible();
  await page.locator("tbody tr").first().getByRole("link").click();
  await expect(page.getByRole("heading", { name: "DAG задач" })).toBeVisible();
  await expect(page.locator(".graph")).toBeVisible();
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
  await page.route("**/api/v1/commands", route => route.fulfill({ status: 201, json: { id: "command-new", text: "Создать безопасный план", status: "received" } }));
  await page.route("**/api/v1/commands/command-new/plan", route => route.fulfill({ status: 201, json: draft }));
  await page.route("**/api/v1/plans/plan-new", route => route.fulfill({ json: draft }));
  await page.goto("/plans/new");
  await page.getByLabel("Что нужно изменить").fill("Добавить безопасную проверку проекта без изменения публичного API");
  await page.getByText("orders", { exact: true }).click();
  await page.getByRole("button", { name: "Сгенерировать черновик" }).click();
  await expect(page).toHaveURL(/\/plans\/plan-new$/);
  await expect(page.getByRole("heading", { name: "Проверенный план" })).toBeVisible();
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
