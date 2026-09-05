// Custom file themes + operator CSS (THM-9.4): server list, <style>
// injection, import/delete, and custom.css status. Pure resolution lives
// in lib/custom-themes.ts; ui.ts owns the resolved-theme composition.
import { createSignal } from "solid-js";
import {
  customThemeCss,
  customThemeId,
  resolveCustomPalette,
  type CustomThemeFile,
} from "../lib/custom-themes.js";

export type { CustomThemeFile };

const [customFiles, setCustomFiles] = createSignal<CustomThemeFile[]>([]);
const [customLoadError, setCustomLoadError] = createSignal("");
const [serverThemeErrors, setServerThemeErrors] = createSignal<string[]>([]);
export { customFiles, customLoadError, serverThemeErrors };

const ACTIVE_KEY = "blackbird.custom-theme.v1";
const STYLE_ID = "blackbird-custom-theme";
const CUSTOM_CSS_ID = "blackbird-custom-css";

const [activeCustomName, setActiveCustomName] = createSignal<string | null>(null);
export { activeCustomName };

/** Staged custom preview (THM-9.3 flow): string stages a file, null stages
 * clearing back to built-ins, undefined means untouched. */
const [previewCustom, setPreviewCustom] = createSignal<string | null | undefined>(undefined);
export { previewCustom };

/** Effective custom theme: staged preview, else the committed choice. */
export function effectiveCustomName(): string | null {
  const staged = previewCustom();
  return staged !== undefined ? staged : activeCustomName();
}

function readStorage(key: string): string | null {
  try {
    if (typeof window === "undefined" || typeof window.localStorage === "undefined") return null;
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeStorage(key: string, value: string | null) {
  try {
    if (typeof window === "undefined" || typeof window.localStorage === "undefined") return;
    if (value === null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, value);
  } catch {
    /* private mode */
  }
}

export function customFileByName(name: string): CustomThemeFile | undefined {
  return customFiles().find((f) => f.name === name);
}

/** Refresh the server theme list (valid files + load-error strings). */
export async function refreshCustomThemes(): Promise<void> {
  setCustomLoadError("");
  try {
    const response = await fetch("/api/v1/themes", { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const body = (await response.json()) as { themes?: CustomThemeFile[]; errors?: string[] };
    setCustomFiles(Array.isArray(body.themes) ? body.themes : []);
    setServerThemeErrors(
      Array.isArray(body.errors) ? body.errors.filter((e) => typeof e === "string") : [],
    );
  } catch (error) {
    setCustomLoadError(error instanceof Error ? error.message : "Could not load custom themes");
  }
}

/** Ensure the <style> block for a custom theme exists (idempotent). */
export function ensureCustomCss(name: string): boolean {
  const file = customFileByName(name);
  if (!file || typeof document === "undefined") return false;
  const id = customThemeId(name);
  let el = document.getElementById(STYLE_ID) as HTMLStyleElement | null;
  if (!el) {
    el = document.createElement("style");
    el.id = STYLE_ID;
    document.head.appendChild(el);
  }
  el.textContent = customThemeCss(id, resolveCustomPalette(file));
  el.dataset.themeName = name;
  return true;
}

/** Remove the custom-theme <style> block. */
export function removeCustomCss() {
  if (typeof document === "undefined") return;
  document.getElementById(STYLE_ID)?.remove();
}

/** Activate a custom theme by file name (persisted); null clears it. */
export function setActiveCustom(name: string | null) {
  if (name !== null && !ensureCustomCss(name)) return false;
  if (name === null) removeCustomCss();
  writeStorage(ACTIVE_KEY, name);
  setActiveCustomName(name);
  return true;
}

/** Stage a custom preview (live, unpersisted); null stages clearing. */
export function previewCustomTheme(name: string | null) {
  setPreviewCustom(name);
  if (name !== null) ensureCustomCss(name);
}

/** Commit a staged custom preview (persistence). Returns whether one existed. */
export function commitCustomPreview(): boolean {
  const staged = previewCustom();
  if (staged === undefined) return false;
  setPreviewCustom(undefined);
  setActiveCustom(staged);
  return true;
}

/** Drop a staged custom preview, restoring the committed theme CSS. */
export function discardCustomPreview() {
  if (previewCustom() === undefined) return;
  setPreviewCustom(undefined);
  const active = activeCustomName();
  if (active !== null) ensureCustomCss(active);
  else removeCustomCss();
}

/** Boot: refresh the list, re-apply a stored custom theme when its file
 * still exists (else fall back silently). */
export async function initCustomThemes(): Promise<void> {
  await refreshCustomThemes();
  const stored = readStorage(ACTIVE_KEY);
  if (stored && customFileByName(stored)) {
    ensureCustomCss(stored);
    setActiveCustomName(stored);
  } else {
    if (stored) writeStorage(ACTIVE_KEY, null);
    setActiveCustomName(null);
  }
}

/** Import a theme file (validates server-side, installs, applies). */
export async function importCustomTheme(content: string, name?: string): Promise<string> {
  const response = await fetch("/api/v1/themes/import", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(name ? { name, content } : { content }),
  });
  const body = (await response.json().catch(() => ({}))) as {
    theme?: CustomThemeFile;
    error?: { message?: string };
    message?: string;
  };
  if (!response.ok) throw new Error(body.error?.message || body.message || "Import failed");
  await refreshCustomThemes();
  const themeName = body.theme?.name;
  if (!themeName) throw new Error("Import succeeded without a theme");
  return themeName;
}

/** Delete an installed theme file (operator escape hatch for bad imports). */
export async function deleteCustomTheme(name: string): Promise<void> {
  const response = await fetch(`/api/v1/themes/${encodeURIComponent(name)}`, { method: "DELETE" });
  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as { message?: string };
    throw new Error(body.message || "Delete failed");
  }
  if (activeCustomName() === name) setActiveCustom(null);
  await refreshCustomThemes();
}

/* ---- custom.css ---- */

export type CustomCssStatus =
  | { state: "unknown" }
  | { state: "absent" }
  | { state: "ready"; bytes: number }
  | { state: "failed"; message: string };

const [customCssStatus, setCustomCssStatus] = createSignal<CustomCssStatus>({ state: "unknown" });
export { customCssStatus };

/** Fetch and inject the operator stylesheet last in <head> (THM-9.4). */
export async function refreshCustomCss(): Promise<void> {
  if (typeof document === "undefined") return;
  try {
    const response = await fetch("/api/v1/custom-css", { headers: { Accept: "text/css" } });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const text = await response.text();
    if (!text) {
      // Absent is the normal state: empty body, nothing to inject.
      document.getElementById(CUSTOM_CSS_ID)?.remove();
      setCustomCssStatus({ state: "absent" });
      return;
    }
    let el = document.getElementById(CUSTOM_CSS_ID) as HTMLStyleElement | null;
    if (!el) {
      el = document.createElement("style");
      el.id = CUSTOM_CSS_ID;
      document.head.appendChild(el);
    }
    el.textContent = text;
    setCustomCssStatus({ state: "ready", bytes: text.length });
  } catch (error) {
    setCustomCssStatus({
      state: "failed",
      message: error instanceof Error ? error.message : "Could not load custom.css",
    });
  }
}
