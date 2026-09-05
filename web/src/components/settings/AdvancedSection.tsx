// Advanced section: read-only daemon values + raw XML-RPC (POL-8.8).
import { For } from "solid-js";
import type { SectionProps } from "./types";

export function AdvancedSection(props: SectionProps) {
  return (
    <section>
      <h1>Advanced</h1>{" "}
      <p class="settings-intro">
        Live values below are read-only. The escape hatch executes an XML-RPC method after
        confirmation and logs its method name.
      </p>
      <div class="advanced-values">
        <For each={Object.keys(props.daemon).sort()}>
          {(key) => (
            <div>
              <span>{key}</span>
              <b>{props.daemon[key]}</b>
            </div>
          )}
        </For>
      </div>
      <h2>Execute XML-RPC method</h2>
      <div class="raw-rpc">
        <input
          value={props.rawMethod}
          placeholder="method.name"
          onInput={(event) => props.setRawMethod(event.currentTarget.value)}
        />
        <textarea
          value={props.rawParams}
          placeholder="One string parameter per line"
          onInput={(event) => props.setRawParams(event.currentTarget.value)}
        />
        <button type="button" onClick={() => void props.executeRaw()}>
          Execute after confirmation
        </button>
      </div>
    </section>
  );
}
