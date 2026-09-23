import { expect, test, type Page } from "@playwright/test";

const clearPersonalCanvasState = async (page: Page) => {
  await page.addInitScript(() =>
    Object.keys(localStorage)
      .filter(
        (key) =>
          key.startsWith("architecture-v2-layout:v2:") ||
          key.startsWith("architecture-v2-viewport:v2:") ||
          key.startsWith("architecture-v2-groups:v2:"),
      )
      .forEach((key) => localStorage.removeItem(key)),
  );
};

test.describe("Architecture V2 refinement", () => {
  test.beforeEach(async ({ page }) => clearPersonalCanvasState(page));

  test("platform separates confirmed connections from compact unlinked CURRENT services", async ({
    page,
  }) => {
    await page.goto("/architecture-v2/current/platform");
    await expect(page.getByTestId("architecture-canvas")).toBeVisible();
    await expect(
      page.getByTestId("architecture-node-platform:unlinked"),
    ).toBeVisible();
    await expect(
      page.getByTestId("architecture-node-platform:unlinked"),
    ).toContainText("Unlinked / no confirmed relations");
    await expect(
      page.locator("[data-testid^='architecture-node-component:']").first(),
    ).toContainText("Connected architecture");
    expect(
      await page.locator("[data-route='orthogonal']").count(),
    ).toBeGreaterThan(0);
    const route = await page
      .locator("[data-route='orthogonal']")
      .first()
      .getAttribute("d");
    expect(route || "").not.toContain("C");
    expect(route || "").toContain("L");
  });

  test("audited gateway relation renders and explains its meaning", async ({
    page,
  }) => {
    await page.goto("/architecture-v2/current/platform");
    const gateway = page.getByTestId(
      "architecture-node-a7d21e56-6998-4465-9284-a10b61ef8e5c",
    );
    await gateway.click();
    await page.getByRole("button", { name: "Show on canvas" }).click();
    await expect(
      page.getByTestId(
        "architecture-node-25403d85-7047-4bfa-9711-66b7aa446956",
      ),
    ).toBeVisible();
    const relation = page
      .locator(
        "[data-testid^='architecture-edge-a7d21e56-6998-4465-9284-a10b61ef8e5c:25403d85-7047-4bfa-9711-66b7aa446956']",
      )
      .first();
    const point = await relation.evaluate((element) => {
      const path = element as SVGPathElement;
      const local = path.getPointAtLength(path.getTotalLength() / 2);
      const matrix = path.getScreenCTM();
      if (!matrix) throw new Error("edge has no screen matrix");
      return {
        x: local.x * matrix.a + local.y * matrix.c + matrix.e,
        y: local.x * matrix.b + local.y * matrix.d + matrix.f,
      };
    });
    await page.mouse.click(point.x, point.y);
    await expect(
      page.getByText("Confirmed relation", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("Confirmed internal relation", { exact: true }),
    ).toBeVisible();
    await expect(
      page
        .getByLabel("Architecture element inspector")
        .getByText("ms-go-auth", { exact: true }),
    ).toBeVisible();
  });

  test("service defaults to expanded transport groups and group toggle preserves viewport", async ({
    page,
  }) => {
    await page.goto("/architecture-v2/current/services/ms-go-auth");
    await expect(
      page.getByText("Operation grouping", { exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "Transport" })).toHaveClass(
      /primary/,
    );
    const natsGroup = page.getByTestId(
      "architecture-node-group:NATS request/reply",
    );
    await expect(natsGroup).toBeVisible();
    await expect(
      page.getByTestId("architecture-node-operation:nats-auth-verify-jwt"),
    ).toBeVisible();
    const before = await page
      .locator(".react-flow__viewport")
      .getAttribute("style");
    await natsGroup.click();
    await natsGroup.click();
    await expect(
      page.getByTestId("architecture-node-operation:nats-auth-verify-jwt"),
    ).toHaveCount(0);
    expect(
      await page.locator(".react-flow__viewport").getAttribute("style"),
    ).toBe(before);
    await natsGroup.click();
    await natsGroup.click();
    await expect(
      page.getByTestId("architecture-node-operation:nats-auth-verify-jwt"),
    ).toBeVisible();
    await page
      .getByTestId("architecture-node-operation:nats-auth-verify-jwt")
      .click();
    await page
      .getByTestId("architecture-node-operation:nats-auth-verify-jwt")
      .click();
    await expect(page).toHaveURL(/operations\/nats-auth-verify-jwt$/);
  });

  test("reference operation remains evidence-backed while dragged edges stay orthogonal", async ({
    page,
  }) => {
    await page.goto(
      "/architecture-v2/current/services/ms-go-linux-validator/operations/validate",
    );
    await expect(
      page.getByText("Rules of operation · 3", { exact: true }),
    ).toBeVisible();
    await page.getByText("Rules of operation · 3", { exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "Rules of operation" }),
    ).toBeVisible();
    await expect(
      page.getByTestId("architecture-edge-process:3:rules"),
    ).toHaveCount(0);
    const moving = page.getByTestId("architecture-node-data:0");
    const edge = page.getByTestId("architecture-edge-process:1:data:0");
    const before = await edge.getAttribute("d");
    const box = await moving.boundingBox();
    if (!box) throw new Error("data node is not measurable");
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(
      box.x + box.width / 2 + 280,
      box.y + box.height / 2 - 180,
      { steps: 8 },
    );
    await page.mouse.up();
    expect(await edge.getAttribute("d")).not.toBe(before);
    expect(await edge.getAttribute("d")).not.toContain("C");
  });

  test("offcanvas TARGET keeps exploration context and full TARGET route remains available", async ({
    page,
  }) => {
    await page.goto(
      "/architecture-v2/current/services/ms-go-linux-validator/operations/validate",
    );
    await page.waitForTimeout(350);
    const beforeURL = page.url();
    const beforeViewport = await page
      .getByTestId("architecture-canvas")
      .locator(".react-flow__viewport")
      .getAttribute("style");
    await page
      .getByRole("button", { name: "Propose a change", exact: true })
      .click();
    await expect(page.getByTestId("target-change-drawer")).toBeVisible();
    await expect(
      page.getByText("ms-go-linux-validator", { exact: true }),
    ).toBeVisible();
    await expect(page.getByLabel("What changes")).toBeVisible();
    await expect(page.getByTestId("target-impact")).toBeVisible();
    expect(page.url()).toBe(beforeURL);
    expect(
      await page
        .getByTestId("architecture-canvas")
        .locator(".react-flow__viewport")
        .getAttribute("style"),
    ).toBe(beforeViewport);
    await page.getByRole("button", { name: "Close target proposal" }).click();
    await expect(page.getByTestId("target-change-drawer")).toHaveCount(0);
    await page.goto("/architecture-v2/target");
    await expect(page.getByLabel("TARGET visual workspace")).toBeVisible();
  });

  test("Back keeps the platform route explorable", async ({ page }) => {
    await page.goto(
      "/architecture-v2/current/services/ms-go-linux-validator/operations/validate",
    );
    await page.getByRole("button", { name: "Back" }).click();
    await expect(page).toHaveURL(
      /\/architecture-v2\/current\/services\/ms-go-linux-validator$/,
    );
    await page.getByRole("button", { name: "Back" }).click();
    await expect(page).toHaveURL(/\/architecture-v2\/current\/platform$/);
  });
});
