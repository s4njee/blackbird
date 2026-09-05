// Interface settings section (POL-8.8; theme/density picker THM-9.2–9.4).
import { For, Show, createSignal } from "solid-js";
import { COLUMN_DEFINITIONS } from "../../lib/columns";
import { setFormatPrefs } from "../../lib/format";
import { readToken } from "../../lib/theme";
import {
  customThemeWarnings,
  exportThemeYaml,
  resolveCustomPalette,
} from "../../lib/custom-themes";
import { BUILTIN_PALETTES } from "../../lib/theme-palettes";
import { themeDef, THEMES, type Density, type ThemeChoice, type ThemeId } from "../../lib/themes";
import type { SectionProps } from "./types";
import { SettingRow } from "./SettingRow";
import { COLUMNS } from "./model";
import { columnLayoutConfig } from "../../store/ui";
import {
  activeAccents,
  activeThemeLabel,
  applyResolvedTheme,
  browserDensity,
  browserTheme,
  effectiveThemeChoice,
  prefersDark,
  previewBrowserTheme,
  previewDensityChoice,
  previewDensity,
  previewTheme,
  resolvedDensity,
  resolvedTheme,
  serverTheme,
} from "../../store/ui";
import {
  customCssStatus,
  customFileByName,
  customFiles,
  customLoadError,
  deleteCustomTheme,
  discardCustomPreview,
  effectiveCustomName,
  importCustomTheme,
  previewCustom,
  previewCustomTheme,
  refreshCustomCss,
  refreshCustomThemes,
  serverThemeErrors,
} from "../../store/custom.js";
import { confirmDialog } from "../../store/dialog";
import { noticePrefs } from "../../store/notifications";
import { requestBrowserPermission } from "../../store/notifications";
import { resetLayout } from "../../store/ui";
import { setBrowserEnabled } from "../../store/notifications";
import { setDefaultDurationMs } from "../../store/notifications";
import { showToast } from "../../store/ui";

/** Miniature theme preview: sidebar, rows, and accents from the theme's
 * palette excerpt (values pinned against CSS in test/themes.test.ts). */
function ThemeMini(props: { themeId: Parameters<typeof themeDef>[0] }) {
  const preview = () => themeDef(props.themeId).preview;
  return (
    <span class="theme-mini" aria-hidden="true" style={{ background: preview().bg }}>
      <span class="theme-mini-side" style={{ background: preview().panel }}>
        <i style={{ background: preview().accent }} />
        <i style={{ background: preview().text, opacity: 0.45 }} />
        <i style={{ background: preview().text, opacity: 0.25 }} />
      </span>
      <span class="theme-mini-main">
        <i style={{ background: preview().text, opacity: 0.7 }} />
        <i class="theme-mini-bar" style={{ background: preview().progress }} />
        <i style={{ background: preview().accent }} class="theme-mini-chip" />
      </span>
    </span>
  );
}

