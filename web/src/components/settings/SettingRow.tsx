// SettingRow (POL-8.8): labelled settings control row.
import { Show, type JSX } from "solid-js";

export function SettingRow(props: {
  label: string;
  hint: string;
  error?: string;
  /** Apply behavior badge (POL-8.4): restart/reconnect where the field needs
   * it per lib/applyBehavior. Live fields (everything editable today) show
   * no badge. */
  needs?: "restart" | "reconnect";
  children: JSX.Element;
}) {
  // The label wraps the control cell, so every input/select/textarea it
  // contains is implicitly labelled (POL-8.5) with no per-row id plumbing.
  // Children never contain buttons or nested labels (verified), so label
  // activation always lands on the row's own control.
  return (
    <label class="setting-row">
      <span class="setting-row-head">
        <span class="setting-row-label">
          {props.label}
          <Show when={props.needs}>
            <span class={`needs-badge needs-${props.needs}`}>{props.needs} required</span>
          </Show>
        </span>
        <small>{props.hint}</small>
      </span>
      <span class="setting-control">
        {props.children}
        <Show when={props.error}>
          <p>{props.error}</p>
        </Show>
      </span>
    </label>
  );
}
