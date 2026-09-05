// Shared server-side directory browser (PAR-5.1): roots with free space,
// constrained navigation, new-folder creation, and shared recent picks.
// Reused by Add, Move, Settings > Directories, and automation rule editors.

import { For, Show, createSignal, onMount } from "solid-js";
import { formatBytes } from "../lib/format";
import { showToast } from "../store/ui";

export interface BrowserRoot {
  path: string;
  freeBytes: number;
  totalBytes: number;
}

export interface BrowserState {
  roots: BrowserRoot[];
  path: string;
  parent?: string;
  entries: Array<{ name: string; path: string }>;
}

const RECENT_KEY = "blackbird.dir-recent.v1";
const RECENT_LIMIT = 8;

export function dirRecents(): string[] {
  try {
    const raw = JSON.parse(localStorage.getItem(RECENT_KEY) ?? "[]") as unknown;
    return Array.isArray(raw) ? raw.filter((item): item is string => typeof item === "string") : [];
  } catch {
    return [];
  }
}

export function rememberDir(path: string) {
  try {
    localStorage.setItem(
      RECENT_KEY,
      JSON.stringify(
        [path, ...dirRecents().filter((item) => item !== path)].slice(0, RECENT_LIMIT),
      ),
    );
  } catch {
    /* private mode: recents simply don't persist */
  }
}

async function fetchBrowser(path: string): Promise<BrowserState> {
  const response = await fetch(
    `/api/v1/directories${path ? `?path=${encodeURIComponent(path)}` : ""}`,
    {
      headers: { Accept: "application/json" },
    },
  );
  const body = (await response.json().catch(() => ({}))) as BrowserState & {
    error?: { message?: string };
  };
  if (!response.ok) throw new Error(body.error?.message || "Could not browse that directory");
  return body;
}

export function DirectoryBrowser(props: {
  value: string;
  onPick: (path: string) => void;
  showRecents?: boolean;
  pickDefault?: boolean;
}) {
  const [browser, setBrowser] = createSignal<BrowserState | null>(null);
  const [error, setError] = createSignal("");
  const [newName, setNewName] = createSignal("");
  const [creating, setCreating] = createSignal(false);
  const showRecents = () => props.showRecents !== false;

  const browse = async (path = "") => {
    try {
      const data = await fetchBrowser(path);
      setBrowser(data);
      setError("");
      // Inline forms default an empty destination to the browsed folder;
      // modal pickers (pickDefault=false) leave the field untouched until an
      // explicit pick so opening never applies or closes anything.
      if (!props.value && props.pickDefault !== false) props.onPick(data.path);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not browse directories");
    }
  };

  // An explicit pick: remembered, reported, and navigated to. Plain
  // navigation (entries, roots, up) only browses so modal pickers stay open.
  const pick = (path: string) => {
    rememberDir(path);
    props.onPick(path);
    void browse(path);
  };

  const create = async () => {
    const current = browser();
    const name = newName().trim();
    if (!current || !name || creating()) return;
    setCreating(true);
    try {
      const response = await fetch("/api/v1/directories", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: current.path, name }),
      });
      const body = (await response.json().catch(() => ({}))) as {
        path?: string;
        error?: { message?: string };
      };
      if (!response.ok) throw new Error(body.error?.message || "Could not create directory");
      setNewName("");
      if (body.path) pick(body.path);
      else void browse(current.path);
    } catch (err) {
      showToast(err instanceof Error ? err.message : "Could not create directory");
    } finally {
      setCreating(false);
    }
  };

  onMount(() => void browse(props.value));

  return (
    <div class="directory-browser">
      <Show when={showRecents() && dirRecents().length}>
        <div class="dir-recents">
          <span>Recent</span>
          <For each={dirRecents()}>
            {(path) => (
              <button type="button" onClick={() => pick(path)}>
                {path}
              </button>
            )}
          </For>
        </div>
      </Show>
      <div class="dir-roots">
        <For each={browser()?.roots ?? []}>
          {(root) => (
            <button
              type="button"
              classList={{ active: browser()?.path === root.path }}
              title={`${root.path} · ${formatBytes(root.freeBytes)} free of ${formatBytes(root.totalBytes)}`}
              onClick={() => void browse(root.path)}
            >
              {root.path} <small class="tnum">{formatBytes(root.freeBytes)} free</small>
            </button>
          )}
        </For>
      </div>
      <div class="dir-nav">
        <button
          type="button"
          disabled={!browser()?.parent}
          onClick={() => void browse(browser()!.parent!)}
        >
          ↑
        </button>
        <b>{browser()?.path || "Loading…"}</b>
        <button
          type="button"
          disabled={!browser()}
          onClick={() => browser() && pick(browser()!.path)}
        >
          Use this folder
        </button>
      </div>
      <Show when={error()}>
        <p class="input-error">{error()}</p>
      </Show>
      <div class="dir-entries">
        <For each={browser()?.entries ?? []}>
          {(entry) => (
            <button
              type="button"
              classList={{ active: props.value === entry.path }}
              onClick={() => void browse(entry.path)}
            >
              📁 {entry.name}
            </button>
          )}
        </For>
      </div>
      <div class="dir-create">
        <input
          value={newName()}
          placeholder="New folder…"
          aria-label="New folder name"
          onInput={(event) => setNewName(event.currentTarget.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") void create();
          }}
        />
        <button
          type="button"
          disabled={!newName().trim() || creating()}
          onClick={() => void create()}
        >
          Create
        </button>
      </div>
    </div>
  );
}

/** Browse button opening the directory browser in a modal. */
export function DirPicker(props: {
  value: string;
  onPick: (path: string) => void;
  label?: string;
}) {
  const [open, setOpen] = createSignal(false);
  return (
    <>
      <button type="button" class="settings-add-row" onClick={() => setOpen(true)}>
        {props.label ?? "Browse…"}
      </button>
      <Show when={open()}>
        <div
          class="modal-backdrop"
          role="presentation"
          onMouseDown={(event) => {
            if (event.currentTarget === event.target) setOpen(false);
          }}
        >
          <section
            class="dirpicker-modal"
            role="dialog"
            aria-modal="true"
            aria-label="Choose directory"
          >
            <header class="modal-title">
              <h2>Choose directory</h2>
              <button
                type="button"
                aria-label="Close directory browser"
                onClick={() => setOpen(false)}
              >
                ✕
              </button>
            </header>
            <DirectoryBrowser
              value={props.value}
              pickDefault={false}
              onPick={(path) => {
                props.onPick(path);
                setOpen(false);
              }}
            />
          </section>
        </div>
      </Show>
    </>
  );
}
