import { Show, createEffect, createSignal, lazy, onMount, Suspense } from "solid-js";
import { ActionToolbar, ContextMenu } from "./components/ActionControls";
import { AddTorrentModal } from "./components/AddTorrentModal";
import { CreateTorrentModal } from "./components/CreateTorrentModal";
import { DetailPanel } from "./components/DetailPanel";
import { DialogHost } from "./components/Dialog";
import { LostConnectionBanner } from "./components/LostConnectionBanner";
import { MoveDataModal } from "./components/MoveDataModal";
import { Sidebar } from "./components/Sidebar";
import { StatusBar } from "./components/StatusBar";
import { TorrentTable, type ContextTarget } from "./components/TorrentTable";
import { TopBar } from "./components/TopBar";
import {
  applyResolvedTheme,
  hydrateAppearance,
  hydrateColumnsFromConfig,
  hydrateDaemonInfo,
  hydrateLabels,
  hydrateSavedFiltersFromConfig,
  hydrateSortFromConfig,
  initDensity,
  initTheme,
  route,
  setMenuOpen,
} from "./store/ui";
import { initCustomThemes, refreshCustomCss } from "./store/custom.js";
import { DocumentMeta } from "./components/DocumentMeta";
import { HelpOverlay } from "./components/HelpOverlay";
import { NoticeCentre, NoticeStack } from "./components/Notices";
import "./styles/app.css";
import { monitorAttention } from "./store/attention";

// Non-console routes split into their own chunks (PERF-7.5): the entry
// bundle ships the console first, and each route below loads on demand when
// the user navigates to it. Modules use named exports, so map to default.
const SettingsPanel = lazy(() =>
  import("./components/SettingsPanel").then((m) => ({ default: m.SettingsPanel })),
);
const RssView = lazy(() => import("./components/RssView").then((m) => ({ default: m.RssView })));
const HistoryView = lazy(() =>
  import("./components/HistoryView").then((m) => ({ default: m.HistoryView })),
);
const PreservationView = lazy(() =>
  import("./components/PreservationView").then((m) => ({ default: m.PreservationView })),
);
const AttentionView = lazy(() =>
  import("./components/AttentionView").then((m) => ({ default: m.AttentionView })),
);
const StatsView = lazy(() =>
  import("./components/StatsView").then((m) => ({ default: m.StatsView })),
);

function RouteFallback() {
  return (
    <div class="route-loading" role="status">
      Loading view…
    </div>
  );
}

function SettingsView() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <SettingsPanel />
    </Suspense>
  );
}

function LazyRssView() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <RssView />
    </Suspense>
  );
}

function LazyHistoryView() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <HistoryView />
    </Suspense>
  );
}

function LazyStatsView() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <StatsView />
    </Suspense>
  );
}

/** Main console: top bar over toolbar / sidebar / table+status bar stack (5.1 shell). */
function ConsoleView() {
  const [contextMenu, setContextMenu] = createSignal<ContextTarget | null>(null);
  // Mirror menu visibility for the global shortcut guard (POL-8.5).
  createEffect(() => {
    setMenuOpen(contextMenu() !== null);
  });
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
  monitorAttention();
  onMount(() => {
    // Built-in theme + density first (sync, pre-paint parity), then custom
    // files: a stored custom theme applies when its list arrives.
    initTheme();
    initDensity();
    void initCustomThemes().then(() => {
      applyResolvedTheme();
      void refreshCustomCss();
    });
    void fetch("/api/v1/settings", { headers: { Accept: "application/json" } })
      .then((response) =>
        response.ok
          ? (response.json() as Promise<{
              ui?: {
                columns?: unknown;
                visible_columns?: string[];
                saved_filters?: unknown;
                sort?: unknown;
                accent?: unknown;
                theme?: unknown;
                date_format?: unknown;
                rate_format?: unknown;
              };
              labels?: unknown;
              capabilities?: { rename?: boolean };
              directories?: { open_url_template?: string };
            }>)
          : null,
      )
      .then((settings) => {
        if (!settings) return;
        hydrateDaemonInfo(settings.capabilities, settings.directories);
        hydrateLabels(settings.labels);
        if (!settings.ui) return;
        if (settings.ui.columns) hydrateColumnsFromConfig(settings.ui.columns);
        else if (settings.ui.visible_columns?.length)
          hydrateColumnsFromConfig(
            settings.ui.visible_columns.map((key) => ({ key, visible: true })),
          );
        hydrateSavedFiltersFromConfig(settings.ui.saved_filters);
        hydrateSortFromConfig(settings.ui.sort);
        // Appearance applies at boot (POL-8.4), not when Settings mounts.
        hydrateAppearance(settings.ui);
      })
      .catch(() => {
        /* session/auth errors leave the local defaults intact */
      });
  });
  return (
    <div class="app-window">
      <TopBar />
      <Show
        when={route() === "console"}
        fallback={
          <Show
            when={route() === "settings"}
            fallback={
              <Show
                when={route() === "rss"}
                fallback={
                  <Show
                    when={route() === "history"}
                    fallback={
                      <Show
                        when={route() === "attention"}
                        fallback={
                          <Show when={route() === "preservation"} fallback={<LazyStatsView />}>
                            <Suspense fallback={<RouteFallback />}>
                              <PreservationView />
                            </Suspense>
                          </Show>
                        }
                      >
                        <Suspense fallback={<RouteFallback />}>
                          <AttentionView />
                        </Suspense>
                      </Show>
                    }
                  >
                    <LazyHistoryView />
                  </Show>
                }
              >
                <LazyRssView />
              </Show>
            }
          >
            <SettingsView />
          </Show>
        }
      >
        <ConsoleView />
      </Show>
      <AddTorrentModal />
      <CreateTorrentModal />
      <MoveDataModal />
      <DialogHost />
      <HelpOverlay />
      <NoticeStack />
      <NoticeCentre />
      <DocumentMeta />
    </div>
  );
}
