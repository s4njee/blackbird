// Directories section + watch editor (POL-8.8).
import { For, Show } from "solid-js";
import type { Draft, SectionProps } from "./types";
import { DirPicker } from "../DirectoryBrowser";
import { SettingRow } from "./SettingRow";
import type { WatchDirDraft } from "./types";

export function DirectoriesSection(props: SectionProps) {
  return (
    <section>
      <h1>Directories</h1>
      <div class="settings-fields">
        <SettingRow label="Default download directory" hint="directory.default.set">
          <div class="sort-control">
            <input
              value={props.draft.directories.default}
              onInput={(event) => props.updateDirectory("default", event.currentTarget.value)}
            />
            <DirPicker
              value={props.draft.directories.default}
              onPick={(path) => props.updateDirectory("default", path)}
            />
          </div>
        </SettingRow>
        <SettingRow
          label="Session directory"
          hint="Where rTorrent keeps .torrent files; read to backfill comment/created-by"
        >
          <input
            value={props.draft.directories.session ?? ""}
            placeholder="/data/session"
            onInput={(event) => props.updateDirectory("session", event.currentTarget.value)}
          />
        </SettingRow>
        <SettingRow
          label="Open-directory URL template"
          hint="directories.open_url_template · {path} is replaced; empty copies the path"
        >
          <input
            value={props.draft.directories.open_url_template ?? ""}
            placeholder="https://files.example.com/browse?path={path}"
            onInput={(event) =>
              props.updateDirectory("open_url_template", event.currentTarget.value)
            }
          />
        </SettingRow>
      </div>
      <WatchDirectoriesSection
        draft={props.draft}
        errors={props.errors}
        updateWatch={props.updateWatch}
        addWatch={props.addWatch}
        removeWatch={props.removeWatch}
      />
      <h2>Per-label destinations</h2>
      <For each={props.draft.labels}>
        {(label) => (
          <div class="mapping-row">
            <span>{label.name}</span>
            <input
              value={props.draft.directories.per_label[label.name] ?? ""}
              placeholder="Use default directory"
              onInput={(event) =>
                props.setDraft((current) => ({
                  ...current,
                  directories: {
                    ...current.directories,
                    per_label: {
                      ...current.directories.per_label,
                      [label.name]: event.currentTarget.value,
                    },
                  },
                }))
              }
            />
            <DirPicker
              value={props.draft.directories.per_label[label.name] ?? ""}
              onPick={(path) =>
                props.setDraft((current) => ({
                  ...current,
                  directories: {
                    ...current.directories,
                    per_label: { ...current.directories.per_label, [label.name]: path },
                  },
                }))
              }
            />
          </div>
        )}
      </For>
    </section>
  );
}

/** Watch-directory list editor (PAR-3.1): each entry loads .torrent files
 * dropped into its path with the entry's label/destination/start options. */
function WatchDirectoriesSection(props: {
  draft: Draft;
  errors: Record<string, string>;
  updateWatch: (index: number, patch: Partial<WatchDirDraft>) => void;
  addWatch: () => void;
  removeWatch: (index: number) => void;
}) {
  return (
    <div>
      <h2>Watch directories</h2>
      <p class="settings-intro">
        .torrent files dropped into these paths load automatically with the entry's label,
        destination, and start options. Loaded files are renamed <code>.loaded</code> or deleted;
        malformed files move to a <code>failed/</code> subdirectory; torrents already in the session
        are skipped.
      </p>
      <Show
        when={props.draft.directories.watch.length}
        fallback={<p class="settings-intro">No watch directories configured.</p>}
      >
        <div class="watch-entries">
          <For each={props.draft.directories.watch}>
            {(entry, index) => (
              <div class="watch-entry">
                <div class="watch-entry-fields">
                  <SettingRow
                    label="Path"
                    hint="directories.watch[].path · absolute"
                    error={props.errors[`watch-${index()}`]}
                  >
                    <input
                      value={entry.path}
                      placeholder="/watch"
                      onInput={(event) =>
                        props.updateWatch(index(), { path: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Label" hint="directories.watch[].label · d.custom1.set">
                    <input
                      value={entry.label}
                      placeholder="No label"
                      onInput={(event) =>
                        props.updateWatch(index(), { label: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow
                    label="Destination"
                    hint="directories.watch[].destination · d.directory.set"
                  >
                    <div class="sort-control">
                      <input
                        value={entry.destination}
                        placeholder="Use default directory"
                        onInput={(event) =>
                          props.updateWatch(index(), { destination: event.currentTarget.value })
                        }
                      />
                      <DirPicker
                        value={entry.destination}
                        onPick={(path) => props.updateWatch(index(), { destination: path })}
                      />
                    </div>
                  </SettingRow>
                  <SettingRow
                    label="Poll interval"
                    hint="directories.watch[].poll_interval · empty uses the 5s default"
                    error={props.errors[`watch-${index()}`]}
                  >
                    <input
                      value={entry.poll_interval}
                      placeholder="5s"
                      onInput={(event) =>
                        props.updateWatch(index(), { poll_interval: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                </div>
                <div class="watch-entry-flags">
                  <label>
                    <input
                      class="settings-check"
                      type="checkbox"
                      checked={entry.start}
                      onChange={(event) =>
                        props.updateWatch(index(), { start: event.currentTarget.checked })
                      }
                    />{" "}
                    Start loaded torrents
                  </label>
                  <label>
                    <input
                      class="settings-check"
                      type="checkbox"
                      checked={entry.delete_after_load}
                      onChange={(event) =>
                        props.updateWatch(index(), {
                          delete_after_load: event.currentTarget.checked,
                        })
                      }
                    />{" "}
                    Delete file after load
                  </label>
                  <button type="button" onClick={() => props.removeWatch(index())}>
                    Remove
                  </button>
                </div>
              </div>
            )}
          </For>
        </div>
      </Show>
      <button class="settings-add-row" type="button" onClick={() => props.addWatch()}>
        + Add watch directory
      </button>
    </div>
  );
}
