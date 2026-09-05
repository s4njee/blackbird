import { expect, test, type Page } from "@playwright/test";
import { gotoConsole, rows, trackProblems } from "./helpers";

// README screenshots (POL-8.7, backlog.md DOC-7.1): captured from the live
// fakertorrent-backed console so they never go stale. Run with
// `npm run e2e -- screenshots` (or the full suite); images land in
// docs/screenshots/ for the README to embed. Screenshots are informational —
// they never fail on pixels, only on console/page/network problems.
//
// THM-9.2: every shot runs in Dark and Light (`-<theme>.png`); the committed
// pairs are the visual regression baselines for both surfaces.

const THEME_KEY = "blackbird.theme.v1";

async function capture(page: Page, outDir: string, theme: string) {
  const shot = (name: string) => page.screenshot({ path: `${outDir}/${name}-${theme}.png` });

  await gotoConsole(page);
  await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
  await page.screenshot({ path: `${outDir}/console-${theme}.png` });

  // Detail panel with a selected torrent.
  await rows(page).first().click();
  await expect(page.locator(".detail-title")).not.toBeEmpty();
  await shot("console-detail");

  // Guided empty state via an impossible filter.
  await page.locator(".filter-input").fill("zzz-no-such-torrent-zzz");
  await expect(page.locator(".empty-table")).toBeVisible();
  await shot("console-empty-filter");
  await page.locator(".empty-table button").click();

  // Stats view.
  await page.goto("/#/stats");
  await expect(page.locator(".stats-view")).toBeVisible({ timeout: 15000 });
  await page.screenshot({ path: `${outDir}/stats-${theme}.png`, fullPage: true });

  // Settings > About (versions + update check).
  await page.goto("/#/settings/About");
  await expect(page.getByRole("heading", { name: "About" })).toBeVisible({ timeout: 15000 });
  await shot("settings-about");

  // Settings > General (session overview).
  await page.goto("/#/settings/General");
  await expect(page.getByRole("heading", { name: "General" })).toBeVisible({ timeout: 15000 });
  await shot("settings-general");

  // RSS view.
  await page.goto("/#/rss");
  await expect(page.locator(".rss-view")).toBeVisible({ timeout: 15000 });
  await shot("rss");

  // History view.
  await page.goto("/#/history");
  await expect(page.locator(".history-view, .rss-view, main")).toBeVisible({ timeout: 15000 });
  await shot("history");
}

const shots = test.extend<{ shotDir: string }>({});

shots(
  "screenshots: console, stats, settings, rss, history (dark + light)",
  async ({ page }, testInfo) => {
    test.setTimeout(180_000);
    const problems = await trackProblems(page);
    // Screenshots live in the repo docs tree (DOC-7.1 README source), two
    // levels up from web/e2e — never under web/, which is build output's home.
    const outDir = testInfo.file.replace(/\/e2e\/[^/]+$/, "/../docs/screenshots");

    for (const theme of ["dark", "light"]) {
      // Seed the stored choice before any page script runs so the boot
      // script paints the theme pre-paint, exactly like a returning user.
      await page.goto("/");
      await page.evaluate(
        ([key, value]) => localStorage.setItem(key, value as string),
        [THEME_KEY, theme],
      );
      await capture(page, outDir, theme);
    }

    expect(problems).toEqual([]);
  },
);
