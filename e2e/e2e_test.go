//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	localAddr   = "localhost:9011"
	composeFile = "../examples/docker-compose.yml"
	configFile  = "../examples/config.yaml"
)

var binaryPath string

func TestMain(m *testing.M) {
	os.Exit(setupAndRun(m))
}

func setupAndRun(m *testing.M) int {
	tmpDir, err := os.MkdirTemp("", "chcopy-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkdtemp:", err)
		return 1
	}
	defer os.RemoveAll(tmpDir)

	binaryPath = filepath.Join(tmpDir, "chcopy")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}

	build := exec.Command("go", "build", "-o", binaryPath, ".")
	build.Dir = ".."
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build chcopy:", err)
		return 1
	}

	if err := compose("up", "-d").Run(); err != nil {
		fmt.Fprintln(os.Stderr, "compose up:", err)
		return 1
	}
	defer func() {
		_ = compose("down", "-v").Run()
	}()

	if err := waitForReady(60 * time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "wait for ready:", err)
		return 1
	}

	return m.Run()
}

func compose(args ...string) *exec.Cmd {
	full := append([]string{"compose", "-f", composeFile}, args...)
	cmd := exec.Command("docker", full...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd
}

// waitForReady waits until local is reachable AND can talk to source via the
// docker network. The latter is the real precondition for any copy run.
func waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	probe := "SELECT count() FROM remote('source-clickhouse:9000', system.one, 'default', 'default_password')"
	for time.Now().Before(deadline) {
		conn, err := openLocal()
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if conn.Ping(ctx) == nil {
				var n uint64
				if err := conn.QueryRow(ctx, probe).Scan(&n); err == nil {
					cancel()
					conn.Close()
					return nil
				}
			}
			cancel()
			conn.Close()
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("not ready within %s", timeout)
}

func openLocal() (driver.Conn, error) {
	return clickhouse.Open(&clickhouse.Options{
		Addr: []string{localAddr},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: "default",
			Password: "default_password",
		},
	})
}

