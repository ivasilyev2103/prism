package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/helldriver666/prism/internal/config"
	"github.com/helldriver666/prism/internal/types"
	"github.com/helldriver666/prism/internal/vault"
)

func cmdVault(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("vault: subcommand required (add-provider, register, revoke-token, rotate-key)")
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "add-provider":
		return vaultAddProvider(subArgs)
	case "register":
		return vaultRegister(subArgs)
	case "revoke-token":
		return vaultRevokeToken(subArgs)
	case "rotate-key":
		return vaultRotateKey(subArgs)
	default:
		return fmt.Errorf("vault: unknown subcommand %q", sub)
	}
}

func openVaultForCommand(dataDir string) (vault.Vault, error) {
	password, err := readPassword("Master password: ")
	if err != nil {
		return nil, err
	}

	v, err := vault.New(vault.Config{
		DBPath:         config.Config{DataDir: dataDir}.DBPath("secrets.db"),
		MasterPassword: password, // vault zeroes this
	})
	if err != nil {
		return nil, fmt.Errorf("open vault: %w", err)
	}
	return v, nil
}

func vaultAddProvider(args []string) error {
	fs := flag.NewFlagSet("vault add-provider", flag.ExitOnError)
	providerID := fs.String("provider", "", "provider name (claude, openai, gemini, ollama)")
	apiKey := fs.String("api-key", "", "API key")
	projects := fs.String("projects", "*", "comma-separated project IDs (* = all)")
	dataDir := fs.String("data-dir", config.DefaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *providerID == "" || *apiKey == "" {
		return fmt.Errorf("--provider and --api-key are required")
	}

	v, err := openVaultForCommand(*dataDir)
	if err != nil {
		return err
	}
	defer v.Close()

	allowedProjects := strings.Split(*projects, ",")
	for i := range allowedProjects {
		allowedProjects[i] = strings.TrimSpace(allowedProjects[i])
	}

	if err := v.AddProvider(types.ProviderID(*providerID), *apiKey, allowedProjects); err != nil {
		return fmt.Errorf("add provider: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Provider %q added (projects: %s)\n", *providerID, *projects)
	return nil
}

func vaultRegister(args []string) error {
	fs := flag.NewFlagSet("vault register", flag.ExitOnError)
	project := fs.String("project", "", "project ID")
	ttl := fs.String("ttl", "0", "token TTL (e.g., 720h, 0 = permanent)")
	providers := fs.String("providers", "*", "comma-separated provider IDs (* = all)")
	dataDir := fs.String("data-dir", config.DefaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *project == "" {
		return fmt.Errorf("--project is required")
	}

	v, err := openVaultForCommand(*dataDir)
	if err != nil {
		return err
	}
	defer v.Close()

	var tokenTTL time.Duration
	if *ttl != "0" {
		tokenTTL, err = time.ParseDuration(*ttl)
		if err != nil {
			return fmt.Errorf("invalid TTL %q: %w", *ttl, err)
		}
	}

	providerList := strings.Split(*providers, ",")
	allowedProviders := make([]types.ProviderID, 0, len(providerList))
	for _, p := range providerList {
		p = strings.TrimSpace(p)
		if p != "" && p != "*" {
			allowedProviders = append(allowedProviders, types.ProviderID(p))
		}
	}

	token, err := v.RegisterProject(*project, allowedProviders, tokenTTL)
	if err != nil {
		return fmt.Errorf("register project: %w", err)
	}

	// Print token to stdout for scripting.
	fmt.Println(token)
	fmt.Fprintf(os.Stderr, "Project %q registered (TTL: %s)\n", *project, *ttl)
	return nil
}

func vaultRevokeToken(args []string) error {
	fs := flag.NewFlagSet("vault revoke-token", flag.ExitOnError)
	token := fs.String("token", "", "token to revoke")
	dataDir := fs.String("data-dir", config.DefaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *token == "" {
		return fmt.Errorf("--token is required")
	}

	v, err := openVaultForCommand(*dataDir)
	if err != nil {
		return err
	}
	defer v.Close()

	if err := v.RevokeToken(*token); err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Token revoked")
	return nil
}

func vaultRotateKey(args []string) error {
	fs := flag.NewFlagSet("vault rotate-key", flag.ExitOnError)
	providerID := fs.String("provider", "", "provider name")
	apiKey := fs.String("api-key", "", "new API key")
	dataDir := fs.String("data-dir", config.DefaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *providerID == "" || *apiKey == "" {
		return fmt.Errorf("--provider and --api-key are required")
	}

	v, err := openVaultForCommand(*dataDir)
	if err != nil {
		return err
	}
	defer v.Close()

	if err := v.RotateProviderKey(types.ProviderID(*providerID), *apiKey); err != nil {
		return fmt.Errorf("rotate key: %w", err)
	}

	fmt.Fprintf(os.Stderr, "API key rotated for provider %q\n", *providerID)
	return nil
}
