package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/helldriver666/prism/internal/audit"
	"github.com/helldriver666/prism/internal/cache"
	"github.com/helldriver666/prism/internal/config"
	"github.com/helldriver666/prism/internal/cost"
	"github.com/helldriver666/prism/internal/ingress"
	"github.com/helldriver666/prism/internal/policy"
	"github.com/helldriver666/prism/internal/privacy"
	"github.com/helldriver666/prism/internal/provider"
	"github.com/helldriver666/prism/internal/vault"
)

// closeable is satisfied by types with a Close method (tracker, logger, cache).
type closeable interface {
	Close() error
}

// dbExposer is satisfied by cost.Tracker implementations that expose the DB.
type dbExposer interface {
	DB() *sql.DB
}

func cmdStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	dataDir := fs.String("data-dir", config.DefaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 1. Load configuration.
	cfg, err := config.Load(*dataDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// 2. Read master password.
	password, err := readPassword("Master password: ")
	if err != nil {
		return err
	}
	defer explicitBzero(password)

	// 3. Derive audit HMAC key before vault zeroes the password.
	auditHMACKey := deriveHMACKey(password, "prism-audit-hmac")

	// 4. Derive cache encryption key.
	cacheEncKey := deriveHMACKey(password, "prism-cache-encryption")

	// 5. Open Vault.
	vaultPw := make([]byte, len(password))
	copy(vaultPw, password)
	v, err := vault.New(vault.Config{
		DBPath:         cfg.DBPath("secrets.db"),
		MasterPassword: vaultPw, // vault zeroes this internally
	})
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()
	log.Println("Vault opened")

	// 6. Create provider registry.
	reg := provider.NewRegistry()
	reg.Register(provider.NewClaudeProvider(provider.ClaudeConfig{}))
	reg.Register(provider.NewOllamaProvider(provider.OllamaConfig{
		BaseURL: cfg.Ollama.BaseURL,
	}))
	reg.Register(provider.NewOpenAIProvider(provider.OpenAIConfig{}))
	reg.Register(provider.NewGeminiProvider(provider.GeminiConfig{}))
	log.Println("Providers registered: claude, ollama, openai, gemini")

	// 7. Create privacy pipeline (tiered).
	pipeline := buildPrivacyPipeline(cfg)
	log.Printf("Privacy pipeline: tier %d, profile=%s", cfg.Tier, cfg.Privacy.DefaultProfile)

	// 8. Create cost tracker.
	costTracker, err := cost.NewTracker(cfg.DBPath("cost.db"))
	if err != nil {
		return fmt.Errorf("create cost tracker: %w", err)
	}
	if c, ok := costTracker.(closeable); ok {
		defer c.Close()
	}
	log.Println("Cost tracker started")

	// 9. Create budget checker (needs the cost DB).
	var budgetChecker policy.BudgetChecker
	if dbx, ok := costTracker.(dbExposer); ok {
		costDB := dbx.DB()
		budgetChecker = cost.NewBudgetChecker(costDB)

		// Load budget config into DB.
		if err := loadBudgets(costDB, *dataDir); err != nil {
			log.Printf("Warning: failed to load budgets: %v", err)
		}
	} else {
		return fmt.Errorf("cost tracker does not expose DB for budget checking")
	}
	log.Println("Budget checker ready")

	// 10. Create audit logger.
	auditLog, err := audit.NewLogger(cfg.DBPath("audit.db"), auditHMACKey)
	if err != nil {
		return fmt.Errorf("create audit logger: %w", err)
	}
	if c, ok := auditLog.(closeable); ok {
		defer c.Close()
	}
	log.Println("Audit logger started")

	// 11. Create semantic cache (optional, tier 2+ with Ollama).
	var semanticCache cache.SemanticCache
	if cfg.Tier >= 2 && cfg.Cache.Enabled {
		embedder := cache.NewOllamaEmbedder(cfg.Ollama.BaseURL, cfg.Ollama.EmbedModel)
		sc, err := cache.NewSemanticCache(cfg.DBPath("cache.db"), embedder, cacheEncKey, nil)
		if err != nil {
			log.Printf("Warning: semantic cache unavailable: %v", err)
		} else {
			semanticCache = sc
			if c, ok := sc.(closeable); ok {
				defer c.Close()
			}
			log.Println("Semantic cache enabled")
		}
	} else {
		log.Println("Semantic cache disabled (tier < 2 or cache.enabled=false)")
	}

	// 12. Load routes and create router.
	routesYAML, err := config.LoadRoutes(*dataDir)
	if err != nil {
		return fmt.Errorf("load routes: %w", err)
	}
	router, err := policy.NewRouter(routesYAML, reg)
	if err != nil {
		return fmt.Errorf("create router: %w", err)
	}

	// Validate routes at startup.
	if errs := router.Validate(); len(errs) > 0 {
		for _, e := range errs {
			log.Printf("Route validation warning: %v", e)
		}
	}
	log.Println("Router loaded and validated")

	// 13. Create policy engine.
	engine := policy.NewEngine(policy.Deps{
		Privacy:     pipeline,
		Router:      router,
		BudgetCheck: budgetChecker,
		Providers:   reg,
		CostTracker: costTracker,
		AuditLog:    auditLog,
		Cache:       semanticCache,
		Failover:    policy.NewFailover(),
	})
	log.Println("Policy engine ready")

	// 14. Create ingress handler.
	ingressHandler := ingress.NewHandler(ingress.Config{
		Validator:   v,
		RateLimiter: ingress.NewRateLimiter(cfg.RateLimitPerMinute),
	})

	// 15. Build HTTP server.
	mux := buildMux(engine, ingressHandler, costTracker, auditLog)

	srv := &http.Server{
		Handler: mux,
	}

	// 16. Listen on loopback only.
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddr, err)
	}

	// 17. Graceful shutdown.
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ln)
	}()

	log.Printf("Prism listening on %s (tier %d)", cfg.ListenAddr, cfg.Tier)

	// Wait for signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("Received %v, shutting down...", sig)
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	// Flush cost tracker buffer.
	if err := costTracker.Flush(ctx); err != nil {
		log.Printf("Cost tracker flush error: %v", err)
	}

	log.Println("Prism stopped")
	return nil
}

