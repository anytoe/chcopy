package clickhouse

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/anytoe/chcopy/internal/models"
)

func RemoteExpr(source Endpoint, table string) string {
	fn := "remote"
	if source.IsSecure() {
		fn = "remoteSecure"
	}
	return fmt.Sprintf("%s(%s, %s, %s, %s)",
		fn,
		quoteString(source.Addr()),
		table,
		quoteString(source.User),
		quoteString(source.Password),
	)
}

func SourceCountSQL(source Endpoint, t models.Table) string {
	sql := "SELECT count() FROM " + RemoteExpr(source, t.Table)
	if w := strings.TrimSpace(t.Where); w != "" {
		sql += " " + w
	}
	return sql
}

func LocalCountSQL(t models.Table) string {
	return "SELECT count() FROM " + t.Table
}

func TruncateSQL(t models.Table) string {
	return "TRUNCATE TABLE " + t.Table
}

func InsertSQL(source Endpoint, t models.Table) string {
	sql := fmt.Sprintf("INSERT INTO %s SELECT * FROM %s", t.Table, RemoteExpr(source, t.Table))
	if w := strings.TrimSpace(t.Where); w != "" {
		sql += " " + w
	}
	return sql
}

// PrintDryRun writes the SQL chcopy would execute, in order, without touching any DB.
func PrintDryRun(out io.Writer, source Endpoint, ic *models.ImportConfig) {
	for _, t := range ic.Tables {
		fmt.Fprintln(out, "-- "+t.Table)
		if t.Truncate {
			fmt.Fprintln(out, TruncateSQL(t)+";")
		}
		fmt.Fprintln(out, InsertSQL(source, t)+";")
		fmt.Fprintln(out)
	}
}

// Run executes the import plan against local. Source is reached via remote() in SQL.
func (c *Client) Run(ctx context.Context, out io.Writer, source Endpoint, ic *models.ImportConfig) error {
	for _, t := range ic.Tables {
		srcCount, err := c.Count(ctx, SourceCountSQL(source, t))
		if err != nil {
			return fmt.Errorf("%s: source count: %w", t.Table, err)
		}
		before, err := c.Count(ctx, LocalCountSQL(t))
		if err != nil {
			return fmt.Errorf("%s: local count before: %w", t.Table, err)
		}
		truncated := ""
		if t.Truncate {
			if err := c.Exec(ctx, TruncateSQL(t)); err != nil {
				return fmt.Errorf("%s: truncate: %w", t.Table, err)
			}
			truncated = ", truncated"
		}
		if err := c.Exec(ctx, InsertSQL(source, t)); err != nil {
			return fmt.Errorf("%s: insert: %w", t.Table, err)
		}
		after, err := c.Count(ctx, LocalCountSQL(t))
		if err != nil {
			return fmt.Errorf("%s: local count after: %w", t.Table, err)
		}
		fmt.Fprintf(out, "%s: source=%d, local before=%d%s, local after=%d\n",
			t.Table, srcCount, before, truncated, after)
	}
	return nil
}
