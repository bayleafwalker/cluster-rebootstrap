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
- typed, digest-bound, phase-homogeneous plans, one per phase:
  decommission, bootstrap, restore, resume, and recommission; and
- plan validation, human rendering, and non-executing dry-runs.

## Gate semantics

Every mandatory predicate in the profile must have status `PASS` before a run
is eligible. Eligibility is not authorization. The command produces `GO` only
when a separate, valid operator-authorization document is supplied with
`--authorization`; otherwise it produces `NO-GO` with `eligible: true`.

Authorization is bound to the run ID, profile digest, recovery commit,
checkpoint digest, evidence digest, and execution-plan digest. A document from
another run or checkpoint is rejected before a new run is created.

No command in this release performs cluster mutation.

## Execution-plan semantics

A plan declares exactly one phase, and every step in it must declare that same
phase. Plans are therefore phase-homogeneous: `decommission`, `bootstrap`,
`restore`, `resume`, and `recommission` each get their own plan, which is what
the decision gate requires. `resume` is a phase in its own right rather than a
fold into `restore` or `reconcile`: resuming after an interrupted external
effect carries its own preconditions and its own STOP conditions ("a source
digest differs from the checkpoint", "a volume is partially attached"), while
`rebootstrap reconcile` repairs projections from the journal and never touches
cluster state.

Every plan step declares its phase, dependencies, preconditions, STOP
conditions, observations, mutation/destruction flags, and whether it is
automatic, delegated, agent-assisted, or operator-only. Delegated commands use
an argv array plus typed adapter/executable identities; shell command strings,
interpreter wrappers, and shell metacharacters are rejected.

Validation rejects stale digests, an unknown or mismatched phase, unknown
dependencies, dependency cycles, and destructive steps without explicit
confirmation requirements. A confirmation record is valid only for its exact
plan digest and must acknowledge every destructive step.

## The digest rule

There is one digest rule in this repository, and every digest follows it:

> **sha256 over `encoding/json` of the typed value, with no trailing newline**,
> rendered as `sha256:` plus lowercase hex.

`model.CanonicalDigestBytes` produces those bytes and `model.Digest` hashes
them. `model.ProfileDigest`, `model.EvidenceDigest`, and
`plan.Plan.CanonicalDigest` are all thin wrappers over it; a plan is hashed with
its own `planDigest` field blank so the digest does not depend on the digest
recorded inside it. Struct field order is fixed by declaration and
`encoding/json` sorts map keys, so the encoding is deterministic.

`model.CanonicalJSON` is those same bytes plus a single trailing newline. That
newline is framing for the NDJSON event journal, the projections, and stdout
receipts — it is never part of a digest input.

Each digest function is pinned by a golden vector in its package tests. Changing
the rule changes every recorded digest, so the vectors fail first and loudly.
The plan digest quoted in the quick start below also appears in
`examples/synthetic/plan.json` and `examples/synthetic/authorization.json`;
regenerate the three together with `rebootstrap plan digest`.

## Quick start

```sh
go run ./cmd/rebootstrap profile validate --profile examples/synthetic/profile.json

go run ./cmd/rebootstrap gate evaluate \
  --run /tmp/rebootstrap-run \
  --run-id synthetic-001 \
  --profile examples/synthetic/profile.json \
  --input examples/synthetic/evidence.json \
  --recovery-commit 0123456789abcdef0123456789abcdef01234567 \
  --checkpoint-digest sha256:1111111111111111111111111111111111111111111111111111111111111111 \
  --plan-digest sha256:18a86974ad0ca0e204a589e8336d7261c0c10ddf8a848b0d5c3f59dfaba86765 \
  --authorization examples/synthetic/authorization.json

go run ./cmd/rebootstrap status --run /tmp/rebootstrap-run
go run ./cmd/rebootstrap report --run /tmp/rebootstrap-run
go run ./cmd/rebootstrap reconcile --run /tmp/rebootstrap-run

go run ./cmd/rebootstrap plan validate --file examples/synthetic/plan.json
go run ./cmd/rebootstrap plan digest --file examples/synthetic/plan.json
go run ./cmd/rebootstrap plan render --file examples/synthetic/plan.json
go run ./cmd/rebootstrap plan dry-run --file examples/synthetic/plan.json
```

`examples/synthetic/plan.json` is a `resume` plan: it classifies the outcome of
every interrupted step before any retry, refuses to proceed while a source
digest differs from the checkpoint or a volume is partially attached, and gates
its one destructive step behind an exact operator acknowledgement.

The run directory contains `run.json`, `events.ndjson`,
`projections/status.json`, `projections/gate.json`, and `reports/gate.json`.
Events are appended before projections are atomically replaced. A lock file
prevents concurrent writers. `rebootstrap reconcile --run RUN` replays the
durable journal to repair projections after an interrupted write. Evidence is
validated at the model and journal boundary: it is single-line, bounded, and
credential-shaped/raw output is rejected. Secrets, kubeconfigs, decrypted SOPS
documents, authorization headers, and raw command output cannot be placed in
evidence.

## Development

```sh
gofmt -w cmd internal
go test ./...
go test -race ./...
go vet ./...
```

The CLI deliberately does not execute plan argv values yet. Cluster/Talos/Flux
adapters remain a later, separately reviewed slice.
