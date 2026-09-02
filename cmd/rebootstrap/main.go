package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/bayleafwalker/cluster-rebootstrap/internal/gate"
	"github.com/bayleafwalker/cluster-rebootstrap/internal/model"
	"github.com/bayleafwalker/cluster-rebootstrap/internal/plan"
	"github.com/bayleafwalker/cluster-rebootstrap/internal/render"
	"github.com/bayleafwalker/cluster-rebootstrap/internal/run"
	"github.com/bayleafwalker/cluster-rebootstrap/internal/version"
)

// Exit codes are a contract, not an implementation detail: a caller wiring
// this into a gate has to tell "the gate said NO-GO" apart from "I could not
// read the evidence". They match the decision-gate evaluator in the operating
// repository, so one wrapper can drive either.
const (
	exitFailure = 1 // a command failed, or a gate evaluated to NO-GO
	exitUnread  = 2 // an input could not be read or parsed
)

func main() {
	if err := execute(os.Args[1:]); err != nil {
		var coded exitError
		if errors.As(err, &coded) {
			if !coded.quiet {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
			os.Exit(coded.code)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(exitFailure)
	}
}

// exitError carries the process exit code a failure should produce. quiet
// suppresses the "error:" line for outcomes that are not errors — a NO-GO
// decision is a valid, fully reported result that simply is not a GO.
type exitError struct {
	code  int
	quiet bool
	err   error
}

func (e exitError) Error() string { return e.err.Error() }

func (e exitError) Unwrap() error { return e.err }

// unreadable marks an input that could not be opened or parsed, which exits 2
// so a caller never mistakes an unreadable file for a considered verdict.
func unreadable(err error) error {
	if err == nil {
		return nil
	}
	return exitError{code: exitUnread, err: err}
}

func execute(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "profile":
		return profileCommand(args[1:])
	case "gate":
		return gateCommand(args[1:])
	case "status":
		return statusCommand(args[1:])
	case "report":
		return reportCommand(args[1:])
	case "reconcile":
		return reconcileCommand(args[1:])
	case "plan":
		return planCommand(args[1:])
	case "version":
		return versionCommand(args[1:])
	default:
		return usageError()
	}
}

// versionCommand prints the identity of this binary as canonical JSON, so a
// plan, a run, or a gate report can bind to the exact tool that produced it.
//
// A version string alone is not evidence — it is whatever the linker was told
// to write. commit and buildDate come from the toolchain's own VCS stamp
// instead: the revision this binary was built from and that revision's
// timestamp, with "-dirty" appended when the tree had uncommitted changes. A
// binary reporting the dev sentinel, an unknown commit, or a dirty suffix did
// not come from the pinned release pipeline.
func versionCommand(args []string) error {
	if len(args) > 0 {
		return errors.New("usage: rebootstrap version")
	}
	commit, buildDate := buildStamp()
	return printCanonical(map[string]any{
		"schemaVersion": model.SchemaVersion,
		"version":       version.Version,
		"commit":        commit,
		"buildDate":     buildDate,
	})
}

// buildStamp reads the VCS stamp the Go toolchain embeds at build time. Both
// fields fall back to "unknown" rather than to an empty string: a blank commit
// reads like a missing field, while "unknown" is a fact worth recording — this
// binary cannot prove which source produced it.
func buildStamp() (commit, buildDate string) {
	commit, buildDate = "unknown", "unknown"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return commit, buildDate
	}
	dirty := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if setting.Value != "" {
				commit = setting.Value
			}
		case "vcs.time":
			if setting.Value != "" {
				buildDate = setting.Value
			}
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if dirty && commit != "unknown" {
		commit += "-dirty"
	}
	return commit, buildDate
}

const planUsage = "usage: rebootstrap plan validate|digest|render|bind|dry-run --file PLAN"

func planCommand(args []string) error {
	if len(args) == 0 {
		return errors.New(planUsage)
	}
	switch args[0] {
	case "validate":
		p, err := loadPlan(args[1:])
		if err != nil {
			return err
		}
		if err := p.Validate(); err != nil {
			return err
		}
		return printCanonical(map[string]any{"schemaVersion": 1, "status": "PASS", "planID": p.ID, "planDigest": p.PlanDigest})
	case "digest":
		p, err := loadPlan(args[1:])
		if err != nil {
			return err
		}
		digest, err := p.CanonicalDigest()
		if err != nil {
			return err
		}
		fmt.Println(digest)
		return nil
	case "render":
		return renderCommand(args[1:])
	case "bind":
		return bindCommand(args[1:])
	case "dry-run":
		return dryRunPlan(args[1:])
	default:
		return errors.New(planUsage)
	}
}

