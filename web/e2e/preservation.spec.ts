import { expect, test } from "@playwright/test";
import { gotoConsole, rows } from "./helpers";

test("watch, pin, reject cleanup, review and unpin", async ({ page }) => {
  await gotoConsole(page);
  const row = rows(page).first();
  const hash = (await row.getAttribute("data-hash"))!;
  await row.click();
  await page.getByRole("button", { name: "Preservation", exact: true }).click();
  const view = page.getByRole("main", { name: "Preservation watchlist" });
  await view.getByRole("button", { name: "Watch torrent", exact: true }).click();
  await expect(view.getByRole("button", { name: "Save pin" })).toBeVisible();
  await view.getByLabel("Protect from cleanup").check();
  await view.getByLabel("Reason", { exact: true }).fill("Preserve source material");
  await view.getByLabel("Review date (UTC)").fill("2026-01-01");
  await view.getByRole("button", { name: "Save pin" }).click();
  await expect(view.getByText(/Pin review due/)).toBeVisible();
  await expect(view.getByRole("button", { name: "Stop watching" })).toBeDisabled();
  await page.reload();
  await expect(view.getByLabel("Protect from cleanup")).toBeChecked();
  await expect(view.getByLabel("Reason", { exact: true })).toHaveValue("Preserve source material");
  const response = await page.request.post("/api/v1/torrents/action", {
    data: { action: "remove", hashes: [hash] },
  });
  expect((await response.json()).results[0]).toMatchObject({
    ok: false,
    error: expect.stringContaining("preservation pin"),
  });
  await view.getByRole("combobox", { name: "Watchlist filter" }).selectOption("due");
  await expect(view.getByRole("button", { name: "Save pin" })).toBeVisible();
  await view.getByRole("combobox", { name: "Watchlist filter" }).selectOption("all");
  await view.getByLabel("Protect from cleanup").uncheck();
  await view.getByRole("button", { name: "Save pin" }).click();
  await expect(view.getByRole("button", { name: "Stop watching" })).toBeEnabled();
  await view.getByRole("button", { name: "Stop watching" }).click();
  await expect(view.getByText("No watched torrents in this view.")).toBeVisible();
});
