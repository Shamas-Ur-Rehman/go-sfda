# Changelog

Not yet released. The following notes are a proposed `v0.1.0` summary and
should be reviewed before publishing.

## Unreleased / proposed v0.1.0

### Added

- Reusable Go client for the public SFDA drugs list and detail dialogs.
- Configurable bounded detail fetching, request pacing, retries, filtering,
  and Excel output.
- Page-level resumable JSON checkpoints and buffered storage flush support.
- Streaming Excel output and an opt-in one-page live smoke-test consumer.
- MongoDB and PostgreSQL adapter patterns.
- GitHub Actions validation for formatting, tests, race tests, vet, and builds.

### Limitations

- SFDA frontend routes and markup are not a versioned API.
- Checkpointed sink processing is at-least-once; database sinks should be
  idempotent on registration number.
- No license has been selected or added pending maintainer approval.
