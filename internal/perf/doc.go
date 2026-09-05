// Package perf guards the Epic 6 budgets in CI (PERF-6.6). The benchmarks
// themselves live next to the code they measure (internal/poller,
// internal/api); this package only runs them, compares against
// docs/performance-baselines.json, and fails on regression. See
// docs/performance.md for the full report.
package perf
