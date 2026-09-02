# The outage run sheet

```sh
rebootstrap plan render --file PLAN --format runsheet --out run-sheet.md
```

## Who this document is for

One reader, in one situation: an operator working a cluster rebuild at three in
the morning, on the worst day this tool will ever be used. Tired, reading top
to bottom, about to type something that may be irreversible.

Everything about the run sheet's shape follows from that reader. It is not a
summary of the plan for a reviewer — `--format text` is that, and it stays
exactly as it was. The run sheet is a working document meant to be printed or
held open on a second screen, marked up as the rebuild proceeds, and then
transcribed into the run's evidence.

## The rules the format follows

**Warnings come before the thing they warn about.** For every step, the STOP
conditions and the operator confirmation prompt are printed *above* the
command. A warning printed under a command is read after the command has
already been run. This is the single property the format exists to guarantee,
and `TestRunsheetWarnsBeforeItActs` fails if it is ever violated.

**A destructive step is announced, not annotated.** `--format text` marks
destruction with the word `DESTRUCTIVE` at the end of a header line. The run
sheet opens the step with a banner block that says the step destroys data,
cannot be undone, and must not be started until every STOP condition is false
and the acknowledgement is recorded word for word.

**The operator learns up front where they are personally needed.** Before step
1, the sheet lists every step that is `operator-only` or gated behind a
confirmation, with its number, whether it is destructive, and why it needs a
person. Discovering at step 4 of 6 that step 5 cannot proceed without you is
how a rebuild stalls at the point it can least afford to.

**Nothing is a blank.** An unbound checkpoint digest renders as
*(not bound yet)* and an unstamped CLI version renders as `unknown`, because a
blank cell reads as an oversight while a named absence reads as a fact.

**No step can vanish.** Steps are grouped by phase in execution order. A
validated plan is phase-homogeneous, so this is normally one group — but if a
step names a phase the renderer does not recognize, that phase is printed
anyway rather than dropped. A run sheet that silently omits a step is worse
than one showing an odd phase name.

**Observations are checkboxes.** Every STOP condition, precondition, and
observation is a `- [ ]` item, and every step ends with an outcome line
(`done` / `not-started` / `STOPPED — escalate`) and a notes rule. The
vocabulary is deliberately the same one the `resume` phase uses to classify an
interrupted step, so what the operator wrote on paper maps directly onto what
the next plan needs to know.

## What the header binds

The header table carries the plan ID and phase, the plan digest, the profile
digest, the recovery commit, the checkpoint digest, and the version of the
binary that rendered the sheet. Those six values are what make a printed sheet
traceable: a sheet whose plan digest does not match the plan being executed is
stale, and the closing-out section says so explicitly.

Pair it with `rebootstrap version`, which prints the same binary's identity as
canonical JSON:

```sh
rebootstrap version
{"buildDate":"...","commit":"...","schemaVersion":1,"version":"v0.1.0"}
```

`commit` and `buildDate` come from the Go toolchain's VCS stamp — the revision
the binary was built from and that revision's timestamp — with `-dirty`
appended when the build tree had uncommitted changes. A binary reporting
`"version":"dev"`, `"commit":"unknown"`, or a `-dirty` suffix was not produced
by the pinned release pipeline and is not recovery tooling.

## The renderer does not execute anything

`internal/render` imports `fmt`, `io`, `strconv`, `strings`, and the plan
package. It has no `os/exec`, no `os`, and no `net` import at all. Rendering a
plan turns it into bytes; it never runs a step's argv. That is why delegated
commands are shown as quoted argv tokens inside a fenced block with an explicit
note that there is no shell — no globbing, no pipes, no variable expansion —
rather than as a copy-pasteable shell line that would quietly imply otherwise.

## Drift is a test failure

`examples/synthetic/runsheet.golden.md` is the run sheet rendered from
`examples/synthetic/plan.json`, checked in. Any change to the wording, the
ordering, or the checkbox vocabulary fails `TestRunsheetMatchesGolden`. That is
the point: this document is read under pressure, so a change to it should be a
deliberate decision someone reviewed, not a diff absorbed silently.

Regenerate it only when the change is intended:

```sh
go run ./cmd/rebootstrap plan render --format runsheet \
  --file examples/synthetic/plan.json \
  --out examples/synthetic/runsheet.golden.md
```

The golden pins the `dev` version sentinel, because tests are never built with
the release linker flags. A stamped release binary renders its own version in
the header; that one line is the only difference, and the golden test skips
rather than fails if it is ever run from a stamped build.
