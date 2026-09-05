# Taste

## Workflow
- When a task is ambiguous at the scope level (mount-only vs. also implementing the backfill feature), asks a focused single clarifying question with a recommended default, then proceeds without further back-and-forth. Once scope is set, however, technical unknowns (e.g., how rTorrent's `d.bitfield` encodes pieces, or yaml duration marshalling) are resolved independently via research, experiments, and reference-behavior checks — not by interrupting the user. Confidence: 0.6
- Keeps the diff free of unrelated churn: when a broad `gofmt -w` reformats pre-existing files outside the task's scope, inspects the diff, reverts those files, and re-runs the affected suites before finishing. Now applied proactively — later runs scope formatting/vet to exclude the known pre-existing unformatted files rather than touching them. Confidence: 0.65
- Keeps infrastructure and deployment configuration consistent across every variant when touching deploy plumbing — the base `docker-compose.yml`, the linux and macos override files, entrypoint scripts, and bootstrap-generated configs all get the same volume/path change, and the changed deploy files are validated with `docker compose config`. Confidence: 0.8
- Config values that were previously informational/read-only get upgraded to editable, meaningful settings (and vice-versa documented in code comments/schema) as soon as they start driving real behavior. Confidence: 0.55

## Communication
- Files a full end-of-task summary organized by work area with a bulleted inventory of shipped changes plus the verification evidence, including deferred items explicitly (which then become the next work queue). Confidence: 0.8
