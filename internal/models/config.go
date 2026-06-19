// Package models defines the YAML config, table specs, and copy plan types.
package models

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Connection           Connection     `yaml:"connection"`
	ImportConfigurations []ImportConfig `yaml:"import_configurations"`
}

// Connection holds optional Go-client tunables for the local ClickHouse connection.
// Zero values mean "use the clickhouse-go default".
type Connection struct {
	DialTimeout Duration `yaml:"dial_timeout"`
	ReadTimeout Duration `yaml:"read_timeout"`
}

// Duration is a time.Duration that unmarshals from a YAML string like "30s" or "5m".
type Duration time.Duration

func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string like \"30s\": %w", err)
	}
	p, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(p)
	return nil
}

type ImportConfig struct {
	Name   string  `yaml:"name"`
	Tables []Table `yaml:"tables"`
}

type Table struct {
	Table    string `yaml:"table"`
	Where    string `yaml:"where"`
	Batch    string `yaml:"batch"`
	Truncate bool   `yaml:"truncate"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	if c.Connection.DialTimeout < 0 {
		return fmt.Errorf("connection.dial_timeout must be non-negative")
	}
	if c.Connection.ReadTimeout < 0 {
		return fmt.Errorf("connection.read_timeout must be non-negative")
	}
	if len(c.ImportConfigurations) == 0 {
		return fmt.Errorf("no import_configurations defined")
	}
	seen := map[string]bool{}
	for i, ic := range c.ImportConfigurations {
		if ic.Name == "" {
			return fmt.Errorf("import_configurations[%d]: name is required", i)
		}
		if seen[ic.Name] {
			return fmt.Errorf("duplicate import configuration name: %q", ic.Name)
		}
		seen[ic.Name] = true
		if len(ic.Tables) == 0 {
			return fmt.Errorf("%s: at least one table is required", ic.Name)
		}
		for j, t := range ic.Tables {
			parts := strings.Split(t.Table, ".")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("%s.tables[%d]: table must be db.table, got %q", ic.Name, j, t.Table)
			}
			w := strings.TrimSpace(t.Where)
			if w != "" && !strings.HasPrefix(strings.ToUpper(w), "WHERE") {
				return fmt.Errorf("%s.tables[%d]: where must start with WHERE, got %q", ic.Name, j, t.Where)
			}
			if b := strings.TrimSpace(t.Batch); b != "" && strings.ContainsAny(b, " \t") {
				return fmt.Errorf("%s.tables[%d]: batch must be a single column name, got %q", ic.Name, j, t.Batch)
			}
		}
	}
	return nil
}

func (c *Config) Names() []string {
	out := make([]string, len(c.ImportConfigurations))
	for i, ic := range c.ImportConfigurations {
		out[i] = ic.Name
	}
	return out
}

func (c *Config) Find(name string) (*ImportConfig, bool) {
	for i := range c.ImportConfigurations {
		if c.ImportConfigurations[i].Name == name {
			return &c.ImportConfigurations[i], true
		}
	}
	return nil, false
}
