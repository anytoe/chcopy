// Package clickhouse wraps the ClickHouse client and implements the per-table copy.
package clickhouse

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

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

func Open(e Endpoint) (*Client, error) {
	opts := &clickhouse.Options{
		Addr: []string{e.Addr()},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: e.User,
			Password: e.Password,
		},
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

// quoteString wraps s in single quotes for a SQL literal, doubling any embedded quotes.
func quoteString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
