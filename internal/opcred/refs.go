package opcred

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dexical30/mysql-mcp-1password/internal/config"
)

// RefSource resolves one op:// reference per connection setting, matching
// configs written as MYSQL_HOST=op://Vault/Item/Field and so on. Only the
// referenced fields are read, never the rest of the item.
type RefSource struct {
	OpPath  string
	Refs    config.Refs
	Timeout time.Duration
	run     runner
	resolve func(string) (string, error)
}

func NewRefSource(opPath string, refs config.Refs) *RefSource {
	return &RefSource{OpPath: opPath, Refs: refs, Timeout: 60 * time.Second, run: execRun, resolve: ResolveOpPath}
}

func (s *RefSource) Fetch(ctx context.Context) (*Credentials, error) {
	opPath, err := s.resolve(s.OpPath)
	if err != nil {
		return nil, err
	}
	// One deadline for all lookups: the first may wait on Touch ID, the rest
	// reuse that approval.
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	value := func(name, v string) ([]byte, error) {
		if !strings.HasPrefix(v, "op://") {
			return []byte(v), nil
		}
		stdout, stderr, err := s.run(ctx, opPath, "read", "--no-newline", v)
		if err != nil {
			clear(stdout)
			detail := strings.TrimSpace(string(stderr))
			if detail == "" {
				detail = err.Error()
			}
			return nil, fmt.Errorf("1Password lookup for %s failed: %s", name, detail)
		}
		return stdout, nil
	}

	c := &Credentials{Port: 3306}
	fail := func(err error) (*Credentials, error) {
		c.Wipe()
		return nil, err
	}
	str := func(name, v string) (string, error) {
		b, err := value(name, v)
		defer clear(b)
		return string(b), err
	}

	if c.Host, err = str("MYSQL_HOST", s.Refs.Host); err != nil {
		return fail(err)
	}
	if c.User, err = str("MYSQL_USER", s.Refs.User); err != nil {
		return fail(err)
	}
	if c.Password, err = value("MYSQL_PASS", s.Refs.Password); err != nil {
		return fail(err)
	}
	if s.Refs.Database != "" {
		if c.Database, err = str("MYSQL_DB", s.Refs.Database); err != nil {
			return fail(err)
		}
	}
	if s.Refs.Port != "" {
		p, err := str("MYSQL_PORT", s.Refs.Port)
		if err != nil {
			return fail(err)
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return fail(fmt.Errorf("MYSQL_PORT has an invalid port value"))
		}
		c.Port = n
	}
	if c.Host == "" || c.User == "" || len(c.Password) == 0 {
		return fail(fmt.Errorf("1Password returned an empty value for MYSQL_HOST, MYSQL_USER or MYSQL_PASS"))
	}
	return c, nil
}
