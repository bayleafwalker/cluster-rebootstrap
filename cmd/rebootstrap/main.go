package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bayleafwalker/cluster-rebootstrap/internal/gate"
	"github.com/bayleafwalker/cluster-rebootstrap/internal/model"
	"github.com/bayleafwalker/cluster-rebootstrap/internal/run"
)

func main() {
	if err := execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
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
	default:
		return usageError()
	}
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
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *runDir == "" || *profilePath == "" || *inputPath == "" {
		return errors.New("--run, --profile, and --input are required")
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
	input, err := loadJSON[model.GateInput](*inputPath)
	if err != nil {
		return err
	}
	if err := model.ValidateGateInput(*input, *profile); err != nil {
		return err
	}
	var authorization *model.OperatorAuthorization
	if *authorizationPath != "" {
		loaded, loadErr := loadJSON[model.OperatorAuthorization](*authorizationPath)
		if loadErr != nil {
			return loadErr
		}
		if err := model.ValidateAuthorization(*loaded); err != nil {
			return err
		}
		authorization = loaded
	}
	var report model.GateReport
	err = run.WithLock(*runDir, true, func() error {
		runValue, loadErr := run.LoadRun(*runDir)
		if os.IsNotExist(rootCause(loadErr)) {
			if strings.TrimSpace(*runID) == "" {
				return errors.New("--run-id is required when initializing a run")
			}
			runValue = model.Run{
				SchemaVersion: model.SchemaVersion,
				RunID:         *runID,
				ProfileID:     profile.ID,
				ProfileDigest: digest,
				CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
			}
			if err := run.CreateRun(*runDir, runValue); err != nil {
				return err
			}
		} else if loadErr != nil {
			return loadErr
		}
		if runValue.ProfileID != profile.ID || runValue.ProfileDigest != digest {
			return fmt.Errorf("profile binding mismatch: run has %s/%s, input has %s/%s", runValue.ProfileID, runValue.ProfileDigest, profile.ID, digest)
		}
		report = gate.Evaluate(runValue, *profile, *input, authorization)
		return run.AppendGate(*runDir, runValue, report)
	})
	if err != nil {
		return err
	}
	return printCanonical(report)
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
	return errors.New("usage: rebootstrap profile validate | gate evaluate | status | report")
}
