import { expect, test } from "@playwright/test";
import { gotoConsole, rows, trackProblems } from "./helpers";

// Routing, persistence, and continuity (POL-8.6). Same suite runs against
// the Compose appliance with E2E_BASE_URL set.

test("settings section deep-links and back/forward work", async ({ page }) => {
  const problems = await trackProblems(page);
  await page.goto("#/settings/Bandwidth");
  await expect(page.getByRole("heading", { name: "Bandwidth" })).toBeVisible({ timeout: 15000 });
  await expect(page).toHaveURL(/#\/settings\/Bandwidth/);
  await page.getByRole("button", { name: "Back to console" }).click();
  await expect(rows(page).first()).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("heading", { name: "Bandwidth" })).toBeVisible();
  await page.goForward();
  await expect(rows(page).first()).toBeVisible();
  expect(problems).toEqual([]);
});

test("reload restores filter and focus from the URL", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  const first = rows(page).first();
  const hash = await first.getAttribute("data-hash");
  const name = await first.locator(".torrent-name").innerText();
  await page.goto(`#/?filter=${encodeURIComponent(name.slice(0, 8))}&focus=${hash}`);
  // Wait for the hash navigation to apply before reloading.
  await expect(page.locator(".filter-input")).toHaveValue(name.slice(0, 8));
  await page.reload();
  await expect(rows(page).first()).toBeVisible({ timeout: 15000 });
  await expect(page.locator(".filter-input")).toHaveValue(name.slice(0, 8));
  await expect(page.locator(`tr[data-hash="${hash}"]`)).toHaveClass(/selected/);
  expect(problems).toEqual([]);
});

test("sidebar width persists across reloads", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  const aside = page.locator("aside.sidebar");
  const before = await aside.evaluate((el) => el.getBoundingClientRect().width);
  const handle = page.locator(".sidebar-resize-handle");
  const box = await handle.boundingBox();
  await page.mouse.move(box!.x + box!.width / 2, box!.y + 10);
  await page.mouse.down();
  await page.mouse.move(box!.x + box!.width / 2 + 60, box!.y + 10, { steps: 5 });
  await page.mouse.up();
  const after = await aside.evaluate((el) => el.getBoundingClientRect().width);
  expect(after).toBeGreaterThan(before + 20);
  await page.reload();
  await expect(rows(page).first()).toBeVisible({ timeout: 15000 });
  const restored = await aside.evaluate((el) => el.getBoundingClientRect().width);
  expect(Math.abs(restored - after)).toBeLessThan(2);
  expect(problems).toEqual([]);
});

test("reset layout restores the sidebar width", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  const aside = page.locator("aside.sidebar");
  const handle = page.locator(".sidebar-resize-handle");
  const box = await handle.boundingBox();
  await page.mouse.move(box!.x + box!.width / 2, box!.y + 10);
  await page.mouse.down();
  await page.mouse.move(box!.x + box!.width / 2 + 80, box!.y + 10, { steps: 5 });
  await page.mouse.up();
  const wide = await aside.evaluate((el) => el.getBoundingClientRect().width);
  expect(wide).toBeGreaterThan(220);
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Interface" }).click();
  await page.getByRole("button", { name: "Reset layout" }).click();
  await expect(rows(page).first()).toBeVisible({ timeout: 15000 });
  const reset = await aside.evaluate((el) => el.getBoundingClientRect().width);
  expect(Math.abs(reset - 196)).toBeLessThan(2);
  expect(problems).toEqual([]);
});
