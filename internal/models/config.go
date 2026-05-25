// Package models defines the YAML config, table specs, and copy plan types.
package models

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ImportConfigurations []ImportConfig `yaml:"import_configurations"`
}

type ImportConfig struct {
	Name   string  `yaml:"name"`
	Tables []Table `yaml:"tables"`
}

type Table struct {
	Table    string `yaml:"table"`
	Where    string `yaml:"where"`
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
