# Theme token reference (generated — do not hand-edit)

Regenerate with `npm run gen:theme-docs` (web/). Source of truth:
`src/styles/tokens.css` (`:root` semantic layer). Raw values live in
`src/styles/palette.css`; theme files in `src/styles/themes/` or the
config `themes/` directory override `--pal-*` values only.

| Token | Default | Role |
|---|---|---|
| **Backgrounds** | | |
| `--bg-canvas` | `var(--pal-bg-canvas)` |  |
| `--bg-app` | `var(--pal-bg-app)` |  |
| `--bg-row-alt` | `var(--pal-bg-row-alt)` |  |
| `--bg-panel` | `var(--pal-bg-panel)` |  |
| `--bg-chrome` | `var(--pal-bg-chrome)` |  |
| `--bg-elevated` | `var(--pal-bg-elevated)` |  |
| `--bg-control` | `var(--pal-bg-control)` |  |
| `--bg-control-active` | `var(--pal-bg-control-active)` |  |
| `--bg-track` | `var(--pal-bg-track)` |  |
| `--bg-track-dim` | `var(--pal-bg-track-dim)` |  |
| **Borders** | | |
| `--border-strong` | `var(--pal-border-strong)` |  |
| `--border-default` | `var(--pal-border-default)` |  |
| `--border-control` | `var(--pal-border-control)` |  |
| `--border-subtle` | `var(--pal-border-subtle)` |  |
| `--border-row` | `var(--pal-border-row)` |  |
| `--border-row-dim` | `var(--pal-border-row-dim)` |  |
| **Text** | | |
| `--text-primary` | `var(--pal-text-primary)` |  |
| `--text-body` | `var(--pal-text-body)` |  |
| `--text-secondary` | `var(--pal-text-secondary)` |  |
| `--text-muted` | `var(--pal-text-muted)` |  |
| `--text-dim` | `var(--pal-text-dim)` |  |
| `--text-faint` | `var(--pal-text-faint)` |  |
| `--text-fainter` | `var(--pal-text-fainter)` |  |
| `--text-ghost` | `var(--pal-text-ghost)` |  |
| **State** | | |
| `--state-disabled` | `var(--pal-state-disabled)` |  |
| `--accent` | `var(--pal-accent)` |  |
| `--accent-ink` | `var(--pal-accent-ink)` |  |
| `--accent-tint` | `color-mix(in srgb, var(--accent) 22%, transparent)` | selected row bg |
| `--accent-tint-strong` | `color-mix( in srgb, var(--accent) 30%, transparent )` | sidebar active, chips, graph fill |
| `--accent-text` | `color-mix( in srgb, var(--accent) 55%, var(--accent-ink) )` | text on tinted chips |
| `--accent-foreground` | `var(--pal-accent-foreground)` | text on solid accent controls |
| **Focus (POL-8.5): accent-derived outline** | | |
| `--focus-ring` | `color-mix(in srgb, var(--accent) 45%, var(--accent-ink))` |  |
| **Rates** | | |
| `--rate-up` | `var(--pal-rate-up)` |  |
| `--rate-up-text` | `var(--pal-rate-up-text)` |  |
| **Progress: palette aliases, never the accent (see header)** | | |
| `--progress-active` | `var(--pal-progress-active)` |  |
| `--progress-complete` | `var(--pal-progress-complete)` |  |
| **Status** | | |
| `--status-error` | `var(--pal-status-error)` |  |
| `--status-warn` | `var(--pal-status-warn)` |  |
| **Label palette** | | |
| `--label-iso` | `var(--pal-label-iso)` |  |
| `--label-archive` | `var(--pal-label-archive)` |  |
| `--label-kernel` | `var(--pal-label-kernel)` |  |
| `--label-apps` | `var(--pal-label-apps)` |  |
| `--label-media` | `var(--pal-label-media)` |  |
| **Typography (theme-independent)** | | |
| `--font-sans` | `"IBM Plex Sans", system-ui, sans-serif` |  |
| `--fs-stat-value` | `22px` | 600, line-height 1.1 |
| `--fs-section-title` | `14px` | 600 — Settings section titles |
| `--fs-app-name` | `13px` | 600, letter-spacing 0.04em — wordmark, modal title |
| `--fs-cell` | `12.5px` | 400 — table cells, sidebar items |
| `--fs-control` | `12px` | 400 — buttons, inputs, tabs, detail cells |
| `--fs-detail` | `11.5px` | 400 — detail facts, tracker rows |
| `--fs-header` | `11px` | 500, uppercase, letter-spacing 0.05em — column headers |
| `--fs-meta` | `11px` | 400 — status bar, hints |
| `--fs-chip` | `10.5px` | 400 — label/priority chips |
| `--fs-caption` | `10px` | 400, uppercase, letter-spacing 0.10–0.14em — captions |
| `--fs-progress-label` | `9.5px` | 400, letter-spacing 0.02em — inline % on bars |
| **Fixed heights (dense — keep these)** | | |
| `--h-topbar` | `44px` |  |
| `--h-toolbar` | `36px` |  |
| `--h-table-header` | `28px` |  |
| `--h-table-row` | `30px` |  |
| `--h-detail-tabs` | `32px` |  |
| `--h-detail-header` | `24px` |  |
| `--h-detail-file-row` | `26px` |  |
| `--h-peer-header` | `26px` |  |
| `--h-peer-row` | `27px` |  |
| `--h-statusbar` | `26px` |  |
| `--h-sidebar-item` | `26px` |  |
| `--h-button` | `26px` | buttons 24–28 by context |
| `--h-input` | `28px` |  |
| `--h-menu-item` | `26px` |  |
| **Spacing steps in use** | | |
| `--sp-1` | `4px` |  |
| `--sp-2` | `6px` |  |
| `--sp-3` | `8px` |  |
| `--sp-4` | `10px` |  |
| `--sp-5` | `12px` |  |
| `--sp-6` | `14px` |  |
| `--sp-7` | `16px` |  |
| `--sp-8` | `18px` |  |
| `--sp-9` | `20px` |  |
| **Radii (a theme may override these and shadows only)** | | |
| `--r-chip` | `2px` | chips, progress bars, dots-as-squares |
| `--r-control` | `3px` | buttons, inputs, menu items |
| `--r-card` | `4px` | cards, dropzone, popovers |
| `--r-window` | `6px` | app window, large panels |
| **Shadows (a theme may override these and radii only)** | | |
| `--shadow-window` | `var(--pal-shadow-window)` |  |
| `--shadow-modal` | `var(--pal-shadow-modal)` |  |
| `--shadow-menu` | `var(--pal-shadow-menu)` |  |
| **Motion** | | |
| `--t-progress` | `300ms linear` | progress-bar width transitions |
| `--t-row-fade` | `120ms` | row insert/remove fade |
