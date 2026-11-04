// Package config reads the server's settings from environment variables only.
// No file (such as .env) is ever read, so nothing in a working directory can
// redirect the server or supply credentials.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FieldMap lists, per connection setting, the 1Password field labels or ids
// that may hold it (matched case-insensitively).
type FieldMap struct {
	Host, Port, User, Password, Database []string
}

// DefaultFieldMap matches 1Password's "Database" item template.
func DefaultFieldMap() FieldMap {
	return FieldMap{
		Host:     []string{"server", "hostname", "host"},
		Port:     []string{"port"},
		User:     []string{"username", "user"},
		Password: []string{"password"},
		Database: []string{"database"},
	}
}

// Refs holds per-setting values for reference mode: each is either an
// op://<Vault>/<Item>/<Field> secret reference or, except Password, a
// literal value.
type Refs struct {
	Host, Port, User, Password, Database string
}

type Config struct {
	// Item mode: one 1Password item read as a whole (MYSQL_OP_ITEM).
	OpVault, OpItem string
	// Reference mode: MYSQL_HOST/PORT/USER/PASS/DB, used when OpItem is empty.
	Refs   Refs
	OpPath string

	IdleTimeout  time.Duration
	QueryTimeout time.Duration
	MaxRows      int

	AllowInsert, AllowUpdate, AllowDelete, AllowDDL bool
	TLS                                             bool

	Fields FieldMap
}

// UsesItem reports whether credentials come from a whole 1Password item
// rather than per-setting references.
func (c Config) UsesItem() bool { return c.OpItem != "" }

// ReadOnly reports whether every write flag is off.
func (c Config) ReadOnly() bool {
	return !c.AllowInsert && !c.AllowUpdate && !c.AllowDelete && !c.AllowDDL
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{
		OpPath:      getenv("MYSQL_OP_PATH"),
		AllowInsert: getenv("MYSQL_ALLOW_INSERT") == "true",
		AllowUpdate: getenv("MYSQL_ALLOW_UPDATE") == "true",
		AllowDelete: getenv("MYSQL_ALLOW_DELETE") == "true",
		AllowDDL:    getenv("MYSQL_ALLOW_DDL") == "true",
		TLS:         getenv("MYSQL_TLS") == "true",
		Fields:      DefaultFieldMap(),
	}

	var err error
	refs := Refs{
		Host:     getenv("MYSQL_HOST"),
		Port:     getenv("MYSQL_PORT"),
		User:     getenv("MYSQL_USER"),
		Password: getenv("MYSQL_PASS"),
		Database: getenv("MYSQL_DB"),
	}
	switch item := getenv("MYSQL_OP_ITEM"); {
	case item != "" && refs != (Refs{}):
		return Config{}, fmt.Errorf("set either MYSQL_OP_ITEM or MYSQL_HOST/MYSQL_USER/MYSQL_PASS, not both")
	case item != "":
		if c.OpVault, c.OpItem, err = parseItemRef(item); err != nil {
			return Config{}, err
		}
	case refs != (Refs{}):
		if err := validateRefs(refs); err != nil {
			return Config{}, err
		}
		c.Refs = refs
	default:
		return Config{}, fmt.Errorf("MYSQL_OP_ITEM (op://<Vault>/<Item>) or MYSQL_HOST/MYSQL_USER/MYSQL_PASS is required")
	}
	if c.IdleTimeout, err = positiveDuration(getenv, "MYSQL_IDLE_TIMEOUT", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if c.QueryTimeout, err = positiveDuration(getenv, "MYSQL_QUERY_TIMEOUT", 30*time.Second); err != nil {
		return Config{}, err
	}
	c.MaxRows = 100
	if v := getenv("MYSQL_MAX_ROWS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("MYSQL_MAX_ROWS must be a positive integer, got %q", v)
		}
		c.MaxRows = n
	}

	overrides := []struct {
		key string
		dst *[]string
	}{
		{"MYSQL_OP_FIELD_HOST", &c.Fields.Host},
		{"MYSQL_OP_FIELD_PORT", &c.Fields.Port},
		{"MYSQL_OP_FIELD_USER", &c.Fields.User},
		{"MYSQL_OP_FIELD_PASSWORD", &c.Fields.Password},
		{"MYSQL_OP_FIELD_DATABASE", &c.Fields.Database},
	}
	for _, o := range overrides {
		if v := getenv(o.key); v != "" {
			*o.dst = []string{v}
		}
	}
	return c, nil
}

func validateRefs(r Refs) error {
	vars := []struct {
		name, value string
		required    bool
	}{
		{"MYSQL_HOST", r.Host, true},
		{"MYSQL_PORT", r.Port, false},
		{"MYSQL_USER", r.User, true},
		{"MYSQL_PASS", r.Password, true},
		{"MYSQL_DB", r.Database, false},
	}
	for _, v := range vars {
		if v.value == "" {
			if v.required {
				return fmt.Errorf("%s is required", v.name)
			}
			continue
		}
		if strings.HasPrefix(v.value, "op://") && !validSecretRef(v.value) {
			return fmt.Errorf("%s must look like op://<Vault>/<Item>/<Field>", v.name)
		}
	}
	// A plaintext password in the MCP config is exactly what this server
	// exists to avoid.
	if !strings.HasPrefix(r.Password, "op://") {
		return fmt.Errorf("MYSQL_PASS must be a 1Password reference (op://<Vault>/<Item>/<Field>), not a plaintext password")
	}
	return nil
}

// validSecretRef accepts op://<Vault>/<Item>/[<Section>/]<Field>.
func validSecretRef(ref string) bool {
	parts := strings.Split(strings.TrimPrefix(ref, "op://"), "/")
	if len(parts) < 3 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
	}
	return true
}

// parseItemRef splits op://<Vault>/<Item>.
func parseItemRef(ref string) (vault, item string, err error) {
	if ref == "" {
		return "", "", fmt.Errorf("MYSQL_OP_ITEM is required (op://<Vault>/<Item>)")
	}
	rest, ok := strings.CutPrefix(ref, "op://")
	parts := strings.Split(rest, "/")
	if !ok || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("MYSQL_OP_ITEM must look like op://<Vault>/<Item>, got %q", ref)
	}
	return parts[0], parts[1], nil
}

func positiveDuration(getenv func(string) string, key string, def time.Duration) (time.Duration, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration like 15m, got %q", key, v)
	}
	return d, nil
}
