import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount, type JSX } from "solid-js";
import { COLUMN_DEFINITIONS, type ColumnConfig } from "../lib/columns";
import { connected, globalStats, torrentList } from "../store/session";
import { columnLayoutConfig, setSettingsDirty, showToast } from "../store/ui";

type Label = { name: string; color: string };
type Draft = {
  tuning: Record<string, unknown>;
  directories: { default: string; per_label: Record<string, string>; watch?: string; watch_label?: string; session?: string };
  labels: Label[];
  ui: { accent: string; columns: ColumnConfig[]; visible_columns: any; sort: { column: string; dir: "asc" | "desc" }; date_format: string; rate_format: string; poll_interval: string };
};
type Loaded = Draft & { daemon: Record<string, string> };
type Nav = "General" | "Connection" | "Bandwidth" | "Queue" | "Directories" | "Labels" | "Interface" | "Advanced";

const NAV: Nav[] = ["General", "Connection", "Bandwidth", "Queue", "Directories", "Labels", "Interface", "Advanced"];
const COLUMNS = COLUMN_DEFINITIONS.map((column) => column.key);
const NUMBER_TUNING = new Set(["dht_port", "http_max_open", "max_open_sockets", "max_open_files", "min_peers_normal", "max_peers_normal", "min_peers_seeded", "max_peers_seeded", "max_uploads", "global_down_rate_kb", "global_up_rate_kb", "max_downloads_global", "max_uploads_global"]);

const EMPTY: Loaded = { tuning: {}, directories: { default: "", per_label: {} }, labels: [], ui: { accent: "#35418f", columns: [], visible_columns: [], sort: { column: "added", dir: "desc" }, date_format: "local", rate_format: "binary", poll_interval: "2s" }, daemon: {} };
const clone = <T,>(value: T): T => JSON.parse(JSON.stringify(value)) as T;
const isInterface = (value: string) => value === "Interface";

function normalize(input: Partial<Loaded>): Loaded {
  const base = clone(EMPTY);
  const legacyColumns = input.ui?.visible_columns ?? [];
  const columns = input.ui?.columns?.length ? input.ui.columns : legacyColumns.map((key: string) => ({ key: key as ColumnConfig["key"], visible: true, width: 0 }));
  return {
    ...base,
    ...input,
    tuning: input.tuning ?? {}, daemon: input.daemon ?? {}, labels: input.labels ?? [],
    directories: { ...base.directories, ...(input.directories ?? {}), per_label: input.directories?.per_label ?? {} },
    ui: { ...base.ui, ...(input.ui ?? {}), columns, visible_columns: [], sort: { ...base.ui.sort, ...(input.ui?.sort ?? {}) } },
  };
}

function valueFor(draft: Draft, daemon: Record<string, string>, field: string, daemonKey: string) {
  const declared = draft.tuning[field];
  return declared === null || declared === undefined ? (daemon[daemonKey] ?? "") : declared;
}

function textValue(value: unknown) { return value === null || value === undefined ? "" : String(value); }

