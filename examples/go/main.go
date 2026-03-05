package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"

	"phenix/store"
	"phenix/types"
)

const minArgs = 2

func main() {
	logger := setupLogging()

	// 2. Panic Recovery
	// Ensure panics are logged as structured JSON with tracebacks instead of
	// printing raw text to Stderr, which would break the log parser.
	defer func() {
		if r := recover(); r != nil {
			logger.Error("application panicked", "error", r, "traceback", string(debug.Stack()))
			os.Exit(1)
		}
	}()
	stage := parseArgs(logger)
	input := readInput(logger)
	exp := parseConfig(input, logger)

	executeStage(stage, exp, len(input), logger)

	// 7. Output
	// Phenix expects the (potentially modified) experiment JSON to be printed to Stdout.
	// To modify the JSON without losing data (since our Experiment struct is partial),
	// we use a generic map approach.
	output, err := addAnnotation(input)
	if err != nil {
		logger.Error("failed to modify output JSON", "error", err)
		os.Exit(1) //nolint:gocritic // exitAfterDefer
	}

	if _, err := fmt.Fprint(os.Stdout, string(output)); err != nil {
		logger.Error("failed to write output", "error", err)
		os.Exit(1)
	}
}

func setupLogging() *slog.Logger {
	// 1. Setup Logging
	// Phenix expects logs to be JSON formatted and written to Stderr.
	// We use slog.NewJSONHandler for this.
	var level = slog.LevelInfo
	if env := os.Getenv("PHENIX_LOG_LEVEL"); env != "" {
		var l slog.Level
		if err := l.UnmarshalText([]byte(env)); err == nil {
			level = l
		}
	}

	var output io.Writer = os.Stderr
	if path := os.Getenv("PHENIX_LOG_FILE"); path != "" && path != "stderr" {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open log file %s: %v\n", path, err)
		} else {
			output = f
		}
	}

	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{ //nolint:exhaustruct // partial initialization
		Level: level,
	}))
}

func parseArgs(logger *slog.Logger) string {
	// 3. Parse Arguments
	// Phenix apps are called with the stage name as the first argument.
	if len(os.Args) < minArgs {
		logger.Error("stage argument required")
		os.Exit(1)
	}
	return os.Args[1]
}

func readInput(logger *slog.Logger) []byte {
	// 4. Read Input
	// Phenix passes the experiment configuration via Stdin.
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		logger.Error("failed to read stdin", "error", err)
		os.Exit(1)
	}
	return input
}

func parseConfig(input []byte, logger *slog.Logger) *types.Experiment {
	// 5. Parse Input
	// We unmarshal the JSON input into a struct to access configuration details.
	var cfg store.Config
	if err := json.Unmarshal(input, &cfg); err != nil {
		logger.Error("failed to parse input JSON", "error", err)
		os.Exit(1)
	}

	exp, err := types.DecodeExperimentFromConfig(cfg)
	if err != nil {
		logger.Error("failed to decode experiment", "error", err)
		os.Exit(1)
	}

	if exp == nil || exp.Spec == nil {
		logger.Error("decoded experiment is nil or has no spec")
		os.Exit(1)
	}
	return exp
}

func executeStage(stage string, exp *types.Experiment, inputLen int, logger *slog.Logger) {
	// 6. Execute Stage
	logger.Info(
		"executing stage",
		"stage",
		stage,
		"app",
		"example-go",
		"experiment",
		exp.Spec.ExperimentName(),
	)

	switch stage {
	case "configure":
		logger.Info("configuring application", "config_size", inputLen)
	case "pre-start":
		logger.Info("executing pre-start checks")
	case "post-start":
		logger.Info("executing post-start tasks")
	case "running":
		logger.Info("running application logic")
		// Example of structured logging with extra fields
		logger.Debug("detailed debug info",
			"iteration", 1,
			"status", "active",
		)
	case "cleanup":
		logger.Info("cleaning up resources")
	case "panic":
		// Used for testing panic recovery
		panic("simulated panic")
	default:
		logger.Warn("unknown stage received", "stage", stage)
	}
}

func addAnnotation(input []byte) ([]byte, error) {
	// We use a generic map here to preserve the entire JSON structure.
	// If we unmarshaled into the Experiment struct and marshaled back,
	// we might lose fields that aren't defined in the struct.
	var generic map[string]any
	if err := json.Unmarshal(input, &generic); err != nil {
		return nil, fmt.Errorf("failed to parse generic JSON: %w", err)
	}

	// Ensure metadata exists and add an annotation
	if generic["metadata"] == nil {
		generic["metadata"] = make(map[string]any)
	}

	if metadata, ok := generic["metadata"].(map[string]any); ok {
		if metadata["annotations"] == nil {
			metadata["annotations"] = make(map[string]any)
		}

		if annotations, ok := metadata["annotations"].(map[string]any); ok {
			annotations["example-go-processed"] = "true"
		}
	}

	return json.Marshal(generic)
}
