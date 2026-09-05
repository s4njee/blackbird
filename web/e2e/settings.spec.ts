import { expect, test, type Page } from "@playwright/test";
import { gotoConsole, trackProblems } from "./helpers";

// Settings flows (POL-8.4): same suite runs against the Compose appliance
// with E2E_BASE_URL set. The e2e server uses a temp config, so drafts never
// touch real operator files (this spec never saves).

const accentVar = (page: Page) =>
  page.evaluate(() => document.documentElement.style.getPropertyValue("--accent").trim());
const accentComputed = (page: Page) =>
  page.evaluate(() =>
    getComputedStyle(document.documentElement).getPropertyValue("--accent").trim(),
  );

test("accent previews live and reverts without saving", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Interface" }).click();
  // Empty operator accent: nothing inline, theme default resolved.
  expect(await accentVar(page)).toBe("");
  const committed = await accentComputed(page);
  expect(committed).toBeTruthy();

  await page.locator('.accent-control input[type="color"]').fill("#ff0000");
  await expect.poll(() => accentVar(page), { timeout: 5000 }).toBe("#ff0000");

  await page.getByRole("button", { name: "Revert" }).click();
  await expect.poll(() => accentVar(page), { timeout: 5000 }).toBe("");
  await expect.poll(() => accentComputed(page), { timeout: 5000 }).toBe(committed);
  expect(problems).toEqual([]);
});