export function SettingsPanel() {
  const [active, setActive] = createSignal<Nav>("Connection");
  const [initial, setInitial] = createSignal<Loaded>(clone(EMPTY));
  const [draft, setDraft] = createSignal<Draft>(clone(EMPTY));
  const [loading, setLoading] = createSignal(true);
  const [saving, setSaving] = createSignal(false);
  const [errors, setErrors] = createSignal<Record<string, string>>({});
  const [results, setResults] = createSignal<Array<{ key: string; error?: string }>>([]);
  const [reassign, setReassign] = createSignal<Record<string, string>>({});
  const [rawMethod, setRawMethod] = createSignal("");
  const [rawParams, setRawParams] = createSignal("");
  const dirty = createMemo(() => JSON.stringify(draft()) !== JSON.stringify(stripDaemon(initial())) || Object.keys(reassign()).length > 0);
  createEffect(() => setSettingsDirty(dirty()));
  createEffect(() => {
    const accent = draft().ui.accent;
    if (/^#[0-9a-f]{6}$/i.test(accent)) document.documentElement.style.setProperty("--accent", accent);
  });
  onMount(() => {
    void load();
    const beforeUnload = (event: BeforeUnloadEvent) => { if (dirty()) { event.preventDefault(); event.returnValue = ""; } };
    window.addEventListener("beforeunload", beforeUnload);
    onCleanup(() => { window.removeEventListener("beforeunload", beforeUnload); setSettingsDirty(false); });
  });
  function stripDaemon(value: Loaded): Draft { const { daemon: _daemon, ...settings } = value; return settings; }
  async function load() {
    setLoading(true);
    try {
      const response = await fetch("/api/settings", { headers: { Accept: "application/json" } });
      if (!response.ok) throw new Error("Could not load settings");
      const loaded = normalize(await response.json() as Partial<Loaded>);
      setInitial(loaded); setDraft(clone(stripDaemon(loaded))); setErrors({}); setResults([]); setReassign({});
    } catch (error) { showToast(error instanceof Error ? error.message : "Could not load settings"); }
    finally { setLoading(false); }
  }
  function updateTuning(key: string, value: unknown) { setDraft((current) => ({ ...current, tuning: { ...current.tuning, [key]: value } })); }
  function updateDirectory(key: string, value: string) { setDraft((current) => ({ ...current, directories: { ...current.directories, [key]: value } })); }
  function validate() {
    const next: Record<string, string> = {}; const t = draft().tuning;
    const ports = textValue(t.port_range || valueFor(draft(), initial().daemon, "port_range", "network.port_range"));
    if (ports && (!/^\d{1,5}(-\d{1,5})?$/.test(ports) || ports.split("-").some((part) => Number(part) < 1 || Number(part) > 65535))) next.port_range = "Enter a port or range from 1–65535.";
    const dhtPort = Number(valueFor(draft(), initial().daemon, "dht_port", "dht.port"));
    if (!Number.isInteger(dhtPort) || dhtPort < 1 || dhtPort > 65535) next.dht_port = "Port must be 1–65535.";
    for (const field of NUMBER_TUNING) { const raw = valueFor(draft(), initial().daemon, field, daemonKey(field)); if (raw !== "" && (!Number.isFinite(Number(raw)) || Number(raw) < 0)) next[field] = "Must be a non-negative number."; }
    if (!/^#[0-9a-f]{6}$/i.test(draft().ui.accent)) next.accent = "Use a #rrggbb color.";
    const names = new Set<string>(); draft().labels.forEach((item, index) => { if (!item.name.trim()) next[`label-${index}`] = "Name is required."; else if (names.has(item.name)) next[`label-${index}`] = "Names must be unique."; names.add(item.name); if (!/^#[0-9a-f]{6}$/i.test(item.color)) next[`color-${index}`] = "Use #rrggbb."; });
    setErrors(next); return Object.keys(next).length === 0;
  }
  async function save() {
    if (!validate() || saving()) return;
    setSaving(true); setResults([]);
    try {
      for (const [oldLabel, replacement] of Object.entries(reassign())) {
        const hashes = torrentList().filter((torrent) => torrent.label === oldLabel).map((torrent) => torrent.hash);
        if (!hashes.length) continue;
        const response = await fetch("/api/torrents/action", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ action: "set_label", hashes, label: replacement }) });
        if (!response.ok) throw new Error(`Could not reassign torrents from ${oldLabel}`);
      }
      const response = await fetch("/api/settings", { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify(draft()) });
      const body = await response.json().catch(() => ({})) as { message?: string; results?: Array<{ key?: string; Key?: string; error?: string; err?: unknown; Err?: unknown }>; saved?: boolean; error?: string };
      if (!response.ok) throw new Error(body.message || "Could not save settings");
      const runtime = (body.results ?? []).map((item) => ({ key: item.key ?? item.Key ?? "setting", error: item.error || String(item.err ?? item.Err ?? "") || undefined }));
      setResults(runtime); if (!body.saved) throw new Error(body.error || "Settings were not persisted");
      setInitial({ ...draft(), daemon: initial().daemon }); setReassign({}); showToast(runtime.some((item) => item.error) ? "Settings saved; some runtime updates failed." : "Settings saved.");
    } catch (error) { showToast(error instanceof Error ? error.message : "Could not save settings"); }
    finally { setSaving(false); }
  }
  function revert() { setDraft(clone(stripDaemon(initial()))); setErrors({}); setResults([]); setReassign({}); }
  function deleteLabel(name: string) {
    if (!window.confirm(`Delete the ${name} label? Affected torrents can be cleared or reassigned on Save.`)) return;
    const replacement = window.prompt("Reassign affected torrents to this label (leave blank to clear):", ""); if (replacement === null) return;
    setDraft((current) => ({
      ...current,
      labels: current.labels.filter((item) => item.name !== name),
      directories: {
        ...current.directories,
        per_label: Object.fromEntries(Object.entries(current.directories.per_label).filter(([key]) => key !== name)),
      },
    }));
    setReassign((current) => ({ ...current, [name]: replacement.trim() }));
  }
  async function executeRaw() {
    if (!rawMethod() || !window.confirm(`Execute XML-RPC method ${rawMethod()}? This is an operator escape hatch.`)) return;
    try { const response = await fetch("/api/settings/execute", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ method: rawMethod(), params: rawParams().split("\n").filter(Boolean) }) }); const body = await response.json().catch(() => ({})) as { message?: string }; if (!response.ok) throw new Error(body.message || "Method failed"); showToast("XML-RPC method completed."); } catch (error) { showToast(error instanceof Error ? error.message : "Method failed"); }
  }
  return <div class="settings-panel">
    <nav class="settings-nav" aria-label="Settings sections"><For each={NAV}>{(section) => <button classList={{ active: active() === section }} type="button" onClick={() => setActive(section)}>{section}{section === "Advanced" && <small>.rtorrent.rc</small>}</button>}</For></nav>
    <main class="settings-content"><Show when={!loading()} fallback={<div class="settings-loading">Loading settings…</div>}>
      <SettingsSection active={active()} draft={draft()} daemon={initial().daemon} errors={errors()} updateTuning={updateTuning} updateDirectory={updateDirectory} setDraft={setDraft} deleteLabel={deleteLabel} rawMethod={rawMethod()} setRawMethod={setRawMethod} rawParams={rawParams()} setRawParams={setRawParams} executeRaw={executeRaw} />
      <Show when={results().length}><div class="settings-results"><For each={results()}>{(result) => <div classList={{ failure: Boolean(result.error) }}><span>{result.key}</span><b>{result.error || "Applied"}</b></div>}</For></div></Show>
      <footer class="settings-footer"><button type="button" disabled={!dirty() || saving()} onClick={revert}>Revert</button><button class="settings-save" type="button" disabled={!dirty() || saving()} onClick={() => void save()}>{saving() ? "Saving…" : "Save"}</button></footer>
    </Show></main>
  </div>;
}