func loadPlan(args []string) (plan.Plan, error) {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "typed execution plan JSON")
	if err := flags.Parse(args); err != nil {
		return plan.Plan{}, err
	}
	if *path == "" {
		return plan.Plan{}, errors.New("--file is required")
	}
	return openPlan(*path)
}

func openPlan(path string) (plan.Plan, error) {
	file, err := os.Open(path)
	if err != nil {
		return plan.Plan{}, unreadable(fmt.Errorf("open plan: %w", err))
	}
	defer file.Close()
	p, err := plan.Decode(file)
	if err != nil {
		return plan.Plan{}, unreadable(err)
	}
	return p, nil
}

const renderUsage = "usage: rebootstrap plan render --file PLAN [--format text|runsheet] [--out FILE]"

// renderCommand renders a validated plan. Rendering is read-only in the
// strongest sense available here: the plan is decoded, validated, and turned
// into bytes, and no step's argv is ever executed.
func renderCommand(args []string) error {
	flags := flag.NewFlagSet("plan render", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "typed execution plan JSON")
	format := flags.String("format", "text", "text (flat listing) or runsheet (operator run sheet)")
	out := flags.String("out", "", "write to this file instead of stdout")
	if err := parseFlags(flags, args, renderUsage); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("--file is required")
	}
	p, err := openPlan(*path)
	if err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	var buffer bytes.Buffer
	switch *format {
	case "text":
		err = render.Text(&buffer, p)
	case "runsheet":
		err = render.Runsheet(&buffer, p, render.Meta{CLIVersion: version.Version})
	default:
		return fmt.Errorf("unknown --format %q: expected text or runsheet", *format)
	}
	if err != nil {
		return err
	}
	if *out != "" {
		return writeOutput(*out, buffer.Bytes())
	}
	_, err = os.Stdout.Write(buffer.Bytes())
	return err
}

const bindUsage = "usage: rebootstrap plan bind --file PLAN --profile-digest DIGEST --recovery-commit SHA [--checkpoint-digest DIGEST] [--out FILE]"

// bindCommand substitutes an authored plan's placeholder anchors for the real
// ones and re-derives the plan digest over the result. It is the only
// supported way to do that: a plan is digest-bound, so editing an anchor by
// hand invalidates the plan until the digest is recomputed by hand too.
//
// The input plan is decoded but deliberately not validated first — an authored
// plan carrying placeholder anchors and a stale digest is exactly the input
// this command exists to repair. The bound result is validated before it is
// written, so nothing invalid leaves here.
func bindCommand(args []string) error {
	flags := flag.NewFlagSet("plan bind", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "authored execution plan JSON")
	profileDigest := flags.String("profile-digest", "", "clean-room profile digest to bind to")
	recoveryCommit := flags.String("recovery-commit", "", "immutable recovery commit to bind to")
	checkpointDigest := flags.String("checkpoint-digest", "", "checkpoint digest, when the quiesced window has produced one")
	out := flags.String("out", "", "write the bound plan here instead of stdout")
	if err := parseFlags(flags, args, bindUsage); err != nil {
		return err
	}
	if *path == "" || *profileDigest == "" || *recoveryCommit == "" {
		return errors.New("--file, --profile-digest, and --recovery-commit are required")
	}
	p, err := openPlan(*path)
	if err != nil {
		return err
	}
	bound, err := plan.Bind(p, *profileDigest, *recoveryCommit, *checkpointDigest)
	if err != nil {
		return err
	}
	var buffer bytes.Buffer
	if err := plan.Encode(&buffer, bound); err != nil {
		return err
	}
	if *out != "" {
		return writeOutput(*out, buffer.Bytes())
	}
	_, err = os.Stdout.Write(buffer.Bytes())
	return err
}

// parseFlags parses a flag set and turns -h/--help into a printed usage and a
// clean exit. Without this, asking a subcommand for help fails the shell's &&
// chain, which is a poor way to greet someone reading the tool for the first
// time.
func parseFlags(flags *flag.FlagSet, args []string, usage string) error {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println(usage)
			fmt.Println()
			flags.SetOutput(os.Stdout)
			flags.PrintDefaults()
			return exitError{code: 0, quiet: true, err: err}
		}
		return err
	}
	return nil
}

