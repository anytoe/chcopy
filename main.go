package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/anytoe/chcopy/internal/models"
	"github.com/anytoe/chcopy/internal/repositories/clickhouse"
	"github.com/spf13/cobra"
)

func main() {
	var (
		configPath string
		name       string
		list       bool
		dryRun     bool
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
			warnIfNonLocal(local.Host)
			client, err := clickhouse.Open(local, clickhouse.ConnOptions{
				DialTimeout: cfg.Connection.DialTimeout.Std(),
				ReadTimeout: cfg.Connection.ReadTimeout.Std(),
			})
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

func warnIfNonLocal(host string) {
	if isLocal(host) {
		return
	}
	fmt.Fprintf(os.Stderr, "WARNING: %q does not look local; proceeding in 10s. Ctrl-C to abort.\n", host)
	time.Sleep(10 * time.Second)
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
