# cluster-rebootstrap

`cluster-rebootstrap` is a small, file-backed control plane for safe cluster
replacement and disaster rebootstrap. It records readiness evidence and
operator decisions before later adapters invoke Talos, Kubernetes, Flux,
storage, or database tooling.

The initial slice is intentionally dependency-free and does not contact a
cluster, a Git forge, an object store, or an in-cluster service. It provides:

- versioned profile and gate contracts;
- a single-writer run directory with an append-only event journal;
- atomic status and gate projections;
- canonical JSON reports suitable for external receipt storage; and
- an explicit operator authorization boundary.

## Gate semantics

Every mandatory predicate in the profile must have status `PASS` before a run
is eligible. Eligibility is not authorization. The command produces `GO` only
when a separate, valid operator-authorization document is supplied with
`--authorization`; otherwise it produces `NO-GO` with `eligible: true`.

No command in this release performs cluster mutation.

## Quick start

```sh
go run ./cmd/rebootstrap profile validate --profile examples/synthetic/profile.json

go run ./cmd/rebootstrap gate evaluate \
  --run /tmp/rebootstrap-run \
  --run-id synthetic-001 \
  --profile examples/synthetic/profile.json \
  --input examples/synthetic/evidence.json \
  --authorization examples/synthetic/authorization.json

go run ./cmd/rebootstrap status --run /tmp/rebootstrap-run
go run ./cmd/rebootstrap report --run /tmp/rebootstrap-run
```

The run directory contains `run.json`, `events.ndjson`,
`projections/status.json`, `projections/gate.json`, and `reports/gate.json`.
Events are appended before projections are atomically replaced. A lock file
prevents concurrent writers. Secrets, kubeconfigs, decrypted SOPS documents,
authorization headers, and raw command output are outside this contract and
must not be placed in evidence.

## Development

```sh
gofmt -w cmd internal
go test ./...
go test -race ./...
go vet ./...
```

The planned adapters and execution commands are described in the appservice
design brief, but are deliberately not included in this first public slice.
