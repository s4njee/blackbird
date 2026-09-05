import { expect, type Page } from "@playwright/test";

// Shared browser-suite helpers (POL-8.1): every spec fails on console
// errors, page errors, failed requests, and non-OK API responses.
export async function trackProblems(page: Page): Promise<string[]> {
  const problems: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() === "error") problems.push(`console: ${msg.text()}`);
  });
  page.on("pageerror", (err) => problems.push(`pageerror: ${err}`));
  page.on("requestfailed", (request) =>
    problems.push(`requestfailed: ${request.url()} ${request.failure()?.errorText}`),
  );
  page.on("response", (response) => {
    const url = response.url();
    if (
      !response.ok() &&
      (url.includes("/api/v1/") || url.includes("/ws")) &&
      response.status() !== 101
    ) {
      problems.push(`http ${response.status()}: ${url}`);
    }
  });
  return problems;
}

export const rows = (page: Page) => page.locator(".torrent-table tbody tr[data-hash]");

export async function gotoConsole(page: Page) {
  await page.goto("/");
  await expect(rows(page).first()).toBeVisible({ timeout: 15000 });
}
