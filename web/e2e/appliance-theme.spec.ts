import { expect, test } from "@playwright/test";
import { trackProblems } from "./helpers";

// Appliance theme smoke (THM-9.5): against the Compose appliance
// (E2E_BASE_URL + E2E_USER/E2E_PASSWORD), every theme loads the console
// and stats views without console/page/network errors. Skipped in the
// default fakertorrent suite — theme.spec.ts covers the mechanism there.
const BASE = process.env.E2E_BASE_URL ?? "";
const USER = process.env.E2E_USER ?? "";
const PASSWORD = process.env.E2E_PASSWORD ?? "";

test.skip(
  !BASE || !USER || !PASSWORD,
  "Compose appliance only: set E2E_BASE_URL/E2E_USER/E2E_PASSWORD",
);

if (USER && PASSWORD) {
  const username = USER;
  const password = PASSWORD;
  test.use({ httpCredentials: { username, password } });
}

for (const theme of ["dark", "light"]) {
  test(`appliance loads the ${theme} theme without errors`, async ({ page }) => {
    const problems = await trackProblems(page);
    await page.addInitScript((value) => localStorage.setItem("blackbird.theme.v1", value), theme);
    await page.goto("/");
    await expect(page.locator(".topbar")).toBeVisible({ timeout: 30000 });
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    await expect(page).toHaveTitle(/Blackbird Console/);
    await page.goto("/#/stats");
    await expect(page.locator(".stats-view")).toBeVisible({ timeout: 30000 });
    expect(problems).toEqual([]);
  });
}