func dryRunPlan(args []string) error {
	flags := flag.NewFlagSet("plan dry-run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "typed execution plan JSON")
	confirmationPath := flags.String("confirmation", "", "optional operator confirmation JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("--file is required")
	}
	p, err := loadPlan([]string{"--file", *path})
	if err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if *confirmationPath != "" {
		confirmation, err := loadJSON[plan.OperatorConfirmation](*confirmationPath)
		if err != nil {
			return err
		}
		if err := confirmation.Validate(p); err != nil {
			return err
		}
		fmt.Println("PASS: confirmation is bound to this plan; no commands executed")
		return nil
	}
	fmt.Println("DRY-RUN: no commands executed")
	for _, step := range p.Steps {
		if step.Destructive {
			fmt.Printf("STOP: %s requires operator confirmation (%s)\n", step.ID, step.Confirmation.Prompt)
		} else {
			fmt.Printf("WOULD RUN: %s/%s [%s]\n", step.Phase, step.ID, step.Kind)
		}
	}
	return nil
}

func profileCommand(args []string) error {
	if len(args) == 0 || args[0] != "validate" {
		return errors.New("usage: rebootstrap profile validate --profile PROFILE")
	}
	flags := flag.NewFlagSet("profile validate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	profilePath := flags.String("profile", "", "profile JSON path")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *profilePath == "" {
		return errors.New("--profile is required")
	}
	profile, err := loadJSON[model.Profile](*profilePath)
	if err != nil {
		return err
	}
	if err := model.ValidateProfile(*profile); err != nil {
		return err
	}
	digest, err := model.ProfileDigest(*profile)
	if err != nil {
		return err
	}
	return printCanonical(map[string]any{
		"schemaVersion":       model.SchemaVersion,
		"status":              "PASS",
		"profileID":           profile.ID,
		"profileDigest":       digest,
		"mandatoryPredicates": profile.MandatoryPredicates,
	})
}

func gateCommand(args []string) error {
	if len(args) == 0 || args[0] != "evaluate" {
		return errors.New("usage: rebootstrap gate evaluate --run RUN --run-id ID --profile PROFILE --input INPUT [--authorization AUTHORIZATION]")
	}
	flags := flag.NewFlagSet("gate evaluate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	runDir := flags.String("run", "", "run directory")
	runID := flags.String("run-id", "", "new run identifier; required when RUN does not exist")
	profilePath := flags.String("profile", "", "profile JSON path")
	inputPath := flags.String("input", "", "gate evidence JSON path")
	authorizationPath := flags.String("authorization", "", "explicit operator authorization JSON path")
	recoveryCommit := flags.String("recovery-commit", "", "immutable recovery commit for a new run")
	checkpointDigest := flags.String("checkpoint-digest", "", "checkpoint digest for a new run")
	planDigest := flags.String("plan-digest", "", "execution-plan digest for a new run")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *runDir == "" || *profilePath == "" || *inputPath == "" {
		return errors.New("--run, --profile, and --input are required")
	}
	profile, err := loadJSON[model.Profile](*profilePath)
	if err != nil {
		return unreadable(err)
	}
	if err := model.ValidateProfile(*profile); err != nil {
		return err
	}
	digest, err := model.ProfileDigest(*profile)
	if err != nil {
		return err
	}
	input, err := loadJSON[model.GateInput](*inputPath)
	if err != nil {
		return unreadable(err)
	}
	if err := model.ValidateGateInput(*input, *profile); err != nil {
		return err
	}
	var authorization *model.OperatorAuthorization
	if *authorizationPath != "" {
		loaded, loadErr := loadJSON[model.OperatorAuthorization](*authorizationPath)
		if loadErr != nil {
			return unreadable(loadErr)
		}
		authorization = loaded
	}
	var report model.GateReport
	err = run.WithLock(*runDir, true, func() error {
		newRun := false
		runValue, loadErr := run.LoadRun(*runDir)
		if os.IsNotExist(rootCause(loadErr)) {
			if strings.TrimSpace(*runID) == "" {
				return errors.New("--run-id is required when initializing a run")
			}
			if strings.TrimSpace(*recoveryCommit) == "" || strings.TrimSpace(*checkpointDigest) == "" || strings.TrimSpace(*planDigest) == "" {
				return errors.New("--recovery-commit, --checkpoint-digest, and --plan-digest are required when initializing a run")
			}
			runValue = model.Run{
				SchemaVersion:    model.SchemaVersion,
				RunID:            *runID,
				ProfileID:        profile.ID,
				ProfileDigest:    digest,
				RecoveryCommit:   *recoveryCommit,
				CheckpointDigest: *checkpointDigest,
				PlanDigest:       *planDigest,
				CreatedAt:        time.Now().UTC().Format(time.RFC3339Nano),
			}
			newRun = true
		} else if loadErr != nil {
			return loadErr
		}
		if runValue.ProfileID != profile.ID || runValue.ProfileDigest != digest {
			return fmt.Errorf("profile binding mismatch: run has %s/%s, input has %s/%s", runValue.ProfileID, runValue.ProfileDigest, profile.ID, digest)
		}
		if authorization != nil {
			evidenceDigest, err := model.EvidenceDigest(*input)
			if err != nil {
				return err
			}
			if err := model.ValidateAuthorization(*authorization, runValue, evidenceDigest); err != nil {
				return err
			}
		}
		if newRun {
			if err := run.CreateRunLocked(*runDir, runValue); err != nil {
				return err
			}
		}
		report = gate.Evaluate(runValue, *profile, *input, authorization)
		return run.AppendGateLocked(*runDir, runValue, report)
	})
	if err != nil {
		return err
	}
	if err := printCanonical(report); err != nil {
		return err
	}
	// The report is written first and in full, then the decision sets the
	// exit code. A NO-GO is a complete, valid result — not a failure — so it
	// gets a plain stderr line rather than an "error:" prefix, and stdout is
	// the same canonical JSON either way.
	if report.Decision != "GO" {
		fmt.Fprintf(os.Stderr, "NO-GO: run %s is %s\n", report.RunID, noGoReason(report))
		return exitError{code: exitFailure, quiet: true, err: errors.New("gate decision is NO-GO")}
	}
	return nil
}

func noGoReason(report model.GateReport) string {
	switch {
	case !report.AllMandatoryPass:
		return "not eligible: a mandatory predicate is not PASS"
	case report.OperatorAuthorization == nil:
		return "eligible, but no operator authorization was supplied"
	default:
		return "eligible, but the operator authorization is not valid for this run"
	}
}

func statusCommand(args []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	runDir := flags.String("run", "", "run directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *runDir == "" {
		return errors.New("usage: rebootstrap status --run RUN")
	}
	var status model.StatusProjection
	if err := run.WithLock(*runDir, false, func() error {
		var err error
		status, err = run.LoadStatus(*runDir)
		return err
	}); err != nil {
		return err
	}
	return printCanonical(status)
}

func reconcileCommand(args []string) error {
	flags := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	runDir := flags.String("run", "", "run directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *runDir == "" {
		return errors.New("usage: rebootstrap reconcile --run RUN")
	}
	if err := run.Reconcile(*runDir); err != nil {
		return err
	}
	return printCanonical(map[string]any{"schemaVersion": 1, "status": "PASS", "reconciled": true})
}

func reportCommand(args []string) error {
	flags := flag.NewFlagSet("report", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	runDir := flags.String("run", "", "run directory")
	output := flags.String("output", "", "optional output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *runDir == "" {
		return errors.New("usage: rebootstrap report --run RUN [--output PATH]")
	}
	var report model.GateReport
	if err := run.WithLock(*runDir, false, func() error {
		var err error
		report, err = run.LoadReport(*runDir)
		return err
	}); err != nil {
		return err
	}
	encoded, err := model.CanonicalJSON(report)
	if err != nil {
		return err
	}
	if *output != "" {
		if err := writeOutput(*output, encoded); err != nil {
			return err
		}
	}
	_, err = os.Stdout.Write(encoded)
	return err
}

func loadJSON[T any](path string) (*T, error) {
	var reader io.Reader
	if path == "-" {
		reader = os.Stdin
	} else {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		defer file.Close()
		reader = file
	}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode %s: multiple JSON values", path)
		}
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &value, nil
}

func printCanonical(value any) error {
	encoded, err := model.CanonicalJSON(value)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(encoded)
	return err
}

func writeOutput(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tmp-report-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func rootCause(err error) error {
	if err == nil {
		return nil
	}
	return errors.Unwrap(err)
}

func usageError() error {
	return errors.New("usage: rebootstrap profile validate | gate evaluate | plan validate|digest|render|bind|dry-run | status | report | reconcile | version")
}
