// About section (POL-8.7 states, POL-8.8 location).
import { Show, onMount } from "solid-js";
import { SettingRow } from "./SettingRow";
import {
  checkForUpdates,
  prefs as updatePrefs,
  refreshVersion,
  release as releaseState,
  setUpdateCheckEnabled,
  setUpdateCheckEndpoint,
  version as versionInfo,
  versionFailed as versionLoadFailed,
  versionLoading as versionLoadingState,
} from "../../store/version";

/** About panel (POL-8.7): build identity, daemon versions, and the opt-in
 * release check. The check is browser-local: nothing leaves the browser
 * until the user enables it and provides an endpoint. */
export function AboutSection() {
  const info = versionInfo;
  const loading = versionLoadingState;
  const failed = versionLoadFailed;
  const update = updatePrefs;
  const state = releaseState;
  onMount(() => void refreshVersion());
  return (
    <section>
      <h1>About</h1>
      <p class="settings-intro">
        Build identity and daemon versions. The release check is off by default and runs only in
        this browser when you enable it.
      </p>
      <Show when={!loading()} fallback={<div class="settings-loading">Loading version info…</div>}>
        <Show
          when={!failed()}
          fallback={
            <div class="settings-loading">
              Could not load version info.{" "}
              <button type="button" onClick={() => void refreshVersion()}>
                Retry
              </button>
            </div>
          }
        >
          <div class="settings-fields">
            <SettingRow label="Blackbird" hint="binary version · commit · build date">
              <span>
                {info()?.blackbird.version ?? "—"} · {info()?.blackbird.commit ?? "—"} ·{" "}
                {info()?.blackbird.buildDate ?? "—"}
              </span>
            </SettingRow>
            <SettingRow label="Daemon" hint="live rTorrent / libtorrent versions">
              <span>
                rTorrent {info()?.rtorrent.version || "—"} / libtorrent{" "}
                {info()?.rtorrent.library || "—"}
              </span>
            </SettingRow>
            <SettingRow label="Session" hint="live connection state">
              <span>
                {info()?.connection || "—"} · {info()?.torrents ?? 0} torrents
              </span>
            </SettingRow>
            <SettingRow
              label="Check for updates"
              hint="opt-in · fetches only the endpoint below in this browser"
            >
              <span class="sort-control">
                <label>
                  <input
                    type="checkbox"
                    checked={update().enabled}
                    onChange={(event) => setUpdateCheckEnabled(event.currentTarget.checked)}
                  />{" "}
                  Enable release check
                </label>
              </span>
            </SettingRow>
            <Show when={update().enabled}>
              <SettingRow
                label="Release endpoint"
                hint="e.g. https://api.github.com/repos/OWNER/REPO/releases/latest"
              >
                <span class="sort-control">
                  <input
                    value={update().endpoint}
                    placeholder="https://…/releases/latest"
                    onInput={(event) => setUpdateCheckEndpoint(event.currentTarget.value)}
                  />
                  <button
                    type="button"
                    disabled={!update().endpoint.trim() || state().status === "checking"}
                    onClick={() => void checkForUpdates()}
                  >
                    {state().status === "checking" ? "Checking…" : "Check now"}
                  </button>
                </span>
              </SettingRow>
              <Show when={state().status === "current"}>
                <p class="settings-intro">
                  Up to date — latest is {(state() as { latest: string }).latest}.
                </p>
              </Show>
              <Show when={state().status === "update"}>
                <p class="settings-intro">
                  Update available: {(state() as { latest: string }).latest}.{" "}
                  <a href={(state() as { url: string }).url} target="_blank" rel="noreferrer">
                    View release
                  </a>
                </p>
              </Show>
              <Show when={state().status === "failed"}>
                <p class="settings-intro">
                  {(state() as { message: string }).message}{" "}
                  <button type="button" onClick={() => void checkForUpdates()}>
                    Retry
                  </button>
                </p>
              </Show>
            </Show>
          </div>
        </Show>
      </Show>
    </section>
  );
}
