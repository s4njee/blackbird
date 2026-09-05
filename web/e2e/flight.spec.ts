import { expect, test } from "@playwright/test";
import { gotoConsole, rows } from "./helpers";

test("flight recorder links a manual request and downloads the reviewed incident", async ({
  page,
}) => {
  await gotoConsole(page);
  const row = rows(page).first();
  const hash = await row.getAttribute("data-hash");
  await row.click();
  const action = await page.request.post("/api/v1/torrents/action", {
    data: { action: "start", hashes: [hash] },
  });
  expect(action.ok()).toBe(true);
  await page.getByRole("tab", { name: "Logger", exact: true }).click();
  await page.getByRole("button", { name: "Flight recorder", exact: true }).click();
  const recorder = page.getByLabel("Session flight recorder");
  await expect(recorder).toContainText("Recording position");
  await recorder
    .getByRole("button", { name: /rpc_result · start/ })
    .last()
    .click();
  await recorder.getByRole("button", { name: "Inspect linked intent" }).click();
  await expect(
    recorder.getByRole("heading", { name: "intent · start", exact: true }),
  ).toBeVisible();
  await recorder.getByRole("button", { name: "Preview incident export" }).click();
  const preview = recorder.getByLabel("Incident export preview");
  await expect(preview).toBeVisible();
  const json = JSON.parse(await preview.locator("pre").innerText());
  expect(json.version).toBe(1);
  const download = page.waitForEvent("download");
  await preview.getByRole("button", { name: "Download reviewed bundle" }).click();
  expect((await download).suggestedFilename()).toBe("blackbird-incident-v1.json");
});

test("global flight recorder fits the supported desktop floor", async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 800 });
  await gotoConsole(page);
  await page.goto("/#/history");
  await page.getByRole("button", { name: "Flight recorder", exact: true }).click();
  await expect(page.getByLabel("Session flight recorder")).toContainText("Recording position");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
