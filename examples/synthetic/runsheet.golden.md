# Outage run sheet — synthetic-resume

Work this sheet from top to bottom. For every step the STOP conditions and
the operator confirmation are printed **above** the command they guard: if a
STOP condition holds, stop there and do not run the command below it.

Nothing in this document runs itself. Rendering a plan never executes it.

| Field | Value |
| --- | --- |
| Plan ID | `synthetic-resume` |
| Phase | `resume` |
| Plan digest | `sha256:18a86974ad0ca0e204a589e8336d7261c0c10ddf8a848b0d5c3f59dfaba86765` |
| Profile digest | `sha256:0000000000000000000000000000000000000000000000000000000000000000` |
| Recovery commit | `0123456789abcdef0123456789abcdef01234567` |
| Checkpoint digest | `sha256:1111111111111111111111111111111111111111111111111111111111111111` |
| Rendered by | `rebootstrap dev` |
| Steps | 6 total · 1 destructive · 2 operator-only |

## Where you are personally needed

The 2 steps below cannot proceed without you. Read this list before starting
step 1, so no hand-off arrives as a surprise mid-outage.

| Step | ID | Why you | Destructive |
| --- | --- | --- | --- |
| 01 | `classify-interrupted-steps` | operator-only | no |
| 04 | `discard-partial-replicas` | operator-only, and confirmation required | **YES** |

## Phase: resume

### 01 · classify-interrupted-steps

`operator-only` · read-only

**STOP — do not proceed if any of these is true:**

- [ ] an interrupted step's outcome is still unknown

**Preconditions — all must hold first:**

- [ ] the run journal has been reconciled
- [ ] the checkpoint digest is readable

Depends on: nothing — this step can start once its preconditions hold

**Do this by hand — this step is yours:**

> Classify every interrupted restore step as done, not-started, or unknown before any retry is planned.

**Observe and record:**

- [ ] every interrupted step has a recorded done or not-started outcome

Outcome: `[ ] done`  `[ ] not-started`  `[ ] STOPPED — escalate`

Notes: ______________________________________________________________

---

### 02 · verify-source-digests

`delegated` · read-only

**STOP — do not proceed if any of these is true:**

- [ ] a source digest differs from the checkpoint

**Preconditions — all must hold first:**

- [ ] interrupted step outcomes are classified

Depends on: `classify-interrupted-steps`

**Run this — argv, no shell (adapter `restic`):**

```
"restic" "snapshots" "--json"
```

Each quoted token above is one argument. There is no shell here: no
globbing, no pipes, no variable expansion. Type the tokens as written.

**Observe and record:**

- [ ] every retained source digest matches the checkpoint

Outcome: `[ ] done`  `[ ] not-started`  `[ ] STOPPED — escalate`

Notes: ______________________________________________________________

---

### 03 · survey-volume-attachments

`delegated` · read-only

**STOP — do not proceed if any of these is true:**

- [ ] a volume is partially attached

**Preconditions — all must hold first:**

- [ ] source digests match the checkpoint

Depends on: `verify-source-digests`

**Run this — argv, no shell (adapter `kubectl`):**

```
"kubectl" "get" "volumeattachments" "--output" "json"
```

Each quoted token above is one argument. There is no shell here: no
globbing, no pipes, no variable expansion. Type the tokens as written.

**Observe and record:**

- [ ] every restored volume is fully attached or fully detached

Outcome: `[ ] done`  `[ ] not-started`  `[ ] STOPPED — escalate`

Notes: ______________________________________________________________

---

### 04 · discard-partial-replicas

`operator-only` · mutating, **DESTRUCTIVE**

> **!! DESTRUCTIVE STEP !!**
>
> **This step destroys data and cannot be undone.** Do not run it until
> every STOP condition below is false, the preconditions are all checked
> off, and the acknowledgement is recorded word for word. If you are
> unsure about any of those, stop and escalate instead.

**STOP — do not proceed if any of these is true:**

- [ ] any target is not on the reviewed partial-replica list
- [ ] no complete replica of the volume is verified

**Preconditions — all must hold first:**

- [ ] each target appears on the attachment survey as partially written
- [ ] a complete replica of the same volume is verified

Depends on: `survey-volume-attachments`

**Operator confirmation — REQUIRED before the action below:**

- Prompt: Discard only the listed partially written replicas; this is irreversible.
- [ ] Acknowledged, recorded word for word as:

      I confirm the listed partial replicas and understand the irreversible data-loss boundary.

**Do this by hand — this step is yours:**

> Discard the explicitly listed partially written replicas so the restore can resume from the checkpoint.

**Observe and record:**

- [ ] operator records each discarded replica and its verified complete counterpart

Outcome: `[ ] done`  `[ ] not-started`  `[ ] STOPPED — escalate`

Notes: ______________________________________________________________

---

### 05 · resume-restore-waves

`agent-assisted` · mutating

**STOP — do not proceed if any of these is true:**

- [ ] a source digest differs from the checkpoint
- [ ] a wave would retry a step whose outcome is unknown

**Preconditions — all must hold first:**

- [ ] no partially attached volumes remain
- [ ] every source digest matches the checkpoint

Depends on: `discard-partial-replicas`

**Agent-assisted — an agent proposes, you approve each effect:**

> Resume the pinned restore waves from the checkpoint, skipping only steps recorded as done.

**Observe and record:**

- [ ] each resumed wave attaches an artifact-verified receipt

Outcome: `[ ] done`  `[ ] not-started`  `[ ] STOPPED — escalate`

Notes: ______________________________________________________________

---

### 06 · confirm-resume-complete

`automatic` · read-only

**STOP — do not proceed if any of these is true:**

- [ ] a required restore receipt is missing

**Preconditions — all must hold first:**

- [ ] all resumed waves reported receipts

Depends on: `resume-restore-waves`

**Automatic — no operator action; confirm it happened:**

> Confirm the restore receipt set matches the checkpoint manifest before recommission is planned.

**Observe and record:**

- [ ] the restore receipt set matches the checkpoint manifest

Outcome: `[ ] done`  `[ ] not-started`  `[ ] STOPPED — escalate`

Notes: ______________________________________________________________

---

## Closing out

- [ ] Every step above is marked done, not-started, or STOPPED.
- [ ] Every STOP condition that fired is written down, with what was done about it.
- [ ] Every acknowledgement recorded matches its required text word for word.
- [ ] Observations are transcribed into the run's evidence, not left on paper.

This sheet was rendered from plan digest

    sha256:18a86974ad0ca0e204a589e8336d7261c0c10ddf8a848b0d5c3f59dfaba86765

If the plan is rebound or re-authored that digest changes, and this sheet is
stale. Render a new one rather than working from this copy.