export function InterfaceSection(props: SectionProps) {
  const setUI = (patch: Partial<typeof props.draft.ui>) =>
    props.setDraft((current) => ({ ...current, ui: { ...current.ui, ...patch } }));
  const serverId = (): ThemeId => {
    const s = serverTheme();
    if (s !== "system") return s;
    return prefersDark() ? "dark" : "light";
  };
  // Picker selection reflects the staged preview, else the committed choice.
  const pickedTheme = (): ThemeChoice | null =>
    previewTheme() !== null ? previewTheme() : browserTheme();
  const pickedDensity = (): Density =>
    previewDensity() !== null ? (previewDensity() as Density) : browserDensity();
  // "Set as server default" pushes the previewed (or current) theme into
  // the YAML draft and saves immediately.
  const pushServerDefault = () => {
    const target = previewTheme() ?? effectiveThemeChoice();
    setUI({ theme: target });
    props.saveSettings();
  };
  // Picking a built-in card stages it and clears any custom staging so the
  // preview always shows exactly one theme.
  const pickBuiltin = (choice: ThemeChoice | null) => {
    if (effectiveCustomName() !== null) previewCustomTheme(null);
    previewBrowserTheme(choice);
  };
  const [importError, setImportError] = createSignal("");

  /** Import a picked YAML file: validates + installs server-side, then previews. */
  const importPickedFile = async (input: HTMLInputElement) => {
    setImportError("");
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    try {
      const content = await file.text();
      const name = await importCustomTheme(content);
      previewCustomTheme(name);
      applyResolvedTheme();
      showToast(`Theme "${name}" installed. Save to keep it.`);
    } catch (error) {
      const message = error instanceof Error ? error.message : "Import failed";
      setImportError(message);
      showToast(message, { kind: "error" });
    }
  };

  /** Delete the effectively-active custom theme (with confirmation). */
  const deleteActiveCustom = async () => {
    const name = effectiveCustomName();
    if (name === null) return;
    const confirmed = await confirmDialog({
      title: "Delete custom theme",
      body: `Remove "${name}" from the server themes directory?`,
      confirmLabel: "Delete",
      danger: true,
    });
    if (!confirmed) return;
    try {
      await deleteCustomTheme(name);
      showToast(`Theme "${name}" deleted.`);
    } catch (error) {
      showToast(error instanceof Error ? error.message : "Delete failed", { kind: "error" });
    }
  };

  /** Export the visible theme (palette + accent + density) as a YAML file. */
  const exportCurrentTheme = () => {
    const custom = effectiveCustomName();
    const file = custom !== null ? customFileByName(custom) : undefined;
    const id = resolvedTheme();
    const base = file
      ? resolveCustomPalette(file)
      : (BUILTIN_PALETTES[id] ?? BUILTIN_PALETTES["dark"]);
    const yaml = exportThemeYaml({
      name: file?.name ?? activeThemeLabel(),
      description: file?.description ?? `Exported ${activeThemeLabel()}`,
      dark: file?.dark ?? !["light", "classic"].includes(id),
      accents:
        file && Array.isArray(file.accents) && file.accents.length === 5
          ? [...file.accents]
          : [...activeAccents()],
      palette: { ...base },
      accent: readToken("--accent"),
      density: resolvedDensity(),
    });
    const blob = new Blob([yaml], { type: "text/yaml" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `blackbird-theme-${id.replace(/^custom-/, "")}.yml`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
  };

  /** Human status line for the operator stylesheet. */
  const customCssDescription = () => {
    const status = customCssStatus();
    switch (status.state) {
      case "absent":
        return "No custom.css — create one beside the config to inject extra CSS after the theme.";
      case "ready":
        return `custom.css active (${status.bytes} bytes, injected after the theme).`;
      case "failed":
        return `custom.css failed: ${status.message}`;
      default:
        return "";
    }
  };
  const allColumns = () =>
    props.draft.ui.columns.length
      ? props.draft.ui.columns
      : COLUMN_DEFINITIONS.map((column) => ({
          key: column.key,
          visible: true,
          width: column.width,
        }));
  return (
    <section>
      <h1>Interface</h1>
      <div class="settings-fields">
        <SettingRow label="Theme" hint="preview live · Save keeps it in this browser">
          <div class="theme-cards" role="radiogroup" aria-label="Theme">
            <button
              type="button"
              class={`theme-card ${pickedTheme() === null ? "active" : ""}`}
              role="radio"
              aria-checked={pickedTheme() === null}
              aria-label="Operator default theme"
              onClick={() => pickBuiltin(null)}
            >
              <ThemeMini themeId={serverId()} />
              Operator default
              <small>{themeDef(serverId()).label}</small>
            </button>
            <For each={THEMES}>
              {(theme) => (
                <button
                  type="button"
                  class={`theme-card ${pickedTheme() === theme.id ? "active" : ""}`}
                  role="radio"
                  aria-checked={pickedTheme() === theme.id}
                  aria-label={`${theme.label} theme`}
                  onClick={() => pickBuiltin(theme.id)}
                >
                  <ThemeMini themeId={theme.id} />
                  {theme.label}
                </button>
              )}
            </For>
            <button
              type="button"
              class={`theme-card ${pickedTheme() === "system" ? "active" : ""}`}
              role="radio"
              aria-checked={pickedTheme() === "system"}
              aria-label="System theme"
              onClick={() => pickBuiltin("system" as ThemeChoice)}
            >
              <ThemeMini themeId={prefersDark() ? "dark" : "light"} />
              System
              <small>{resolvedTheme()}</small>
            </button>
          </div>
        </SettingRow>
        <SettingRow label="Operator default theme" hint="ui.theme · new browsers">
          <div class="sort-control">
            <select
              value={props.draft.ui.theme || "dark"}
              onChange={(event) => setUI({ theme: event.currentTarget.value })}
            >
              <For each={THEMES}>{(theme) => <option value={theme.id}>{theme.label}</option>}</For>
              <option value="system">System</option>
            </select>
            <button
              type="button"
              class="settings-add-row"
              disabled={previewTheme() === null}
              aria-label="Set as server default"
              title={
                previewTheme() === null
                  ? "Preview a theme above first"
                  : `Push ${previewTheme()} to ui.theme and save`
              }
              onClick={(event) => {
                event.preventDefault();
                pushServerDefault();
              }}
            >
              Set as server default
            </button>
          </div>
        </SettingRow>
        <SettingRow label="Density" hint="preview live · Save keeps it in this browser">
          <div class="theme-cards" role="radiogroup" aria-label="Density">
            <button
              type="button"
              class={`theme-card ${pickedDensity() === "dense" ? "active" : ""}`}
              role="radio"
              aria-checked={pickedDensity() === "dense"}
              aria-label="Dense density"
              onClick={() => previewDensityChoice("dense")}
            >
              Dense
              <small>handoff rows</small>
            </button>
            <button
              type="button"
              class={`theme-card ${pickedDensity() === "comfortable" ? "active" : ""}`}
              role="radio"
              aria-checked={pickedDensity() === "comfortable"}
              aria-label="Comfortable density"
              onClick={() => previewDensityChoice("comfortable")}
            >
              Comfortable
              <small>+4px rows</small>
            </button>
          </div>
        </SettingRow>
        <SettingRow
          label="Custom themes"
          hint="themes/*.yml in the config directory"
          error={importError() || undefined}
        >
          <div>
            <Show when={customLoadError()}>
              <p class="settings-intro">
                Could not load custom themes: {customLoadError()}{" "}
                <button type="button" onClick={() => void refreshCustomThemes()}>
                  Retry
                </button>
              </p>
            </Show>
            <Show when={serverThemeErrors().length > 0}>
              <p class="settings-intro">
                {serverThemeErrors().length} theme file(s) skipped — see the server log:{" "}
                {serverThemeErrors().slice(0, 3).join(" · ")}
                {serverThemeErrors().length > 3 ? " · …" : ""}
              </p>
            </Show>
            <Show
              when={customFiles().length > 0}
              fallback={
                <p class="settings-intro">
                  No custom themes installed. Drop a versioned YAML file into <code>themes/</code>{" "}
                  beside the config (see <code>docs/custom-themes.md</code>), or import one below.
                </p>
              }
            >
              <div class="theme-cards" role="radiogroup" aria-label="Custom themes">
                <For each={customFiles()}>
                  {(file) => {
                    const active =
                      effectiveCustomName() === file.name || previewCustom() === file.name;
                    const mini = () => {
                      const pal = resolveCustomPalette(file);
                      return {
                        bg: pal["bg-app"] ?? "#101214",
                        panel: pal["bg-panel"] ?? "#0e1012",
                        text: pal["text-body"] ?? "#d6d9dd",
                        accent: (file.accents ?? [])[0] ?? "#35418f",
                        progress: pal["progress-active"] ?? "#2f9dff",
                      };
                    };
                    const warnings = () => customThemeWarnings(file);
                    return (
                      <button
                        type="button"
                        class={`theme-card ${active ? "active" : ""}`}
                        role="radio"
                        aria-checked={active}
                        aria-label={`${file.name} custom theme${warnings().length ? `, ${warnings().length} warnings` : ""}`}
                        onClick={() => {
                          previewCustomTheme(file.name);
                          applyResolvedTheme();
                        }}
                      >
                        <span
                          class="theme-mini"
                          aria-hidden="true"
                          style={{ background: mini().bg }}
                        >
                          <span class="theme-mini-side" style={{ background: mini().panel }}>
                            <i style={{ background: mini().accent }} />
                            <i style={{ background: mini().text, opacity: 0.45 }} />
                            <i style={{ background: mini().text, opacity: 0.25 }} />
                          </span>
                          <span class="theme-mini-main">
                            <i style={{ background: mini().text, opacity: 0.7 }} />
                            <i class="theme-mini-bar" style={{ background: mini().progress }} />
                            <i style={{ background: mini().accent }} class="theme-mini-chip" />
                          </span>
                        </span>
                        {file.name}
                        <Show when={warnings().length > 0}>
                          <small>⚠ {warnings().length}</small>
                        </Show>
                      </button>
                    );
                  }}
                </For>
              </div>
              <Show when={effectiveCustomName() !== null}>
                <div class="settings-intro">
                  <For
                    each={customThemeWarnings(
                      customFileByName(effectiveCustomName()!) ?? { name: "", palette: {} },
                    )}
                  >
                    {(warning) => <p>⚠ {warning}</p>}
                  </For>
                  <button
                    type="button"
                    class="settings-add-row"
                    aria-label={`Delete custom theme ${effectiveCustomName() ?? ""}`}
                    onClick={(event) => {
                      // Inside a SettingRow <label>: suppress label click
                      // forwarding so only this action fires.
                      event.preventDefault();
                      void deleteActiveCustom().then(() => {
                        discardCustomPreview();
                        applyResolvedTheme();
                      });
                    }}
                  >
                    Delete {effectiveCustomName()}
                  </button>
                </div>
              </Show>
            </Show>
            <div class="sort-control theme-import-row">
              <label class="settings-add-row" role="button" tabindex="0">
                Import theme file
                <input
                  type="file"
                  accept=".yml,.yaml"
                  hidden
                  onChange={(event) => void importPickedFile(event.currentTarget)}
                />
              </label>
              <button
                type="button"
                class="settings-add-row"
                onClick={() => void exportCurrentTheme()}
              >
                Export current theme
              </button>
              <button
                type="button"
                class="settings-add-row"
                onClick={(event) => {
                  event.preventDefault();
                  void refreshCustomThemes();
                }}
              >
                Rescan
              </button>
            </div>
          </div>
        </SettingRow>
        <SettingRow label="Operator stylesheet" hint="custom.css · served with app auth">
          <div>
            <Show
              when={customCssStatus().state !== "unknown"}
              fallback={<p class="settings-intro">Checking for custom.css…</p>}
            >
              <p class="settings-intro">
                {customCssDescription()}
                <Show when={customCssStatus().state !== "absent"}>
                  {" "}
                  <button type="button" onClick={() => void refreshCustomCss()}>
                    Reload
                  </button>
                </Show>
              </p>
            </Show>
            <p class="settings-intro">
              Stability warning: custom.css overrides internals that may change between releases;
              prefer theme files (documented in <code>docs/custom-themes.md</code>).
            </p>
          </div>
        </SettingRow>
        <SettingRow
          label="Accent color"
          hint="ui.accent · empty follows the theme"
          error={props.errors.accent}
        >
          <div>
            <div class="accent-control">
              <input
                type="color"
                value={props.draft.ui.accent || activeAccents()[0]}
                onInput={(event) => setUI({ accent: event.currentTarget.value })}
              />
              <input
                value={props.draft.ui.accent}
                placeholder="theme default"
                onInput={(event) => setUI({ accent: event.currentTarget.value })}
              />
            </div>
            <div class="accent-presets" aria-label={`Accent presets for ${activeThemeLabel()}`}>
              <button
                type="button"
                class={`accent-preset theme-default ${!props.draft.ui.accent ? "active" : ""}`}
                title="Theme default"
                aria-label="Use theme default accent"
                onClick={() => setUI({ accent: "" })}
              />
              <For each={activeAccents()}>
                {(preset) => (
                  <button
                    type="button"
                    class={`accent-preset ${props.draft.ui.accent.toLowerCase() === preset ? "active" : ""}`}
                    style={{ background: preset }}
                    title={preset}
                    aria-label={`Use accent ${preset}`}
                    onClick={() => setUI({ accent: preset })}
                  />
                )}
              </For>
            </div>
          </div>
        </SettingRow>
        <SettingRow label="Default sort" hint="ui.sort">
          <div class="sort-control">
            <select
              value={props.draft.ui.sort.column}
              onChange={(event) =>
                setUI({ sort: { ...props.draft.ui.sort, column: event.currentTarget.value } })
              }
            >
              <For each={COLUMNS}>{(item) => <option value={item}>{item}</option>}</For>
            </select>
            <select
              value={props.draft.ui.sort.dir}
              onChange={(event) =>
                setUI({
                  sort: {
                    ...props.draft.ui.sort,
                    dir: event.currentTarget.value as "asc" | "desc",
                  },
                })
              }
            >
              <option value="asc">Ascending</option>
              <option value="desc">Descending</option>
            </select>
          </div>
        </SettingRow>
        <SettingRow label="Date format" hint="ui.date_format">
          <select
            value={props.draft.ui.date_format}
            onChange={(event) => {
              setUI({ date_format: event.currentTarget.value });
              setFormatPrefs({
                dateFormat: event.currentTarget.value === "iso" ? "iso" : "local",
              });
            }}
          >
            <option value="local">Local date</option>
            <option value="iso">ISO date</option>
          </select>
        </SettingRow>
        <SettingRow label="Rate format" hint="ui.rate_format">
          <select
            value={props.draft.ui.rate_format}
            onChange={(event) => {
              setUI({ rate_format: event.currentTarget.value });
              setFormatPrefs({
                rateFormat: event.currentTarget.value === "decimal" ? "decimal" : "binary",
              });
            }}
          >
            <option value="binary">Binary (MiB)</option>
            <option value="decimal">Decimal (MB)</option>
          </select>
        </SettingRow>
      </div>
      <h2>Notifications</h2>
      <p class="settings-intro">
        Browser-local: toasts queue with severity and actions, and the bell lists the last 50.
      </p>
      <div class="settings-fields">
        <SettingRow
          label="Browser notifications"
          hint="completions, failures, RSS matches · per browser"
        >
          <div>
            <span class="dialog-skip">
              <input
                type="checkbox"
                checked={noticePrefs().browserEnabled}
                onChange={(event) =>
                  void (async () => {
                    if (!event.currentTarget.checked) {
                      setBrowserEnabled(false);
                      return;
                    }
                    if (typeof Notification === "undefined") {
                      showToast("This browser does not support notifications.", {
                        kind: "warning",
                      });
                      event.currentTarget.checked = false;
                      return;
                    }
                    const result = await requestBrowserPermission();
                    if (result !== "granted") {
                      showToast("Notifications are blocked — allow them in the browser settings.", {
                        kind: "warning",
                      });
                    }
                  })()
                }
              />
              <span>Notify when the tab is hidden</span>
            </span>
          </div>
        </SettingRow>
        <SettingRow label="Toast duration" hint="seconds · 0 keeps until dismissed · per browser">
          <input
            type="number"
            min="0"
            value={Math.round(noticePrefs().defaultDurationMs / 1000)}
            onInput={(event) => setDefaultDurationMs(Number(event.currentTarget.value || 0) * 1000)}
          />
        </SettingRow>
      </div>
      <h2>Browser layout</h2>
      <p class="settings-intro">
        Sidebar width, detail panel size and tab, column layout, sort order, and last route are
        stored per browser. Resetting clears them back to the operator defaults; saved filters and
        notification preferences are kept.
      </p>
      <button type="button" class="settings-add-row" onClick={resetLayout}>
        Reset layout
      </button>
      <h2>Operator default columns</h2>
      <p class="settings-intro">
        This layout is used when a browser has no saved layout. Runtime changes are stored per
        browser from the torrent header.
      </p>
      <button
        type="button"
        class="settings-add-row"
        onClick={() => setUI({ columns: columnLayoutConfig(), visible_columns: [] })}
      >
        Use current browser layout
      </button>
      <div class="column-checks">
        <For each={COLUMN_DEFINITIONS}>
          {(column) => (
            <label>
              <input
                type="checkbox"
                checked={Boolean(allColumns().find((item) => item.key === column.key)?.visible)}
                onChange={(event) => {
                  const next = allColumns().map((item) =>
                    item.key === column.key
                      ? { ...item, visible: event.currentTarget.checked }
                      : item,
                  );
                  setUI({ columns: next, visible_columns: [] });
                }}
              />
              {column.label}
            </label>
          )}
        </For>
      </div>
    </section>
  );
}
