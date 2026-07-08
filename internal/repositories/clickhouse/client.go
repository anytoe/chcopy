// Package clickhouse wraps the ClickHouse client and implements the per-table copy.
package clickhouse

import (
	"context"
	"crypto/tls"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ConnOptions are optional tunables for the Go client. Zero values fall back to
// the clickhouse-go defaults.
type ConnOptions struct {
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	MaxOpenConns int
}

type Endpoint struct {
	Host, Port, User, Password string
}

func (e Endpoint) Addr() string {
	return e.Host + ":" + e.Port
}

// IsSecure returns true when the port is ClickHouse's TLS native port (9440).
// 9000 (plain) and 9440 (TLS) are the only conventional native-protocol ports.
func (e Endpoint) IsSecure() bool {
	return e.Port == "9440"
}

type Client struct {
	conn driver.Conn
}

func Open(e Endpoint, co ConnOptions) (*Client, error) {
	opts := &clickhouse.Options{
		Addr: []string{e.Addr()},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: e.User,
			Password: e.Password,
		},
	}
	if co.DialTimeout > 0 {
		opts.DialTimeout = co.DialTimeout
	}
	if co.ReadTimeout > 0 {
		opts.ReadTimeout = co.ReadTimeout
	}
	if co.MaxOpenConns > 0 {
		opts.MaxOpenConns = co.MaxOpenConns
	}
	if e.IsSecure() {
		opts.TLS = &tls.Config{}
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open clickhouse %s: %w", e.Addr(), err)
	}
	return &Client{conn: conn}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) Ping(ctx context.Context) error { return c.conn.Ping(ctx) }

func (c *Client) Exec(ctx context.Context, sql string) error {
	return c.conn.Exec(ctx, sql)
}

func (c *Client) Count(ctx context.Context, sql string) (uint64, error) {
	var n uint64
	if err := c.conn.QueryRow(ctx, sql).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Values runs a single-column query and returns each row's value in native Go
// type. The destination is built from the column's ScanType because the driver
// does not support scanning into *interface{}.
func (c *Client) Values(ctx context.Context, sql string) ([]any, error) {
	rows, err := c.conn.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cts := rows.ColumnTypes()
	if len(cts) != 1 {
		return nil, fmt.Errorf("expected 1 column, got %d", len(cts))
	}
	scanType := cts[0].ScanType()
	var out []any
	for rows.Next() {
		dst := reflect.New(scanType)
		if err := rows.Scan(dst.Interface()); err != nil {
			return nil, err
		}
		out = append(out, dst.Elem().Interface())
	}
	return out, rows.Err()
}

// quoteString wraps s in single quotes for a SQL literal, doubling any embedded quotes.
func quoteString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
