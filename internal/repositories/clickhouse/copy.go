package clickhouse

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anytoe/chcopy/internal/models"
	"golang.org/x/sync/errgroup"
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
	return "TRUNCATE TABLE " + t.Table + " SETTINGS max_table_size_to_drop = 0"
}

func InsertSQL(source Endpoint, t models.Table) string {
	sql := fmt.Sprintf("INSERT INTO %s SELECT * FROM %s", t.Table, RemoteExpr(source, t.Table))
	if w := strings.TrimSpace(t.Where); w != "" {
		sql += " " + w
	}
	return sql
}

// DistinctBatchesSQL lists the distinct batch-column values on source, honoring
// the table's WHERE clause, ordered ascending.
func DistinctBatchesSQL(source Endpoint, t models.Table) string {
	sql := fmt.Sprintf("SELECT DISTINCT %s FROM %s", t.Batch, RemoteExpr(source, t.Table))
	if w := strings.TrimSpace(t.Where); w != "" {
		sql += " " + w
	}
	sql += fmt.Sprintf(" ORDER BY %s ASC", t.Batch)
	return sql
}

// BatchInsertSQL is InsertSQL narrowed to a single batch value. The value must
// already be a SQL literal (see formatValue).
func BatchInsertSQL(source Endpoint, t models.Table, value string) string {
	sql := fmt.Sprintf("INSERT INTO %s SELECT * FROM %s", t.Table, RemoteExpr(source, t.Table))
	cond := fmt.Sprintf("%s = %s", t.Batch, value)
	if w := strings.TrimSpace(t.Where); w != "" {
		sql += " " + w + " AND " + cond
	} else {
		sql += " WHERE " + cond
	}
	return sql
}

// formatValue renders a native Go value (as scanned from ClickHouse) as a SQL
// literal: strings and times are quoted, numbers and bools are emitted bare.
func formatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case string:
		return quoteString(x)
	case time.Time:
		return quoteString(x.Format("2006-01-02 15:04:05"))
	default:
		return fmt.Sprintf("%v", x)
	}
}

// PrintDryRun writes the SQL chcopy would execute, in order, without touching any DB.
// For batched tables it prints the batch-resolution query plus a single templated
// INSERT (the real per-batch values are only known at run time).
func PrintDryRun(out io.Writer, source Endpoint, ic *models.ImportConfig) {
	for _, t := range ic.Tables {
		if b := strings.TrimSpace(t.Batch); b != "" {
			fmt.Fprintf(out, "-- %s  (batched by %s)\n", t.Table, b)
			if t.Truncate {
				fmt.Fprintln(out, TruncateSQL(t)+";")
			}
			fmt.Fprintln(out, "-- resolve batches:")
			fmt.Fprintln(out, DistinctBatchesSQL(source, t)+";")
			fmt.Fprintln(out, "-- then one INSERT per batch value, e.g. first batch:")
			fmt.Fprintln(out, BatchInsertSQL(source, t, "<"+b+">")+";")
			fmt.Fprintln(out)
			continue
		}
		fmt.Fprintln(out, "-- "+t.Table)
		if t.Truncate {
			fmt.Fprintln(out, TruncateSQL(t)+";")
		}
		fmt.Fprintln(out, InsertSQL(source, t)+";")
		fmt.Fprintln(out)
	}
}

// Run executes the import plan against local. Source is reached via remote() in SQL.
// numThreads is the per-table batch concurrency (see runBatched); values < 1 mean
// sequential. The outer per-table loop stays sequential so the
// count-before -> truncate -> insert -> count-after ordering is preserved.
func (c *Client) Run(ctx context.Context, out io.Writer, source Endpoint, ic *models.ImportConfig, numThreads int) error {
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
		batched := ""
		if strings.TrimSpace(t.Batch) != "" {
			n, err := c.runBatched(ctx, out, source, t, numThreads)
			if err != nil {
				return err
			}
			batched = fmt.Sprintf(", batches=%d", n)
		} else if err := c.Exec(ctx, InsertSQL(source, t)); err != nil {
			return fmt.Errorf("%s: insert: %w", t.Table, err)
		}
		after, err := c.Count(ctx, LocalCountSQL(t))
		if err != nil {
			return fmt.Errorf("%s: local count after: %w", t.Table, err)
		}
		fmt.Fprintf(out, "%s: source=%d, local before=%d%s%s, local after=%d\n",
			t.Table, srcCount, before, truncated, batched, after)
	}
	return nil
}

// runBatched resolves the distinct batch values on source (ascending) and runs
// one INSERT SELECT per value. Up to numThreads inserts run concurrently
// (numThreads < 1 means sequential). The first failing batch cancels the
// remaining in-flight and queued batches and its error is returned. It returns
// the number of batches processed.
func (c *Client) runBatched(ctx context.Context, out io.Writer, source Endpoint, t models.Table, numThreads int) (int, error) {
	values, err := c.Values(ctx, DistinctBatchesSQL(source, t))
	if err != nil {
		return 0, fmt.Errorf("%s: resolve batches: %w", t.Table, err)
	}
	total := len(values)

	workers := numThreads
	if workers < 1 {
		workers = 1
	}
	if workers > total {
		workers = total // no point starting more threads than there are batches
	}

	var (
		mu   sync.Mutex // serialises writes to out so progress lines never interleave
		done atomic.Int64
	)
	jobs := make(chan any)
	g, gctx := errgroup.WithContext(ctx)
	for w := 1; w <= workers; w++ {
		g.Go(func() error { // w is the stable worker id, shown in progress output
			for v := range jobs {
				if err := c.Exec(gctx, BatchInsertSQL(source, t, formatValue(v))); err != nil {
					return fmt.Errorf("%s: insert batch %v: %w", t.Table, v, err)
				}
				n := done.Add(1)
				mu.Lock()
				if workers > 1 {
					fmt.Fprintf(out, "%s: [thread %d] batch %d/%d (%s=%s) done\n", t.Table, w, n, total, t.Batch, formatValue(v))
				} else {
					fmt.Fprintf(out, "%s: batch %d/%d (%s=%s) done\n", t.Table, n, total, t.Batch, formatValue(v))
				}
				mu.Unlock()
			}
			return nil
		})
	}
	// Feed batch values to the workers; stop early once a worker has failed
	// (errgroup cancels gctx), then close so idle workers drain and exit.
feed:
	for _, v := range values {
		select {
		case jobs <- v:
		case <-gctx.Done():
			break feed
		}
	}
	close(jobs)
	if err := g.Wait(); err != nil {
		return 0, err
	}
	return total, nil
}
