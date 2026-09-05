// Labels section (POL-8.8).
import { For, Show } from "solid-js";
import type { SectionProps } from "./types";

export function LabelsSection(props: SectionProps) {
  return (
    <section>
      <h1>Labels</h1>
      <div class="label-editor">
        <For each={props.draft.labels}>
          {(label, index) => (
            <div class="label-edit-row">
              <input
                type="color"
                value={label.color}
                onInput={(event) =>
                  props.setDraft((current) => ({
                    ...current,
                    labels: current.labels.map((item, i) =>
                      i === index() ? { ...item, color: event.currentTarget.value } : item,
                    ),
                  }))
                }
              />
              <input
                value={label.name}
                onInput={(event) =>
                  props.setDraft((current) => ({
                    ...current,
                    labels: current.labels.map((item, i) =>
                      i === index() ? { ...item, name: event.currentTarget.value } : item,
                    ),
                  }))
                }
              />
              <button type="button" onClick={() => props.deleteLabel(label.name)}>
                Delete
              </button>
              <Show when={props.errors[`label-${index()}`] || props.errors[`color-${index()}`]}>
                <small>
                  {props.errors[`label-${index()}`] || props.errors[`color-${index()}`]}
                </small>
              </Show>
            </div>
          )}
        </For>
        <button
          class="settings-add-row"
          type="button"
          onClick={() =>
            props.setDraft((current) => ({
              ...current,
              labels: [...current.labels, { name: "new label", color: "#f59e0b" }],
            }))
          }
        >
          + Add label
        </button>
      </div>
    </section>
  );
}
