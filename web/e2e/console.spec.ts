import { expect, test } from "@playwright/test";
import { gotoConsole, rows, trackProblems } from "./helpers";

// Console flows against the fakertorrent-backed server (POL-8.1): same
// suite runs against the Compose appliance with E2E_BASE_URL set (QA-5.3).

test("console loads the torrent table", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  expect(await rows(page).count()).toBeGreaterThan(0);
  await expect(page).toHaveTitle(/Blackbird Console/);
  expect(problems).toEqual([]);
});

test("filter narrows the table and clears", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  const name = await rows(page).first().locator(".torrent-name").innerText();
  await page.locator(".filter-input").fill(name.slice(0, 8));
  await expect(rows(page).first()).toBeVisible();
  await page.locator(".filter-input").fill("zzz-no-such-torrent-zzz");
  await expect(page.locator(".empty-table")).toBeVisible();
  await page.locator(".empty-table button").click();
  await expect(rows(page).first()).toBeVisible();
  expect(problems).toEqual([]);
});

test("row selection opens the detail panel with tabs", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  await rows(page).first().click();
  await expect(page.locator(".detail-title")).not.toBeEmpty();
  await expect(page.locator(".torrent-table tr.selected")).toHaveCount(1);
  await page.getByRole("tab", { name: "Peers" }).click();
  await expect(page.locator(".peers-table")).toBeVisible();
  await page.keyboard.press("End");
  await expect(page.locator(".torrent-table tr.selected")).toHaveCount(1);
  expect(problems).toEqual([]);
});

test("remove asks in-app and cancels on Escape", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  const count = await rows(page).count();
  await rows(page).first().click();
  await page.keyboard.press("Delete");
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("Remove 1 torrent");
  await expect(page.locator(".dialog-confirm")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  expect(await rows(page).count()).toBe(count);
  expect(problems).toEqual([]);
});
