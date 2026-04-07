package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/helldriver666/prism/internal/config"
	"github.com/helldriver666/prism/internal/vault"
)

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	tier := fs.Int("tier", 1, "deployment tier: 1 (regex), 2 (+Ollama), 3 (+Presidio)")
	dataDir := fs.String("data-dir", config.DefaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *tier < 1 || *tier > 3 {
		return fmt.Errorf("tier must be 1, 2, or 3")
	}

	fmt.Fprintf(os.Stderr, "Initializing Prism (tier %d) in %s\n", *tier, *dataDir)

	// 1. Create data directory.
	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// 2. Write default config files.
	writeDefaults(*dataDir, *tier)

	// 3. Prompt for master password.
	password, err := readPasswordConfirmed()
	if err != nil {
		return err
	}
	defer explicitBzero(password)

	// 4. Create vault database (this derives the encryption key and stores verification record).
	vaultPath := filepath.Join(*dataDir, "secrets.db")
	vaultPw := make([]byte, len(password))
	copy(vaultPw, password)
	v, err := vault.New(vault.Config{
		DBPath:         vaultPath,
		MasterPassword: vaultPw,
	})
	if err != nil {
		return fmt.Errorf("create vault: %w", err)
	}
	v.Close()
	fmt.Fprintln(os.Stderr, "  [ok] Vault database created")

	// 5. Create cost database.
	costPath := filepath.Join(*dataDir, "cost.db")
	if err := touchDB(costPath); err != nil {
		return fmt.Errorf("create cost db: %w", err)
	}
	fmt.Fprintln(os.Stderr, "  [ok] Cost database created")

	// 6. Create audit database.
	auditPath := filepath.Join(*dataDir, "audit.db")
	if err := touchDB(auditPath); err != nil {
		return fmt.Errorf("create audit db: %w", err)
	}
	fmt.Fprintln(os.Stderr, "  [ok] Audit database created")

	// 7. Create cache database.
	cachePath := filepath.Join(*dataDir, "cache.db")
	if err := touchDB(cachePath); err != nil {
		return fmt.Errorf("create cache db: %w", err)
	}
	fmt.Fprintln(os.Stderr, "  [ok] Cache database created")

	// 8. Check tier dependencies.
	if *tier >= 2 {
		if err := checkOllama("http://localhost:11434"); err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] Ollama not available: %v\n", err)
			fmt.Fprintln(os.Stderr, "         Tier 2+ features require Ollama. Install from https://ollama.com")
		} else {
			fmt.Fprintln(os.Stderr, "  [ok] Ollama is available")
		}
	}

	if *tier >= 3 {
		fmt.Fprintln(os.Stderr, "  [info] Tier 3 requires Presidio sidecar (Python). See docs/ARCHITECTURE.md")
	}

	fmt.Fprintln(os.Stderr, "\nPrism initialized. Next steps:")
	fmt.Fprintln(os.Stderr, "  prism vault add-provider --provider claude --api-key sk-ant-...")
	fmt.Fprintln(os.Stderr, "  prism vault register --project my-app --ttl 720h")
	fmt.Fprintln(os.Stderr, "  prism start")

	return nil
}

// writeDefaults writes default config files that don't already exist.
func writeDefaults(dataDir string, tier int) {
	files := []struct {
		name    string
		content []byte
	}{
		{"config.yaml", patchTier(config.DefaultConfigYAML, tier)},
		{"routes.yaml", config.DefaultRoutesYAML},
		{"budgets.yaml", config.DefaultBudgetsYAML},
		{"privacy.yaml", config.DefaultPrivacyYAML},
	}
	for _, f := range files {
		path := filepath.Join(dataDir, f.name)
		wrote, err := config.WriteDefault(path, f.content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] Failed to write %s: %v\n", f.name, err)
			continue
		}
		if wrote {
			fmt.Fprintf(os.Stderr, "  [ok] Created %s\n", f.name)
		} else {
			fmt.Fprintf(os.Stderr, "  [skip] %s already exists\n", f.name)
		}
	}
}

// patchTier replaces the tier value in the default YAML.
func patchTier(yamlContent []byte, tier int) []byte {
	// Simple string replacement since the default always has "tier: 1".
	if tier == 1 {
		return yamlContent
	}
	old := []byte("tier: 1")
	new := []byte(fmt.Sprintf("tier: %d", tier))
	return replaceBytes(yamlContent, old, new)
}

func replaceBytes(data, old, new []byte) []byte {
	for i := 0; i <= len(data)-len(old); i++ {
		match := true
		for j := range old {
			if data[i+j] != old[j] {
				match = false
				break
			}
		}
		if match {
			result := make([]byte, 0, len(data)-len(old)+len(new))
			result = append(result, data[:i]...)
			result = append(result, new...)
			result = append(result, data[i+len(old):]...)
			return result
		}
	}
	return data
}

// touchDB creates an empty file if it doesn't exist.
func touchDB(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return f.Close()
}

// checkOllama verifies Ollama is running.
func checkOllama(baseURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot connect to Ollama at %s: %w", baseURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama returned status %d", resp.StatusCode)
	}
	return nil
}
