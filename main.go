package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/anytoe/chcopy/internal/models"
	"github.com/anytoe/chcopy/internal/repositories/clickhouse"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func main() {
	var (
		configPath string
		name       string
		list       bool
		dryRun     bool
		force      bool
	)

	root := &cobra.Command{
		Use:           "chcopy",
		Short:         "Copy curated ClickHouse data slices into a local instance.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if configPath == "" {
				return fmt.Errorf("--config is required")
			}
			cfg, err := models.Load(configPath)
			if err != nil {
				return err
			}
			if list {
				for _, n := range cfg.Names() {
					fmt.Println(n)
				}
				return nil
			}
			if name == "" {
				return fmt.Errorf("--name is required unless --list is set")
			}
			ic, ok := cfg.Find(name)
			if !ok {
				return fmt.Errorf("no import configuration named %q", name)
			}
			local, err := endpointFromEnv("CHCOPY_LOCAL")
			if err != nil {
				return err
			}
			source, err := endpointFromEnv("CHCOPY_SOURCE")
			if err != nil {
				return err
			}
			if dryRun {
				clickhouse.PrintDryRun(os.Stdout, source, ic)
				return nil
			}
			if err := confirmNonLocal(local.Host, force, os.Stdin, os.Stderr); err != nil {
				return err
			}
			client, err := clickhouse.Open(local)
			if err != nil {
				return err
			}
			defer client.Close()
			ctx := context.Background()
			if err := client.Ping(ctx); err != nil {
				return fmt.Errorf("ping local: %w", err)
			}
			return client.Run(ctx, os.Stdout, source, ic)
		},
	}

	root.Flags().StringVar(&configPath, "config", "", "YAML config file (required)")
	root.Flags().StringVar(&name, "name", "", "Named configuration to run")
	root.Flags().BoolVar(&list, "list", false, "Print available config names and exit")
	root.Flags().BoolVar(&dryRun, "dry-run", false, "Print SQL without executing")
	root.Flags().BoolVar(&force, "force", false, "Skip non-local target confirmation prompt (required for non-TTY use)")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func endpointFromEnv(prefix string) (clickhouse.Endpoint, error) {
	e := clickhouse.Endpoint{
		Host:     os.Getenv(prefix + "_HOST"),
		Port:     os.Getenv(prefix + "_PORT"),
		User:     os.Getenv(prefix + "_USER"),
		Password: os.Getenv(prefix + "_PASSWORD"),
	}
	var missing []string
	if e.Host == "" {
		missing = append(missing, prefix+"_HOST")
	}
	if e.Port == "" {
		missing = append(missing, prefix+"_PORT")
	}
	if e.User == "" {
		missing = append(missing, prefix+"_USER")
	}
	if e.Password == "" {
		missing = append(missing, prefix+"_PASSWORD")
	}
	if len(missing) > 0 {
		return e, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return e, nil
}

func confirmNonLocal(host string, force bool, stdin *os.File, out io.Writer) error {
	if isLocal(host) {
		return nil
	}
	if force {
		fmt.Fprintf(out, "Target ClickHouse %q does not look local; proceeding due to --force.\n", host)
		return nil
	}
	if !isTerminal(stdin) {
		return fmt.Errorf("target ClickHouse %q is non-local and stdin is not a TTY; pass --force to confirm non-interactively", host)
	}
	fmt.Fprintf(out, "WARNING: target ClickHouse %q does not look local.\n", host)
	fmt.Fprint(out, "You are about to write to this server. Type 'yes' to proceed: ")
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil {
		return fmt.Errorf("aborted: failed to read confirmation for target %q: %w", host, err)
	}
	if strings.TrimSpace(line) != "yes" {
		return fmt.Errorf("aborted: confirmation for non-local target %q was not 'yes'", host)
	}
	return nil
}

func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func isLocal(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() {
			return true
		}
	}
	return false
}
