import { expect, test } from "@playwright/test";
import { gotoConsole } from "./helpers";

test("attention inbox keeps snooze and acknowledgement after reload and links evidence", async ({
  page,
}) => {
  await gotoConsole(page);
  await expect
    .poll(
      async () => {
        const response = await page.request.get("/api/v1/attention");
        const body = await response.json();
        return (
          body.state?.incidents?.filter((i: { kind: string }) => i.kind === "tracker").length ?? 0
        );
      },
      { timeout: 15000 },
    )
    .toBeGreaterThan(0);
  await page.getByRole("button", { name: "Attention", exact: true }).click();
  const inbox = page.getByRole("main", { name: "Attention inbox" });
  await expect(inbox.getByRole("heading", { name: "Since your last visit" })).toBeVisible();
  const incident = inbox.getByRole("article").filter({ hasText: "Tracker messages" }).first();
  await incident.getByRole("button", { name: "Snooze 1 hour", exact: true }).click();
  await expect(incident.locator(".attention-state")).toHaveText("snoozed");
  await page.reload();
  await expect(incident.locator(".attention-state")).toHaveText("snoozed");
  await incident.getByRole("button", { name: "Acknowledge", exact: true }).click();
  await expect(incident.locator(".attention-state")).toHaveText("acknowledged");
  await page.reload();
  await expect(incident.locator(".attention-state")).toHaveText("acknowledged");
  await incident.getByText(/Affected torrents \(/).click();
  await incident.getByRole("button", { name: "Why?", exact: true }).first().click();
  await expect(page.getByLabel("Incident evidence")).toContainText("Observed state");
  await incident.getByRole("button", { name: "Recording", exact: true }).first().click();
  await expect(page.getByLabel("Session flight recorder")).toContainText("Recording position");
  await page.setViewportSize({ width: 900, height: 800 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(await inbox.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
});