function daemonKey(field: string) { return ({ port_range: "network.port_range", port_random: "network.port_random", encryption: "protocol.encryption", dht_mode: "dht.mode", dht_port: "dht.port", use_udp: "trackers.use_udp", pex: "protocol.pex", local_address: "network.local_address", bind_address: "network.bind_address", http_max_open: "network.http.max_open", max_open_sockets: "network.max_open_sockets", max_open_files: "network.max_open_files", min_peers_normal: "throttle.min_peers.normal", max_peers_normal: "throttle.max_peers.normal", min_peers_seeded: "throttle.min_peers.seeded", max_peers_seeded: "throttle.max_peers.seeded", max_uploads: "throttle.max_uploads", global_down_rate_kb: "throttle.global_down.max_rate", global_up_rate_kb: "throttle.global_up.max_rate", max_downloads_global: "throttle.max_downloads.global", max_uploads_global: "throttle.max_uploads.global" } as Record<string, string>)[field] ?? field; }

function SettingsSection(props: { active: string; draft: Draft; daemon: Record<string, string>; errors: Record<string, string>; updateTuning: (key: string, value: unknown) => void; updateDirectory: (key: string, value: string) => void; setDraft: (fn: (value: Draft) => Draft) => void; deleteLabel: (name: string) => void; rawMethod: string; setRawMethod: (value: string) => void; rawParams: string; setRawParams: (value: string) => void; executeRaw: () => void }) {
  const value = (field: string) => valueFor(props.draft, props.daemon, field, daemonKey(field));
  const input = (field: string, label: string, hint: string, type: "number" | "text" = "number") => <SettingRow label={label} hint={hint} error={props.errors[field]}><input type={type} value={textValue(value(field))} onInput={(event) => props.updateTuning(field, type === "number" ? Number(event.currentTarget.value) : event.currentTarget.value)} /></SettingRow>;
  const check = (field: string, label: string, hint: string) => <SettingRow label={label} hint={hint}><input class="settings-check" type="checkbox" checked={Boolean(value(field)) && textValue(value(field)) !== "0"} onChange={(event) => props.updateTuning(field, event.currentTarget.checked)} /></SettingRow>;
  const body = createMemo(() => {
  if (isInterface(props.active)) return <InterfaceSection {...props} />;
  if (props.active === "General") return <section><h1>General</h1><p class="settings-intro">Runtime values are read directly from rTorrent. Saving writes YAML atomically and reports any daemon-side failures below.</p><div class="settings-general"><span>Configuration is single-user and YAML-backed.</span><span>Connection, bandwidth, queue, and directory controls are under their matching sections.</span></div></section>;
  if (props.active === "Connection") return <section><h1>Connection &amp; network</h1><p class="port-indicator" classList={{ disconnected: !connected() }}>● Port {globalStats()?.port ?? "—"} {connected() ? "open" : "connection unavailable"}</p><div class="settings-fields">{input("port_range", "Listening port", "network.port_range · TCP + uTP", "text")}{check("port_random", "Randomize port", "network.port_random") }<SettingRow label="Encryption" hint="protocol.encryption.set"><select value={textValue(value("encryption"))} onChange={(event) => props.updateTuning("encryption", event.currentTarget.value)}><option value="none">None</option><option value="allow_incoming,try_outgoing">Allow</option><option value="require">Require</option><option value="require,require_RC4">Require RC4</option></select></SettingRow><SettingRow label="DHT mode" hint="dht.mode.set"><select value={textValue(value("dht_mode"))} onChange={(event) => props.updateTuning("dht_mode", event.currentTarget.value)}><For each={["auto", "on", "off", "disable"]}>{(item) => <option value={item}>{item}</option>}</For></select></SettingRow>{input("dht_port", "DHT port", "dht.port · 1–65535")}{check("use_udp", "UDP trackers", "trackers.use_udp.set")}{check("pex", "Peer exchange", "protocol.pex.set")}{input("local_address", "Report IP address", "network.local_address", "text")}{input("bind_address", "Bind address", "network.bind_address", "text")}{input("http_max_open", "Max open HTTP", "network.http.max_open")}{input("max_open_sockets", "Global socket cap", "network.max_open_sockets")}{input("max_open_files", "Max open files", "network.max_open_files")}{input("min_peers_normal", "Min peers (normal)", "throttle.min_peers.normal")}{input("max_peers_normal", "Max peers (normal)", "throttle.max_peers.normal")}{input("min_peers_seeded", "Min peers (seeded)", "throttle.min_peers.seeded")}{input("max_peers_seeded", "Max peers (seeded)", "throttle.max_peers.seeded")}{input("max_uploads", "Max uploads per torrent", "throttle.max_uploads")}</div></section>;
  if (props.active === "Bandwidth") return <section><h1>Bandwidth</h1><div class="settings-fields">{input("global_down_rate_kb", "Global download limit", "throttle.global_down.max_rate · KB/s · 0 = unlimited")}{input("global_up_rate_kb", "Global upload limit", "throttle.global_up.max_rate · KB/s · 0 = unlimited")}</div><p class="settings-intro">Named alternative throttle groups are deliberately deferred; this console currently exposes only the daemon’s global limits.</p></section>;
  if (props.active === "Queue") return <section><h1>Queue</h1><div class="settings-fields">{input("max_downloads_global", "Max active downloads", "throttle.max_downloads.global")}{input("max_uploads_global", "Max active uploads", "throttle.max_uploads.global")}</div><p class="settings-intro">Stop-on-ratio schedules remain YAML-managed because their exact rTorrent behavior depends on the daemon’s group-seeding configuration.</p></section>;
  if (props.active === "Directories") return <section><h1>Directories</h1><div class="settings-fields"><SettingRow label="Default download directory" hint="directory.default.set"><input value={props.draft.directories.default} onInput={(event) => props.updateDirectory("default", event.currentTarget.value)} /></SettingRow><SettingRow label="Session directory" hint="Read-only daemon startup setting"><input value={props.draft.directories.session ?? "—"} disabled /></SettingRow><SettingRow label="Watch directory" hint="YAML: directories.watch"><input value={props.draft.directories.watch ?? ""} onInput={(event) => props.updateDirectory("watch", event.currentTarget.value)} /></SettingRow><SettingRow label="Watch label" hint="YAML: directories.watch_label"><input value={props.draft.directories.watch_label ?? ""} onInput={(event) => props.updateDirectory("watch_label", event.currentTarget.value)} /></SettingRow></div><h2>Per-label destinations</h2><For each={props.draft.labels}>{(label) => <div class="mapping-row"><span>{label.name}</span><input value={props.draft.directories.per_label[label.name] ?? ""} placeholder="Use default directory" onInput={(event) => props.setDraft((current) => ({ ...current, directories: { ...current.directories, per_label: { ...current.directories.per_label, [label.name]: event.currentTarget.value } } }))} /></div>}</For></section>;
  if (props.active === "Labels") return <section><h1>Labels</h1><div class="label-editor"><For each={props.draft.labels}>{(label, index) => <div class="label-edit-row"><input type="color" value={label.color} onInput={(event) => props.setDraft((current) => ({ ...current, labels: current.labels.map((item, i) => i === index() ? { ...item, color: event.currentTarget.value } : item) }))} /><input value={label.name} onInput={(event) => props.setDraft((current) => ({ ...current, labels: current.labels.map((item, i) => i === index() ? { ...item, name: event.currentTarget.value } : item) }))} /><button type="button" onClick={() => props.deleteLabel(label.name)}>Delete</button><Show when={props.errors[`label-${index()}`] || props.errors[`color-${index()}`]}><small>{props.errors[`label-${index()}`] || props.errors[`color-${index()}`]}</small></Show></div>}</For><button class="settings-add-row" type="button" onClick={() => props.setDraft((current) => ({ ...current, labels: [...current.labels, { name: "new label", color: "#f59e0b" }] }))}>+ Add label</button></div></section>;
  if (props.active === "Interface") return <section><h1>Interface</h1><div class="settings-fields"><SettingRow label="Accent color" hint="ui.accent" error={props.errors.accent}><div class="accent-control"><input type="color" value={props.draft.ui.accent} onInput={(event) => props.setDraft((current) => ({ ...current, ui: { ...current.ui, accent: event.currentTarget.value } }))} /><input value={props.draft.ui.accent} onInput={(event) => props.setDraft((current) => ({ ...current, ui: { ...current.ui, accent: event.currentTarget.value } }))} /></div></SettingRow><SettingRow label="Default sort" hint="ui.sort"><div class="sort-control"><select value={props.draft.ui.sort.column} onChange={(event) => props.setDraft((current) => ({ ...current, ui: { ...current.ui, sort: { ...current.ui.sort, column: event.currentTarget.value } } }))}><For each={COLUMNS}>{(item) => <option value={item}>{item}</option>}</For></select><select value={props.draft.ui.sort.dir} onChange={(event) => props.setDraft((current) => ({ ...current, ui: { ...current.ui, sort: { ...current.ui.sort, dir: event.currentTarget.value as "asc" | "desc" } } }))}><option value="asc">Ascending</option><option value="desc">Descending</option></select></div></SettingRow><SettingRow label="Date format" hint="ui.date_format"><select value={props.draft.ui.date_format} onChange={(event) => props.setDraft((current) => ({ ...current, ui: { ...current.ui, date_format: event.currentTarget.value } }))}><option value="local">Local date</option><option value="iso">ISO date</option></select></SettingRow><SettingRow label="Rate format" hint="ui.rate_format"><select value={props.draft.ui.rate_format} onChange={(event) => props.setDraft((current) => ({ ...current, ui: { ...current.ui, date_format: event.currentTarget.value } }))}><option value="binary">Binary (MiB)</option><option value="decimal">Decimal (MB)</option></select></SettingRow><SettingRow label="Poll interval preference" hint="ui.poll_interval"><input value={props.draft.ui.poll_interval} onInput={(event) => props.setDraft((current) => ({ ...current, ui: { ...current.ui, poll_interval: event.currentTarget.value } }))} /></SettingRow></div><h2>Operator default columns</h2><p class="settings-intro">This layout is used when a browser has no saved layout. Runtime changes are stored per browser from the torrent header.</p><button type="button" class="settings-add-row" onClick={() => props.setDraft((current) => ({ ...current, ui: { ...current.ui, columns: columnLayoutConfig(), visible_columns: undefined } }))}>Use current browser layout</button><div class="column-checks"><For each={COLUMN_DEFINITIONS}>{(column) => <label><input type="checkbox" checked={Boolean(props.draft.ui.columns.find((item) => item.key === column.key)?.visible)} onChange={(event) => props.setDraft((current) => { const existing = current.ui.columns.length ? current.ui.columns : COLUMN_DEFINITIONS.map((item) => ({ key: item.key, visible: true, width: item.width })); const next = existing.some((item) => item.key === column.key) ? existing.map((item) => item.key === column.key ? { ...item, visible: event.currentTarget.checked } : item) : [...existing, { key: column.key, visible: event.currentTarget.checked, width: column.width }]; return { ...current, ui: { ...current.ui, columns: next, visible_columns: undefined } }; })} />{column.label}</label>}</For></div></section>;
  return <section><h1>Advanced</h1><p class="settings-intro">Live values below are read-only. The escape hatch executes an XML-RPC method after confirmation and logs its method name.</p><div class="advanced-values"><For each={Object.keys(props.daemon).sort()}>{(key) => <div><span>{key}</span><b>{props.daemon[key]}</b></div>}</For></div><h2>Execute XML-RPC method</h2><div class="raw-rpc"><input value={props.rawMethod} placeholder="method.name" onInput={(event) => props.setRawMethod(event.currentTarget.value)} /><textarea value={props.rawParams} placeholder="One string parameter per line" onInput={(event) => props.setRawParams(event.currentTarget.value)} /><button type="button" onClick={() => void props.executeRaw()}>Execute after confirmation</button></div></section>;
  });
  return <>{body()}</>;
}