// buildPrivacyPipeline creates the privacy pipeline based on the configured tier.
func buildPrivacyPipeline(cfg *config.Config) privacy.Pipeline {
	var detector privacy.Detector

	switch cfg.Tier {
	case 1:
		detector = privacy.NewRegexDetector()
	case 2:
		ollamaDetector := privacy.NewOllamaDetector(cfg.Ollama.BaseURL, cfg.Ollama.NERModel)
		detector = privacy.NewCompositeDetector(privacy.NewRegexDetector(), ollamaDetector)
	case 3:
		ollamaDetector := privacy.NewOllamaDetector(cfg.Ollama.BaseURL, cfg.Ollama.NERModel)
		presidioDetector, err := privacy.NewPresidioDetector(4)
		if err != nil {
			log.Printf("Warning: Presidio unavailable, falling back to tier 2: %v", err)
			detector = privacy.NewCompositeDetector(privacy.NewRegexDetector(), ollamaDetector)
		} else {
			detector = privacy.NewCompositeDetector(privacy.NewRegexDetector(), ollamaDetector, presidioDetector)
		}
	default:
		detector = privacy.NewRegexDetector()
	}

	return privacy.NewPipeline(detector)
}

// deriveHMACKey derives a key using HMAC-SHA256 for domain separation.
func deriveHMACKey(password []byte, domain string) []byte {
	mac := hmac.New(sha256.New, password)
	mac.Write([]byte(domain))
	return mac.Sum(nil)
}

// loadBudgets reads budgets.yaml and inserts/updates entries in the cost database.
func loadBudgets(db *sql.DB, dataDir string) error {
	bc, err := config.LoadBudgets(dataDir)
	if err != nil {
		return err
	}

	for _, b := range bc.Budgets {
		action := b.Action
		if action == "" {
			action = "block"
		}
		period := b.Period
		if period == "" {
			period = "monthly"
		}

		// Upsert: delete existing matching budget, then insert.
		_, _ = db.Exec(
			`DELETE FROM budgets WHERE level = ? AND COALESCE(project_id,'') = ? AND COALESCE(provider_id,'') = ?`,
			b.Level, b.ProjectID, b.ProviderID)

		_, err := db.Exec(
			`INSERT INTO budgets (level, project_id, provider_id, limit_usd, period, action) VALUES (?, ?, ?, ?, ?, ?)`,
			b.Level, nilIfEmpty(b.ProjectID), nilIfEmpty(b.ProviderID), b.LimitUSD, period, action)
		if err != nil {
			return fmt.Errorf("insert budget %s: %w", b.Level, err)
		}
	}
	return nil
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
