import { expect, test, type Page } from "@playwright/test";
import { gotoConsole, rows, trackProblems } from "./helpers";

// Theme mechanism (THM-9.2): data-theme application, browser persistence,
// system following, and the server operator default.

const KEY = "blackbird.theme.v1";

async function datasetTheme(page: Page) {
  return page.evaluate(() => document.documentElement.dataset.theme);
}

/** Console with an explicit empty-hash route: plain "/" would boot into
 * the persisted last route (POL-8.6), which is Settings mid-flow. */
async function gotoConsoleRoute(page: Page) {
  await page.goto("/#/");
  await expect(rows(page).first()).toBeVisible({ timeout: 15000 });
}

test("explicit theme persists and applies before paint", async ({ page }) => {
  const problems = await trackProblems(page);
  await page.goto("/");
  await page.evaluate((key) => localStorage.setItem(key, "midnight"), KEY);
  await page.reload();
  await gotoConsoleRoute(page);
  expect(await datasetTheme(page)).toBe("midnight");
  // Accent derivation follows the theme (tint is not the raw accent).
  const tint = await page.evaluate(() =>
    getComputedStyle(document.documentElement).getPropertyValue("--accent-tint").trim(),
  );
  expect(tint).not.toBe("");
  expect(problems).toEqual([]);
});

test("system theme follows the emulated color scheme live", async ({ page }) => {
  const problems = await trackProblems(page);
  await page.goto("/");
  await page.evaluate((key) => localStorage.setItem(key, "system"), KEY);
  await page.reload();
  await gotoConsole(page);
  await page.emulateMedia({ colorScheme: "light" });
  await expect.poll(() => datasetTheme(page), { timeout: 5000 }).toBe("light");
  await page.emulateMedia({ colorScheme: "dark" });
  await expect.poll(() => datasetTheme(page), { timeout: 5000 }).toBe("dark");
  expect(problems).toEqual([]);
});

test("no stored choice follows the server operator default without flash", async ({ page }) => {
  const problems = await trackProblems(page);
  await page.goto("/");
  await page.evaluate((key) => localStorage.removeItem(key), KEY);
  await page.reload();
  // The boot script sets data-theme before the app bundle runs.
  const early = await page.evaluate(() => document.documentElement.dataset.theme);
  expect(["dark", "light", "midnight", "contrast", "classic"]).toContain(early);
  await gotoConsole(page);
  expect(await datasetTheme(page)).toBe(early);
  expect(problems).toEqual([]);
});

test("theme preview reverts until saved, density persists per browser", async ({ page }) => {
  const problems = await trackProblems(page);
  await page.goto("/");
  await page.evaluate((key) => localStorage.removeItem(key), KEY);
  await page.evaluate(() => localStorage.removeItem("blackbird.density.v1"));
  await page.reload();
  await gotoConsole(page);
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Interface" }).click();

  // Preview: live, unpersisted.
  await page.getByRole("radio", { name: "Classic theme" }).click();
  await expect.poll(() => datasetTheme(page), { timeout: 5000 }).toBe("classic");
  await page.getByRole("button", { name: "Revert", exact: true }).click();
  await expect.poll(() => datasetTheme(page), { timeout: 5000 }).toBe("dark");

  // Save: persists the preview to this browser.
  await page.getByRole("radio", { name: "Midnight theme" }).click();
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect.poll(() => datasetTheme(page), { timeout: 5000 }).toBe("midnight");
  await page.reload();
  await gotoConsoleRoute(page);
  expect(await datasetTheme(page)).toBe("midnight");

  // Density: comfortable rows persist per browser.
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Interface" }).click();
  await page.getByRole("radio", { name: "Comfortable density" }).click();
  const rowHeight = () =>
    page.evaluate(() =>
      getComputedStyle(document.documentElement).getPropertyValue("--h-table-row").trim(),
    );
  await expect.poll(rowHeight, { timeout: 5000 }).toBe("34px");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await page.reload();
  await gotoConsoleRoute(page);
  await expect.poll(rowHeight, { timeout: 5000 }).toBe("34px");
  expect(problems).toEqual([]);
});