function InterfaceSection(props: { draft: Draft; errors: Record<string, string>; setDraft: (fn: (value: Draft) => Draft) => void }) {
  const setUI = (patch: Partial<Draft["ui"]>) => props.setDraft((current) => ({ ...current, ui: { ...current.ui, ...patch } }));
  const allColumns = () => props.draft.ui.columns.length ? props.draft.ui.columns : COLUMN_DEFINITIONS.map((column) => ({ key: column.key, visible: true, width: column.width }));
  return <section>
    <h1>Interface</h1>
    <div class="settings-fields">
      <SettingRow label="Accent color" hint="ui.accent" error={props.errors.accent}><div class="accent-control"><input type="color" value={props.draft.ui.accent} onInput={(event) => setUI({ accent: event.currentTarget.value })} /><input value={props.draft.ui.accent} onInput={(event) => setUI({ accent: event.currentTarget.value })} /></div></SettingRow>
      <SettingRow label="Default sort" hint="ui.sort"><div class="sort-control"><select value={props.draft.ui.sort.column} onChange={(event) => setUI({ sort: { ...props.draft.ui.sort, column: event.currentTarget.value } })}><For each={COLUMNS}>{(item) => <option value={item}>{item}</option>}</For></select><select value={props.draft.ui.sort.dir} onChange={(event) => setUI({ sort: { ...props.draft.ui.sort, dir: event.currentTarget.value as "asc" | "desc" } })}><option value="asc">Ascending</option><option value="desc">Descending</option></select></div></SettingRow>
      <SettingRow label="Date format" hint="ui.date_format"><select value={props.draft.ui.date_format} onChange={(event) => setUI({ date_format: event.currentTarget.value })}><option value="local">Local date</option><option value="iso">ISO date</option></select></SettingRow>
      <SettingRow label="Rate format" hint="ui.rate_format"><select value={props.draft.ui.rate_format} onChange={(event) => setUI({ rate_format: event.currentTarget.value })}><option value="binary">Binary (MiB)</option><option value="decimal">Decimal (MB)</option></select></SettingRow>
      <SettingRow label="Poll interval preference" hint="ui.poll_interval"><input value={props.draft.ui.poll_interval} onInput={(event) => setUI({ poll_interval: event.currentTarget.value })} /></SettingRow>
    </div>
    <h2>Operator default columns</h2>
    <p class="settings-intro">This layout is used when a browser has no saved layout. Runtime changes are stored per browser from the torrent header.</p>
    <button type="button" class="settings-add-row" onClick={() => setUI({ columns: columnLayoutConfig(), visible_columns: [] })}>Use current browser layout</button>
    <div class="column-checks"><For each={COLUMN_DEFINITIONS}>{(column) => <label><input type="checkbox" checked={Boolean(allColumns().find((item) => item.key === column.key)?.visible)} onChange={(event) => { const next = allColumns().map((item) => item.key === column.key ? { ...item, visible: event.currentTarget.checked } : item); setUI({ columns: next, visible_columns: [] }); }} />{column.label}</label>}</For></div>
  </section>;
}

function SettingRow(props: { label: string; hint: string; error?: string; children: JSX.Element }) { return <div class="setting-row"><div><label>{props.label}</label><small>{props.hint}</small></div><div class="setting-control">{props.children}<Show when={props.error}><p>{props.error}</p></Show></div></div>; }
