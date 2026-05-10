# TODO

## Landed

- [x] Move the explorer UI toward a denser, more tool-like layout inspired by LogChef.
- [x] Add project-level time controls with quick ranges and custom `from` / `to`.
- [x] Make `live tail` a shared project-level control across logs, traces, and stats.
- [x] Keep `live tail` off by default.
- [x] Add compact vs detail log modes.
- [x] Make log rows render message-first in compact mode, with JSON/details secondary.
- [x] Add export for the current explorer view.
- [x] Add copy/share filtered URL support.
- [x] Add result warnings for capped and partial query responses.
- [x] Add a real retention config surface with day-based pruning, dry-run, and SQLite rebuild.
- [x] Add a repo-local log benchmark runner with raw `.ndjson`, SQLite, and query timing output.
- [x] Record the first 100k log API benchmark baseline in `benchmark.md`.
- [x] Batch SQLite index writes to reduce 100k log index catch-up from 27.874s to 270.5ms.
- [x] Add a browser-level first-run and explorer smoke test harness.
- [x] Add terminal-based config and onboarding for local setup: `vigil init`, create/select project, store server URL, write app `.env`, print ingest command, and make the active project obvious.
- [x] Add release tarballs and an install script for installing `vigil` from GitHub Releases.

## Next

- [ ] Add a Python SDK for ingesting logs, traces, metrics, and configuring projects from Python apps.
- [ ] Add a TypeScript SDK for ingesting logs, traces, metrics, and configuring projects from Node/browser-adjacent apps.
- [ ] Add a first-pass `vigil ask` / answer layer for natural-language questions over logs, traces, stats, and event context.
- [ ] Add benchmark coverage for traces and metrics using the same API create / ingest / query flow.
- [ ] Add a query-while-ingesting benchmark to measure live search latency during bursts.
- [ ] Add repeated benchmark runs and report min / median / max to reduce one-run noise.
- [ ] Track indexing lag and storage size as first-class benchmark trend metrics.

## Later

- [ ] Add a root-page server settings UI for runtime settings such as retention, backed by persisted server settings.
- [ ] Add log context around a selected event.
- [ ] Add a field sidebar based on discovered `attrs` / `body` keys.
- [ ] Add a histogram for ingest volume or event volume.
- [ ] Add saved views / saved filtered links.
- [ ] Add larger 500k and 1M log benchmark profiles.
- [ ] Add larger payload body benchmark profiles.
- [ ] Add an evidence-first answer UI that shows the query answer plus the matching events, traces, and time window behind it.

## Later If Vigil Expands

- [ ] Add auth and multi-user RBAC.
- [ ] Add teams / sources abstractions.
- [ ] Add alerts and webhooks.
- [ ] Expand the CLI beyond local config into day-to-day project, ingest, export, and admin commands.
- [ ] Add query cancellation and export jobs.
- [ ] Add provisioning flows.
- [ ] Add model-provider configuration for the `vigil ask` layer once the local retrieval workflow is proven.

## Not Now

- [ ] Do not add ClickHouse source management in the current Vigil shape.
- [ ] Do not add schema-agnostic external table management in V1.
- [ ] Do not move Vigil to SQL-first querying in V1.
- [ ] Do not add OIDC and full user-management surface in the current phase.
