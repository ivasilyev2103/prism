package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/helldriver666/prism/internal/audit"
	"github.com/helldriver666/prism/internal/config"
)

func cmdAudit(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("audit: subcommand required (verify-chain)")
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "verify-chain":
		return auditVerifyChain(subArgs)
	default:
		return fmt.Errorf("audit: unknown subcommand %q", sub)
	}
}

func auditVerifyChain(args []string) error {
	fs := flag.NewFlagSet("audit verify-chain", flag.ExitOnError)
	from := fs.String("from", "24h", "time range (e.g., 24h, 168h, 720h)")
	dataDir := fs.String("data-dir", config.DefaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	duration, err := time.ParseDuration(*from)
	if err != nil {
		return fmt.Errorf("invalid --from duration %q: %w", *from, err)
	}

	// Read master password to derive audit HMAC key.
	password, err := readPassword("Master password: ")
	if err != nil {
		return err
	}
	defer explicitBzero(password)

	auditHMACKey := deriveHMACKey(password, "prism-audit-hmac")

	cfg := &config.Config{DataDir: *dataDir}
	cfg.Defaults()

	auditLog, err := audit.NewLogger(cfg.DBPath("audit.db"), auditHMACKey)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	if c, ok := auditLog.(closeable); ok {
		defer c.Close()
	}

	now := time.Now()
	fromTime := now.Add(-duration)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := auditLog.VerifyChain(ctx, fromTime, now); err != nil {
		fmt.Fprintf(os.Stderr, "FAILED: %v\n", err)
		return fmt.Errorf("HMAC chain verification failed")
	}

	fmt.Fprintf(os.Stderr, "OK: HMAC chain verified for the last %s\n", *from)
	return nil
}
