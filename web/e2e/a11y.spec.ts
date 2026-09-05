import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { gotoConsole } from "./helpers";

// Automated axe checks (POL-8.5): zero serious or critical violations on the
// main routes. Same suite runs against the Compose appliance with
// E2E_BASE_URL set.

async function seriousOn(page: Page) {
  const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa"]).analyze();
  return results.violations.filter((v) => v.impact === "serious" || v.impact === "critical");
}

test("console has zero serious axe violations", async ({ page }) => {
  await gotoConsole(page);
  const serious = await seriousOn(page);
  expect(
    serious.map((v) => `${v.id}: ${v.nodes.length} nodes`),
    JSON.stringify(serious, null, 2),
  ).toEqual([]);
});

test("settings has zero serious axe violations", async ({ page }) => {
  await gotoConsole(page);
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("navigation", { name: "Settings sections" }).waitFor({ timeout: 15000 });
  const serious = await seriousOn(page);
  expect(
    serious.map((v) => `${v.id}: ${v.nodes.length} nodes`),
    JSON.stringify(serious, null, 2),
  ).toEqual([]);
});
