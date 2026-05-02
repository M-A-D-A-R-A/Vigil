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

## Next

- [ ] Add a browser-level first-run and explorer smoke test harness.

## Later

- [ ] Add log context around a selected event.
- [ ] Add a field sidebar based on discovered `attrs` / `body` keys.
- [ ] Add a histogram for ingest volume or event volume.
- [ ] Add saved views / saved filtered links.

## Later If Vigil Expands

- [ ] Add auth and multi-user RBAC.
- [ ] Add teams / sources abstractions.
- [ ] Add alerts and webhooks.
- [ ] Add a CLI.
- [ ] Add query cancellation and export jobs.
- [ ] Add provisioning flows.
- [ ] Add AI query assist.

## Not Now

- [ ] Do not add ClickHouse source management in the current Vigil shape.
- [ ] Do not add schema-agnostic external table management in V1.
- [ ] Do not move Vigil to SQL-first querying in V1.
- [ ] Do not add OIDC and full user-management surface in the current phase.
