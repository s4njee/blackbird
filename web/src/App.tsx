import { Show, createSignal, onMount } from "solid-js";
import { ActionToolbar, ContextMenu } from "./components/ActionControls";
import { AddTorrentModal } from "./components/AddTorrentModal";
import { DetailPanel } from "./components/DetailPanel";
import { LostConnectionBanner } from "./components/LostConnectionBanner";
import { SettingsPanel } from "./components/SettingsPanel";
import { StatsView } from "./components/StatsView";
import { Sidebar } from "./components/Sidebar";
import { StatusBar } from "./components/StatusBar";
import { TorrentTable, type ContextTarget } from "./components/TorrentTable";
import { TopBar } from "./components/TopBar";
import { hydrateColumnsFromConfig, route, toast } from "./store/ui";
import "./styles/app.css";

function SettingsView() {
  return <SettingsPanel />;
}

/** Main console: top bar over toolbar / sidebar / table+status bar stack (5.1 shell). */
function ConsoleView() {
  const [contextMenu, setContextMenu] = createSignal<ContextTarget | null>(null);
  return (
    <>
      <LostConnectionBanner />
      <ActionToolbar onOpenMenu={setContextMenu} />
      <div class="workspace">
        <Sidebar />
        <div class="main-col">
          <TorrentTable onContextMenu={setContextMenu} />
          <DetailPanel />
          <StatusBar />
        </div>
      </div>
      <ContextMenu target={contextMenu()} onClose={() => setContextMenu(null)} />
    </>
  );
}

export default function App() {
  onMount(() => {
    void fetch("/api/settings", { headers: { Accept: "application/json" } })
      .then((response) => response.ok ? response.json() as Promise<{ ui?: { columns?: unknown; visible_columns?: string[] } }> : null)
      .then((settings) => {
        if (!settings?.ui) return;
        if (settings.ui.columns) hydrateColumnsFromConfig(settings.ui.columns);
        else if (settings.ui.visible_columns?.length) hydrateColumnsFromConfig(settings.ui.visible_columns.map((key) => ({ key, visible: true })));
      })
      .catch(() => { /* session/auth errors leave the local defaults intact */ });
  });
  return (
    <div class="app-window">
      <TopBar />
      <Show when={route() === "console"} fallback={<Show when={route() === "settings"} fallback={<StatsView />}><SettingsView /></Show>}><ConsoleView /></Show>
      <Show when={toast()}><div class="toast" role="status">{toast()}</div></Show>
      <AddTorrentModal />
    </div>
  );
}
