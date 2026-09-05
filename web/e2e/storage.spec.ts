import { expect, test } from "@playwright/test";
import { gotoConsole, rows } from "./helpers";

test("intake reviews unknown sizes and refreshes before adding", async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 800 });
  await gotoConsole(page);
  let loads = 0,
    forecasts = 0;
  page.on("request", (r) => {
    if (r.url().endsWith("/api/v1/torrents/add")) loads++;
    if (r.url().endsWith("/api/v1/storage/forecast")) forecasts++;
  });
  await page.getByRole("button", { name: "+ Add torrent", exact: true }).click();
  const modal = page.getByLabel("Add torrent", { exact: true });
  await modal
    .locator("textarea")
    .fill("magnet:?xt=urn:btih:0123456789012345678901234567890123456789");
  await modal.getByRole("button", { name: "Add", exact: true }).click();
  await expect(modal.getByLabel("Storage forecast")).toContainText("Review the refreshed forecast");
  expect(loads).toBe(0);
  await modal.getByLabel("Unknown batch downloads (GiB, optional)").fill("1");
  await modal.getByRole("button", { name: "Refresh forecast", exact: true }).click();
  await expect(modal.getByLabel("Storage forecast")).toContainText("Peak projected usage");
  expect(await modal.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  await modal.getByRole("button", { name: "Add", exact: true }).click();
  await expect.poll(() => loads).toBe(1);
  expect(forecasts).toBeGreaterThanOrEqual(3);
});

test("move dialog exposes a forecast before starting work", async ({ page }) => {
  await gotoConsole(page);
  await rows(page).first().click();
  await page.getByRole("button", { name: "Move data", exact: true }).click();
  const modal = page.getByRole("dialog", { name: "Move torrent data" });
  await expect(modal.getByLabel("Storage forecast")).toBeVisible();
  await expect(modal.getByRole("button", { name: "Continue", exact: true })).toBeDisabled();
  await modal.getByRole("button", { name: "Set directory only", exact: true }).click();
  await modal.getByRole("button", { name: "Use this folder", exact: true }).click();
  let started = 0;
  await page.route("**/api/v1/torrents/move", async (route) => {
    started++;
    await route.fulfill({ json: { id: "reviewed", status: "completed", results: [] } });
  });
  await modal.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(modal.getByLabel("Storage forecast")).toContainText("Review the refreshed forecast");
  expect(started).toBe(0);
  await modal.getByRole("button", { name: "Continue", exact: true }).click();
  await expect.poll(() => started).toBe(1);
});
