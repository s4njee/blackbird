// @vitest-environment happy-dom
// Formatting utilities (POL-8.1): the table's numeric/date cells read through
// these, so they carry explicit coverage.
import { describe, expect, it, afterEach } from "vitest";
import {
  formatBytes,
  formatDate,
  formatInteger,
  formatPrefs,
  formatRate,
  formatRatio,
  formatSeedingTime,
  formatUptime,
  setFormatPrefs,
  splitBytes,
  splitRate,
} from "../src/lib/format.js";

describe("format", () => {
  afterEach(() => setFormatPrefs({ rateFormat: "binary", dateFormat: "local" }));
  it("splits bytes into value + unit pairs", () => {
    expect(splitBytes(0)).toEqual({ value: "0", unit: "B" });
    expect(splitBytes(512)).toEqual({ value: "512", unit: "B" });
    expect(splitBytes(1024)).toEqual({ value: "1", unit: "KB" });
    expect(splitBytes(41_200_000)).toEqual({ value: "39.29", unit: "MB" });
    expect(splitBytes(3 * 1024 ** 3)).toEqual({ value: "3", unit: "GB" });
    expect(formatBytes(3 * 1024 ** 3)).toBe("3 GB");
  });

  it("formats rates with a per-second unit and a zero floor", () => {
    expect(splitRate(0)).toEqual({ value: "0", unit: "B/s" });
    expect(splitRate(-5)).toEqual({ value: "0", unit: "B/s" });
    expect(splitRate(2048)).toEqual({ value: "2", unit: "KB/s" });
    expect(formatRate(2048)).toBe("2 KB/s");
  });

  it("formats ratios with two decimals and an em dash for junk", () => {
    expect(formatRatio(2.409)).toBe("2.41");
    expect(formatRatio(0)).toBe("0.00");
    expect(formatRatio(NaN)).toBe("—");
    expect(formatRatio(Infinity)).toBe("—");
  });

  it("formats counts with thousands separators", () => {
    expect(formatInteger(1204)).toBe("1,204");
    expect(formatInteger(0)).toBe("0");
  });

  it("renders today as clock-today and older dates compactly", () => {
    const today = new Date();
    today.setHours(12, 4, 0, 0);
    expect(formatDate(today)).toBe(
      `${today.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false })} today`,
    );
    expect(formatDate("2020-01-15T00:00:00Z")).toBe(
      new Date("2020-01-15T00:00:00Z").toLocaleDateString([], { month: "short", day: "numeric" }),
    );
    expect(formatDate("not-a-date")).toBe("—");
    expect(formatDate("")).toBe("—");
  });

  it("renders uptimes in the compact handoff forms", () => {
    expect(formatUptime(0)).toBe("0s");
    expect(formatUptime(-3)).toBe("0s");
    expect(formatUptime(NaN)).toBe("0s");
    expect(formatUptime(45)).toBe("45s");
    expect(formatUptime(252)).toBe("4m 12s");
    expect(formatUptime(2 * 86400 + 4 * 3600)).toBe("2d 04h");
    expect(formatUptime(41 * 86400 + 6 * 3600)).toBe("41d 06h");
  });

  it("shows seeding time since completion and a dash otherwise", () => {
    const finished = new Date("2026-01-01T00:00:00Z").getTime();
    expect(formatSeedingTime("2026-01-01T00:00:00Z", finished + 3600_000)).toBe("1h 00m");
    expect(formatSeedingTime("")).toBe("—");
    expect(formatSeedingTime("bogus")).toBe("—");
  });

  it("switches byte math between binary and decimal rates (ui.rate_format)", () => {
    expect(formatPrefs().rateFormat).toBe("binary");
    expect(splitBytes(1024)).toEqual({ value: "1", unit: "KB" });
    setFormatPrefs({ rateFormat: "decimal" });
    expect(formatPrefs().rateFormat).toBe("decimal");
    expect(splitBytes(1000)).toEqual({ value: "1", unit: "KB" });
    expect(splitBytes(1024)).toEqual({ value: "1.02", unit: "KB" });
    expect(formatRate(2_500_000)).toBe("2.5 MB/s");
    // Partial updates keep the other axis.
    setFormatPrefs({ dateFormat: "iso" });
    expect(formatPrefs()).toEqual({ rateFormat: "decimal", dateFormat: "iso" });
  });

  it("renders local dates by default and UTC ISO on request (ui.date_format)", () => {
    expect(formatPrefs().dateFormat).toBe("local");
    expect(formatDate("2020-01-15T12:04:00Z")).toBe(
      new Date("2020-01-15T12:04:00Z").toLocaleDateString([], { month: "short", day: "numeric" }),
    );
    setFormatPrefs({ dateFormat: "iso" });
    expect(formatDate("2020-01-15T12:04:00Z")).toBe("2020-01-15 12:04");
    expect(formatDate("not-a-date")).toBe("—");
  });
});
