import { For, Show, createEffect, onCleanup } from "solid-js";
import {
  centreOpen,
  clearNotices,
  dismissNotice,
  noticeRecords,
  overflowCount,
  runNoticeAction,
  setCentreOpen,
  unreadCount,
  visibleNotices,
  type Notice,
} from "../store/notifications";

import { navigate } from "../store/ui";

function NoticeCard(props: { notice: Notice }) {
  const notice = () => props.notice;
  return (
    <div
      class={`notice notice-${notice().kind}`}
      role={notice().kind === "error" ? "alert" : "status"}
      aria-live={notice().kind === "error" ? "assertive" : "polite"}
    >
      <span class="notice-message">
        {notice().message}
        <Show when={notice().count > 1}>
          <b class="notice-count">×{notice().count}</b>
        </Show>
      </span>
      <Show when={notice().action}>
        {(action) => (
          <button type="button" class="notice-action" onClick={() => runNoticeAction(notice().id)}>
            {action().label}
          </button>
        )}
      </Show>
      <button
        type="button"
        class="notice-dismiss"
        aria-label="Dismiss notification"
        onClick={() => dismissNotice(notice().id)}
      >
        ×
      </button>
    </div>
  );
}

/** Queued toasts: at most MAX_VISIBLE on screen plus an overflow opener. */
export function NoticeStack() {
  return (
    <div class="notice-stack" role="log" aria-label="Notifications">
      <For each={visibleNotices()}>{(notice) => <NoticeCard notice={notice} />}</For>
      <Show when={overflowCount() > 0}>
        <button type="button" class="notice-overflow" onClick={() => setCentreOpen(true)}>
          +{overflowCount()} more
        </button>
      </Show>
    </div>
  );
}

/** Bell with the unread badge for the top bar. */
export function NoticeBell() {
  return (
    <button
      class="btn-icon"
      type="button"
      title="Notifications"
      aria-label={unreadCount() > 0 ? `Notifications, ${unreadCount()} unread` : "Notifications"}
      onClick={() => setCentreOpen(!centreOpen())}
    >
      🔔
      <Show when={unreadCount() > 0}>
        <span class="notice-badge">{unreadCount()}</span>
      </Show>
    </button>
  );
}

function formatTime(at: number): string {
  const date = new Date(at);
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

/** Notification centre: the last 50 records with timestamps. Focus-trapped
 * like a dialog while open, with focus restored to the bell on close. */
export function NoticeCentre() {
  let opener: Element | null = null;
  createEffect(() => {
    if (centreOpen()) {
      opener = document.activeElement;
      const id = window.setTimeout(() => {
        document.querySelector<HTMLButtonElement>(".notice-close")?.focus();
      }, 0);
      onCleanup(() => window.clearTimeout(id));
    } else if (opener instanceof HTMLElement) {
      opener.focus();
      opener = null;
    }
  });
  const trapTab = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      setCentreOpen(false);
      return;
    }
    if (event.key !== "Tab") return;
    const root = (event.currentTarget as HTMLElement).closest(".notice-centre-backdrop");
    const items = root
      ? Array.from(root.querySelectorAll<HTMLElement>(".notice-centre button")).filter(
          (el) => !el.hasAttribute("disabled"),
        )
      : [];
    if (!items.length) return;
    const first = items[0];
    const last = items[items.length - 1];
    if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    } else if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    }
  };
  return (
    <Show when={centreOpen()}>
      <div
        class="notice-centre-backdrop"
        onMouseDown={(event) => {
          if (event.target === event.currentTarget) setCentreOpen(false);
        }}
      >
        <section
          class="notice-centre"
          role="dialog"
          aria-modal="true"
          aria-label="Notification centre"
          onKeyDown={trapTab}
        >
          <header class="notice-centre-head">
            <h2>Notifications</h2>
            <button
              type="button"
              class="notice-action"
              onClick={() => {
                setCentreOpen(false);
                navigate("attention");
              }}
            >
              Attention inbox
            </button>
            <button type="button" class="notice-clear" onClick={clearNotices}>
              Clear
            </button>
            <button
              type="button"
              class="notice-close"
              aria-label="Close notifications"
              onClick={() => setCentreOpen(false)}
            >
              ×
            </button>
          </header>
          <Show
            when={noticeRecords().length}
            fallback={<p class="notice-empty">No notifications yet.</p>}
          >
            <ul class="notice-list">
              <For each={noticeRecords()}>
                {(notice) => (
                  <li class={`notice-row notice-${notice.kind}`}>
                    <time class="tnum" title={new Date(notice.at).toLocaleString()}>
                      {formatTime(notice.at)}
                    </time>
                    <span class="notice-row-message">
                      {notice.message}
                      <Show when={notice.count > 1}>
                        <b class="notice-count">×{notice.count}</b>
                      </Show>
                    </span>
                    <Show when={notice.action}>
                      {(action) => (
                        <button
                          type="button"
                          class="notice-action"
                          onClick={() => runNoticeAction(notice.id)}
                        >
                          {action().label}
                        </button>
                      )}
                    </Show>
                  </li>
                )}
              </For>
            </ul>
          </Show>
        </section>
      </div>
    </Show>
  );
}
