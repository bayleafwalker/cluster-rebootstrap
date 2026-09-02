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
  decommission, bootstrap, restore, resume, and recommission;
- plan validation, anchor binding, human rendering, an operator run sheet, and
  non-executing dry-runs; and
- a canonical build identity, so a plan or gate report can name the exact tool
  that produced it.

## Gate semantics

Every mandatory predicate in the profile must have status `PASS` before a run
is eligible. Eligibility is not authorization. The command produces `GO` only
when a separate, valid operator-authorization document is supplied with
`--authorization`; otherwise it produces `NO-GO` with `eligible: true`.

Authorization is bound to the run ID, profile digest, recovery commit,
checkpoint digest, evidence digest, and execution-plan digest. A document from
another run or checkpoint is rejected before a new run is created.

`gate evaluate` writes the full canonical report to stdout either way, and then
sets its exit code from the decision:

| Exit | Meaning |
| --- | --- |
| `0` | `GO` |
| `1` | `NO-GO` — a complete, valid verdict that is not a GO |
| `2` | an input could not be read or parsed; there is no verdict |

`1` and `2` are kept apart deliberately: a wrapper must never mistake an
unreadable evidence file for a considered NO-GO. These are the same codes the
operating repository's decision-gate evaluator uses, so one wrapper drives
either.

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

### Binding a plan to real anchors

Because a plan is digest-bound, filling in an anchor by hand invalidates it
until the digest is recomputed by hand too. `plan bind` is the supported way:
it takes an authored plan whose anchors are placeholders, substitutes the real
ones, re-derives the plan digest, and validates the result before writing it.

```sh
rebootstrap plan bind --file authored.json \
  --profile-digest sha256:… \
  --recovery-commit 0123456789abcdef0123456789abcdef01234567 \
  [--checkpoint-digest sha256:…] \
  [--out bound.json]
```

`--checkpoint-digest` is optional, which is what lets a plan be frozen before
the quiesced window has produced a checkpoint. Bind is not a plan editor: it
copies steps verbatim and refuses a plan carrying a destructive step without a
confirmation requirement, so rebinding cannot launder an unsafe plan into an
executable one.

### Rendering

`plan render` has two formats and they serve different readers.

```sh
rebootstrap plan render --file PLAN [--format text|runsheet] [--out FILE]
```

`--format text` (the default) is the flat listing: a digest check for a
reviewer who already knows the plan. Its bytes are frozen.

`--format runsheet` writes the outage run sheet — a Markdown document worked
top to bottom, grouped by phase, with a header binding it to the plan digest,
recovery commit, checkpoint digest, and rendering CLI version; an up-front
index of every step needing the operator in person; a banner on every
destructive step; and checkboxes for STOP conditions, preconditions, and
observations. Every STOP condition and confirmation prompt is printed **above**
the command it guards, because a warning under a command is read after the
command has been run. See [`docs/run-sheet.md`](docs/run-sheet.md).

Rendering is pure. `internal/render` has no `os/exec`, `os`, or `net` import;
it turns a plan into bytes and never executes a step's argv.

### Build identity

```sh
rebootstrap version
{"buildDate":"…","commit":"…","schemaVersion":1,"version":"v0.1.0"}
```

`version` prints this binary's identity as canonical JSON so a plan, a run, or
a gate report can bind to the exact tool that produced it. `commit` and
`buildDate` come from the Go toolchain's VCS stamp — the revision built from
and its timestamp — with `-dirty` appended when the tree had uncommitted
changes. A binary reporting `"version":"dev"`, `"commit":"unknown"`, or a
`-dirty` suffix was not produced by the pinned release pipeline; see
[`RELEASING.md`](RELEASING.md).

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
go run ./cmd/rebootstrap plan render --file examples/synthetic/plan.json \
  --format runsheet --out /tmp/run-sheet.md
go run ./cmd/rebootstrap plan dry-run --file examples/synthetic/plan.json

go run ./cmd/rebootstrap version
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

`examples/synthetic/runsheet.golden.md` is the run sheet rendered from the
synthetic plan, checked in. Any drift in its wording or ordering fails
`TestRunsheetMatchesGolden`; regenerate it only when the change is intended.

The CLI deliberately does not execute plan argv values yet. Cluster/Talos/Flux
adapters remain a later, separately reviewed slice.
