// Shared field builders (POL-8.8): the tuning input/check rows used by the
// Connection and Queue sections (and the Bandwidth section via the
// dispatcher-provided input renderer).
import { SettingRow } from "./SettingRow";
import { daemonKey, textValue, valueFor } from "./model";
import type { JSX } from "solid-js";
import type { Draft } from "./types";

export type FieldProps = {
  draft: Draft;
  daemon: Record<string, string>;
  errors: Record<string, string>;
  updateTuning: (key: string, value: unknown) => void;
};

export function makeFields(props: FieldProps) {
  const value = (field: string) => valueFor(props.draft, props.daemon, field, daemonKey(field));
  const input = (
    field: string,
    label: string,
    hint: string,
    type: "number" | "text" = "number",
  ): JSX.Element => (
    <SettingRow label={label} hint={hint} error={props.errors[field]}>
      <input
        type={type}
        value={textValue(value(field))}
        onInput={(event) =>
          props.updateTuning(
            field,
            type === "number" ? Number(event.currentTarget.value) : event.currentTarget.value,
          )
        }
      />
    </SettingRow>
  );
  const check = (field: string, label: string, hint: string): JSX.Element => (
    <SettingRow label={label} hint={hint}>
      <input
        class="settings-check"
        type="checkbox"
        checked={Boolean(value(field)) && textValue(value(field)) !== "0"}
        onChange={(event) => props.updateTuning(field, event.currentTarget.checked)}
      />
    </SettingRow>
  );
  return { value, input, check };
}
