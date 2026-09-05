import { expect, test } from "@playwright/test";
import { gotoConsole, rows } from "./helpers";

test("Why explains a selected torrent and opens its existing controls", async ({ page }) => {
  const mutations: string[] = [];
  page.on("request", (request) => {
    if (request.method() !== "GET" && request.url().includes("/api/"))
      mutations.push(request.url());
  });
  await gotoConsole(page);
  await rows(page).first().click();
  await page.getByRole("tab", { name: "Why?", exact: true }).click();
  const why = page.getByLabel("Torrent explanations");
  await expect(why.getByRole("heading", { name: /Observed state:/ })).toBeVisible();
  await expect(why).toContainText("Configured global bandwidth");
  await expect(why).toContainText("A recorded action does not prove the cause");
  await why.getByRole("button", { name: "Inspect speed history" }).first().click();
  await expect(page.getByRole("tab", { name: "Speed", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await page.getByRole("tab", { name: "Why?", exact: true }).click();
  await why.getByRole("button", { name: "Review global bandwidth" }).click();
  await expect(page).toHaveURL(/settings\/Bandwidth/);
  expect(mutations).toEqual([]);
});

test("Why stays usable at the supported desktop floor", async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 800 });
  await gotoConsole(page);
  await rows(page).first().click();
  await page.keyboard.press("9");
  await expect(page.getByRole("tab", { name: "Why?", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByLabel("Torrent explanations")).toContainText(
    "Configured global bandwidth",
  );
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
});
