package main

import (
	"fmt"
	"os"
)

const usage = `Prism — LocalAI Gateway

Usage:
  prism init    [--tier 1|2|3] [--data-dir PATH]
  prism start   [--data-dir PATH]
  prism vault   <subcommand>
  prism routes  validate [--data-dir PATH]
  prism audit   verify-chain [--from DURATION] [--data-dir PATH]

Vault subcommands:
  prism vault add-provider  --provider NAME --api-key KEY [--projects LIST]
  prism vault register      --project NAME [--ttl DURATION] [--providers LIST]
  prism vault revoke-token  --token TOKEN
  prism vault rotate-key    --provider NAME --api-key KEY

Options:
  --data-dir PATH    Data directory (default: ~/.prism)
  --help             Show this help
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "init":
		err = cmdInit(args)
	case "start":
		err = cmdStart(args)
	case "vault":
		err = cmdVault(args)
	case "routes":
		err = cmdRoutes(args)
	case "audit":
		err = cmdAudit(args)
	case "--help", "-h", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "prism: unknown command %q\n\n%s", cmd, usage)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "prism: %v\n", err)
		os.Exit(1)
	}
}