func TestEndToEnd(t *testing.T) {
	env := envFor(map[string]string{
		"ENV":                   "LOCAL",
		"CHCOPY_LOCAL_HOST":     "localhost",
		"CHCOPY_LOCAL_PORT":     "9011",
		"CHCOPY_LOCAL_USER":     "default",
		"CHCOPY_LOCAL_PASSWORD": "default_password",
		"CHCOPY_SOURCE_HOST":    "source-clickhouse",
		"CHCOPY_SOURCE_PORT":    "9000",
		"CHCOPY_SOURCE_USER":    "default",
		"CHCOPY_SOURCE_PASSWORD": "default_password",
	})

	t.Run("list prints configured names", func(t *testing.T) {
		out, err := runChcopy(env, "--config", configFile, "--list")
		require.NoError(t, err, out)
		assert.Contains(t, out, "dev_import")
	})

	t.Run("dry run prints SQL and does not write", func(t *testing.T) {
		truncateLocal(t)

		out, err := runChcopy(env, "--config", configFile, "--name", "dev_import", "--dry-run")
		require.NoError(t, err, out)

		assert.Contains(t, out, "INSERT INTO")
		assert.Contains(t, out, "shop.users")
		assert.Contains(t, out, "shop.orders")
		assert.Contains(t, out, "remote(")
		assert.Contains(t, out, "WHERE created_at >= '2026-05-01'")

		assert.Equal(t, uint64(0), countRows(t, "shop.users"))
		assert.Equal(t, uint64(0), countRows(t, "shop.orders"))
	})

	t.Run("copy populates local with expected slice", func(t *testing.T) {
		truncateLocal(t)

		out, err := runChcopy(env, "--config", configFile, "--name", "dev_import")
		require.NoError(t, err, out)

		assert.Equal(t, uint64(4), countRows(t, "shop.users"))
		assert.Equal(t, uint64(3), countRows(t, "shop.orders"))

		minDate := queryString(t, "SELECT toString(min(created_at)) FROM shop.orders")
		assert.Equal(t, "2026-05-03 16:45:00", minDate)
	})

	t.Run("only flag runs only the selected table", func(t *testing.T) {
		truncateLocal(t)

		out, err := runChcopy(env, "--config", configFile, "--name", "dev_import", "--only", "shop.orders")
		require.NoError(t, err, out)

		// Only shop.orders was imported; shop.users was skipped entirely.
		assert.Contains(t, out, "shop.orders")
		assert.NotContains(t, out, "shop.users")
		assert.Equal(t, uint64(3), countRows(t, "shop.orders"))
		assert.Equal(t, uint64(0), countRows(t, "shop.users"))
	})

	t.Run("only flag with unknown table errors", func(t *testing.T) {
		out, err := runChcopy(env, "--config", configFile, "--name", "dev_import", "--only", "shop.nope", "--dry-run")
		require.Error(t, err, out)
		assert.Contains(t, out, "shop.nope")
		assert.Contains(t, out, "no such table")
	})

	t.Run("batch dry run prints resolution and one templated insert", func(t *testing.T) {
		out, err := runChcopy(env, "--config", configFile, "--name", "batch_import", "--dry-run")
		require.NoError(t, err, out)

		assert.Contains(t, out, "batched by year")
		assert.Contains(t, out, "SELECT DISTINCT year FROM")
		assert.Contains(t, out, "ORDER BY year ASC")
		assert.Contains(t, out, "WHERE year >= 2024 AND year = <year>")
		// Exactly one templated INSERT, not one per batch value.
		assert.Equal(t, 1, strings.Count(out, "INSERT INTO shop.events"))
	})

	t.Run("batch copy loops over distinct values honoring where", func(t *testing.T) {
		truncateEvents(t)

		out, err := runChcopy(env, "--config", configFile, "--name", "batch_import")
		require.NoError(t, err, out)
		assert.Contains(t, out, "batches=2")

		// 2024 (2 rows) + 2025 (3 rows); 2023 excluded by the WHERE clause.
		assert.Equal(t, uint64(5), countRows(t, "shop.events"))
		assert.Equal(t, uint64(0), countRows(t, "shop.events WHERE year = 2023"))
	})

	t.Run("non-local target without --force aborts on non-TTY", func(t *testing.T) {
		nonLocalEnv := envFor(map[string]string{
			"ENV":                    "LOCAL",
			"CHCOPY_LOCAL_HOST":      "ch-prod.invalid",
			"CHCOPY_LOCAL_PORT":      "9001",
			"CHCOPY_LOCAL_USER":      "default",
			"CHCOPY_LOCAL_PASSWORD":  "default_password",
			"CHCOPY_SOURCE_HOST":     "source-clickhouse",
			"CHCOPY_SOURCE_PORT":     "9000",
			"CHCOPY_SOURCE_USER":     "default",
			"CHCOPY_SOURCE_PASSWORD": "default_password",
		})

		out, err := runChcopy(nonLocalEnv, "--config", configFile, "--name", "dev_import")
		require.Error(t, err, out)
		assert.Contains(t, out, "ch-prod.invalid")
		assert.Contains(t, out, "non-local")
		assert.Contains(t, out, "--force")
	})

	t.Run("non-local target with --force bypasses prompt", func(t *testing.T) {
		nonLocalEnv := envFor(map[string]string{
			"ENV":                    "LOCAL",
			"CHCOPY_LOCAL_HOST":      "ch-prod.invalid",
			"CHCOPY_LOCAL_PORT":      "9001",
			"CHCOPY_LOCAL_USER":      "default",
			"CHCOPY_LOCAL_PASSWORD":  "default_password",
			"CHCOPY_SOURCE_HOST":     "source-clickhouse",
			"CHCOPY_SOURCE_PORT":     "9000",
			"CHCOPY_SOURCE_USER":     "default",
			"CHCOPY_SOURCE_PASSWORD": "default_password",
		})

		// --force lets the run progress past the prompt; the connection then
		// fails because the host is unreachable. Both signals confirm the
		// flag bypassed the gate (no "not a TTY" abort, hostname is echoed).
		out, err := runChcopy(nonLocalEnv, "--config", configFile, "--name", "dev_import", "--force")
		require.Error(t, err, out)
		assert.Contains(t, out, "ch-prod.invalid")
		assert.Contains(t, out, "proceeding due to --force")
		assert.NotContains(t, out, "not a TTY")
	})
}

func envFor(overrides map[string]string) []string {
	out := make([]string, 0, len(overrides))
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

func runChcopy(env []string, args ...string) (string, error) {
	cmd := exec.Command(binaryPath, args...)
	cmd.Env = append(append([]string{}, os.Environ()...), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func truncateLocal(t *testing.T) {
	t.Helper()
	conn, err := openLocal()
	require.NoError(t, err)
	defer conn.Close()
	ctx := context.Background()
	require.NoError(t, conn.Exec(ctx, "TRUNCATE TABLE shop.users"))
	require.NoError(t, conn.Exec(ctx, "TRUNCATE TABLE shop.orders"))
}

func truncateEvents(t *testing.T) {
	t.Helper()
	conn, err := openLocal()
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.Exec(context.Background(), "TRUNCATE TABLE shop.events"))
}

func countRows(t *testing.T, table string) uint64 {
	t.Helper()
	conn, err := openLocal()
	require.NoError(t, err)
	defer conn.Close()
	var n uint64
	require.NoError(t, conn.QueryRow(context.Background(), "SELECT count() FROM "+table).Scan(&n))
	return n
}

func queryString(t *testing.T, sql string) string {
	t.Helper()
	conn, err := openLocal()
	require.NoError(t, err)
	defer conn.Close()
	var s string
	require.NoError(t, conn.QueryRow(context.Background(), sql).Scan(&s))
	return s
}

