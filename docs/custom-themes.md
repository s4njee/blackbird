# Custom themes and operator CSS (THM-9.4)

Blackbird ships five built-in themes (Dark, Light, Midnight, High Contrast,
Classic). Operators can add their own without rebuilding: drop versioned
YAML files into `themes/` beside the config file (next to `config.yml`).

## Theme files

`themes/<name>.yml` (or `.yaml`):

```yaml
version: 1
name: "Harbor"
description: "Cool dark blue"
extends: "dark"          # dark | light | midnight | contrast | classic
dark: true
accents: ["#4c5fd5", "#f59e0b", "#e5484d", "#2f9dff", "#3fb950"]  # exactly 5
palette:
  bg-app: "#0d1117"
  text-body: "#d6d9dd"
accent: ""               # optional override (#rrggbb) or empty
density: ""              # optional: dense | comfortable
preview:                 # optional mini-preview swatches (else derived)
  bg: "#0d1117"
  panel: "#11161c"
  text: "#d6d9dd"
  accent: "#4c5fd5"
  progress: "#2f9dff"
```

Rules:

- `version` must be `1`; `name` is required and becomes the file identity.
- With `extends`, `palette` may be a subset (missing keys inherit the
  built-in base). Without `extends`, `palette` must define the complete
  set — see the token reference below.
- Only `--pal-*` values may be overridden (palette names without the
  `pal-` prefix, e.g. `bg-app` for `--pal-bg-app`). Unknown keys are
  ignored with a warning in Settings.
- Accent derivations (`--accent-tint`, `--accent-text`, `--focus-ring`)
  compute from your accent automatically; on light themes set
  `accent-ink: "#000000"` so derived text stays readable, and keep
  progress blue/green — progress is never the accent.
- Every key and value is validated on load; invalid files are skipped with
  a `file.yml:LINE: message` error in the server log (and a count in
  Settings → Interface). Unknown top-level keys are rejected (typo guard).
- Token reference (generated from the semantic layer, never hand-edited):
  [theme-tokens.md](./theme-tokens.md).

Check a file before installing: the picker import validates the same way
and reports line-numbered errors inline.

## Import, export, delete

Settings → Interface → Custom themes: **Import theme file** validates and
installs (overwriting the same name counts as an update), **Export current
theme** downloads the visible theme — built-in or custom — as a complete
YAML file including the current accent override and density, and each
installed file can be deleted (with confirmation) from the picker.

## Operator stylesheet (`custom.css`)

An optional `custom.css` beside the config file is fetched with the app's
authentication and injected after the theme on every load. Use it for small
targeted overrides.

Stability warning: `custom.css` reaches past the token layer into
component internals (class names, layout) that may change between
releases without notice. Prefer theme files: every `--pal-*` value is a
documented, versioned surface, while component CSS is not. Large
`custom.css` files (over 256 KB) are refused. Settings → Interface shows
whether the file is active and its size.
