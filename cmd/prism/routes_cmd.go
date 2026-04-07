package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/helldriver666/prism/internal/config"
	"github.com/helldriver666/prism/internal/policy"
	"github.com/helldriver666/prism/internal/provider"
)

func cmdRoutes(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("routes: subcommand required (validate)")
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "validate":
		return routesValidate(subArgs)
	default:
		return fmt.Errorf("routes: unknown subcommand %q", sub)
	}
}

func routesValidate(args []string) error {
	fs := flag.NewFlagSet("routes validate", flag.ExitOnError)
	dataDir := fs.String("data-dir", config.DefaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	routesYAML, err := config.LoadRoutes(*dataDir)
	if err != nil {
		return err
	}

	// Create a registry with all known providers for validation.
	reg := provider.NewRegistry()
	reg.Register(provider.NewClaudeProvider(provider.ClaudeConfig{}))
	reg.Register(provider.NewOllamaProvider(provider.OllamaConfig{}))
	reg.Register(provider.NewOpenAIProvider(provider.OpenAIConfig{}))
	reg.Register(provider.NewGeminiProvider(provider.GeminiConfig{}))

	router, err := policy.NewRouter(routesYAML, reg)
	if err != nil {
		return fmt.Errorf("parse routes: %w", err)
	}

	errs := router.Validate()
	if len(errs) == 0 {
		fmt.Fprintln(os.Stderr, "routes.yaml: OK (no errors)")
		return nil
	}

	fmt.Fprintf(os.Stderr, "routes.yaml: %d error(s) found:\n", len(errs))
	for i, e := range errs {
		fmt.Fprintf(os.Stderr, "  %d. %v\n", i+1, e)
	}
	return fmt.Errorf("route validation failed with %d error(s)", len(errs))
}
