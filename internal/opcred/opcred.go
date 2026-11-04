// Package opcred fetches MySQL connection details from a 1Password item via
// the op CLI. Values live only in memory; nothing is written to the
// environment, disk, or logs.
package opcred

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/dexical30/mysql-mcp-1password/internal/config"
)

type Credentials struct {
	Host     string
	Port     int
	User     string
	Password []byte
	Database string
}

// Wipe zeroes the password bytes and clears every field.
func (c *Credentials) Wipe() {
	clear(c.Password)
	*c = Credentials{}
}

type runner func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)

// Source reads one 1Password item with a verified op binary.
type Source struct {
	// OpPath is the configured path (MYSQL_OP_PATH, or "" for the default
	// locations). It is resolved and its signature checked before every
	// call, so a binary swapped or upgraded after startup is caught.
	OpPath, Vault, Item string
	Fields              config.FieldMap
	// Timeout bounds the op call, which waits for the Touch ID prompt.
	Timeout time.Duration
	run     runner
	resolve func(string) (string, error)
}

func NewSource(opPath, vault, item string, f config.FieldMap) *Source {
	return &Source{OpPath: opPath, Vault: vault, Item: item, Fields: f, Timeout: 60 * time.Second, run: execRun, resolve: ResolveOpPath}
}

func (s *Source) Fetch(ctx context.Context) (*Credentials, error) {
	opPath, err := s.resolve(s.OpPath)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	stdout, stderr, err := s.run(ctx, opPath, "item", "get", s.Item, "--vault", s.Vault, "--format", "json")
	defer clear(stdout)
	if err != nil {
		// stderr carries op's own diagnostics, never item values; stdout is
		// deliberately left out.
		detail := strings.TrimSpace(string(stderr))
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("1Password lookup failed: %s", detail)
	}
	return parseItem(stdout, s.Fields)
}

func execRun(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// stdin/stdout belong to the MCP protocol; op must not touch them.
	cmd.Stdin = nil
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	// Without this, a child process that inherits the pipes would keep Run
	// waiting after op exits or the timeout fires, wedging every unlock.
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}

type item struct {
	Fields []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Value string `json:"value"`
	} `json:"fields"`
}

func parseItem(data []byte, f config.FieldMap) (*Credentials, error) {
	var it item
	if err := json.Unmarshal(data, &it); err != nil {
		return nil, fmt.Errorf("1Password item is not valid JSON")
	}
	find := func(names []string) string {
		for _, n := range names {
			for _, fl := range it.Fields {
				if strings.EqualFold(fl.ID, n) || strings.EqualFold(fl.Label, n) {
					if fl.Value != "" {
						return fl.Value
					}
				}
			}
		}
		return ""
	}

	c := &Credentials{
		Host:     find(f.Host),
		User:     find(f.User),
		Password: []byte(find(f.Password)),
		Database: find(f.Database),
		Port:     3306,
	}
	var missing []string
	if c.Host == "" {
		missing = append(missing, "host ("+strings.Join(f.Host, "/")+")")
	}
	if c.User == "" {
		missing = append(missing, "user ("+strings.Join(f.User, "/")+")")
	}
	if len(c.Password) == 0 {
		missing = append(missing, "password ("+strings.Join(f.Password, "/")+")")
	}
	if len(missing) > 0 {
		c.Wipe()
		return nil, fmt.Errorf("1Password item is missing fields: %s", strings.Join(missing, ", "))
	}
	if p := find(f.Port); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			c.Wipe()
			return nil, fmt.Errorf("1Password item has an invalid port value")
		}
		c.Port = n
	}
	return c, nil
}
