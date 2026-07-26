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
