package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestLoad_Defaults(t *testing.T) {
	c, err := Load(env(map[string]string{"MYSQL_OP_ITEM": "op://Dev/mysql-prod"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.OpVault != "Dev" || c.OpItem != "mysql-prod" || !c.UsesItem() {
		t.Fatalf("vault/item = %q/%q", c.OpVault, c.OpItem)
	}
	if c.IdleTimeout != 15*time.Minute || c.QueryTimeout != 30*time.Second || c.MaxRows != 100 {
		t.Fatalf("defaults = %v %v %d", c.IdleTimeout, c.QueryTimeout, c.MaxRows)
	}
	if c.AllowInsert || c.AllowUpdate || c.AllowDelete || c.AllowDDL || c.TLS {
		t.Fatalf("flags should default to false: %+v", c)
	}
	if !reflect.DeepEqual(c.Fields, DefaultFieldMap()) {
		t.Fatalf("Fields = %+v", c.Fields)
	}
	if !c.ReadOnly() {
		t.Fatal("ReadOnly() = false, want true")
	}
}

func TestLoad_ItemWithSpaces(t *testing.T) {
	c, err := Load(env(map[string]string{"MYSQL_OP_ITEM": "op://My Vault/My DB"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.OpVault != "My Vault" || c.OpItem != "My DB" {
		t.Fatalf("vault/item = %q/%q", c.OpVault, c.OpItem)
	}
}

func TestLoad_RequiresItem(t *testing.T) {
	if _, err := Load(env(nil)); err == nil {
		t.Fatal("want error when MYSQL_OP_ITEM is unset")
	}
}

func TestLoad_RejectsBadItem(t *testing.T) {
	for _, v := range []string{"Dev/mysql", "op://Dev", "op://Dev/item/password", "op:///item"} {
		if _, err := Load(env(map[string]string{"MYSQL_OP_ITEM": v})); err == nil {
			t.Errorf("MYSQL_OP_ITEM=%q: want error", v)
		}
	}
}

func TestLoad_FlagsExactTrue(t *testing.T) {
	for _, v := range []string{"TRUE", "1", "yes"} {
		c, err := Load(env(map[string]string{"MYSQL_OP_ITEM": "op://D/i", "MYSQL_ALLOW_DELETE": v}))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if c.AllowDelete {
			t.Errorf("MYSQL_ALLOW_DELETE=%q enabled deletes", v)
		}
	}
	c, err := Load(env(map[string]string{"MYSQL_OP_ITEM": "op://D/i", "MYSQL_ALLOW_DELETE": "true"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.AllowDelete || c.ReadOnly() {
		t.Fatal("MYSQL_ALLOW_DELETE=true should enable deletes")
	}
}

func TestLoad_BadDurationsAndRows(t *testing.T) {
	bad := []map[string]string{
		{"MYSQL_IDLE_TIMEOUT": "abc"},
		{"MYSQL_IDLE_TIMEOUT": "0s"},
		{"MYSQL_QUERY_TIMEOUT": "-1s"},
		{"MYSQL_MAX_ROWS": "0"},
		{"MYSQL_MAX_ROWS": "-5"},
		{"MYSQL_MAX_ROWS": "x"},
	}
	for _, kv := range bad {
		kv["MYSQL_OP_ITEM"] = "op://D/i"
		if _, err := Load(env(kv)); err == nil {
			t.Errorf("%v: want error", kv)
		}
	}
}

func TestLoad_FieldOverride(t *testing.T) {
	c, err := Load(env(map[string]string{"MYSQL_OP_ITEM": "op://D/i", "MYSQL_OP_FIELD_HOST": "db_host"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := DefaultFieldMap()
	want.Host = []string{"db_host"}
	if !reflect.DeepEqual(c.Fields, want) {
		t.Fatalf("Fields = %+v, want %+v", c.Fields, want)
	}
}

func refEnv() map[string]string {
	return map[string]string{
		"MYSQL_HOST": "op://Dev/mysql-dev/MYSQL_HOST",
		"MYSQL_PORT": "op://Dev/mysql-dev/MYSQL_PORT",
		"MYSQL_USER": "op://Dev/mysql-dev/MYSQL_USER",
		"MYSQL_PASS": "op://Dev/mysql-dev/MYSQL_PASS",
		"MYSQL_DB":   "op://Dev/mysql-dev/MYSQL_DB",
	}
}

func TestLoad_ReferenceMode(t *testing.T) {
	c, err := Load(env(refEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.UsesItem() {
		t.Fatal("UsesItem() = true in reference mode")
	}
	want := Refs{
		Host:     "op://Dev/mysql-dev/MYSQL_HOST",
		Port:     "op://Dev/mysql-dev/MYSQL_PORT",
		User:     "op://Dev/mysql-dev/MYSQL_USER",
		Password: "op://Dev/mysql-dev/MYSQL_PASS",
		Database: "op://Dev/mysql-dev/MYSQL_DB",
	}
	if c.Refs != want {
		t.Fatalf("Refs = %+v", c.Refs)
	}
}

func TestLoad_ReferenceModeAllowsLiteralNonSecrets(t *testing.T) {
	kv := refEnv()
	kv["MYSQL_HOST"] = "127.0.0.1"
	delete(kv, "MYSQL_PORT")
	delete(kv, "MYSQL_DB")
	c, err := Load(env(kv))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Refs.Host != "127.0.0.1" || c.Refs.Port != "" || c.Refs.Database != "" {
		t.Fatalf("Refs = %+v", c.Refs)
	}
}

func TestLoad_ReferenceModeRejectsPlaintextPassword(t *testing.T) {
	kv := refEnv()
	kv["MYSQL_PASS"] = "hunter2"
	_, err := Load(env(kv))
	if err == nil {
		t.Fatal("plaintext MYSQL_PASS was accepted")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("error leaks the password: %v", err)
	}
}

func TestLoad_ReferenceModeRequiresHostUserPass(t *testing.T) {
	for _, k := range []string{"MYSQL_HOST", "MYSQL_USER", "MYSQL_PASS"} {
		kv := refEnv()
		delete(kv, k)
		if _, err := Load(env(kv)); err == nil {
			t.Errorf("missing %s: want error", k)
		}
	}
}

func TestLoad_ReferenceModeRejectsBadReference(t *testing.T) {
	for _, ref := range []string{"op://Dev/mysql-dev", "op://Dev", "op:///item/field"} {
		kv := refEnv()
		kv["MYSQL_HOST"] = ref
		if _, err := Load(env(kv)); err == nil {
			t.Errorf("MYSQL_HOST=%q: want error", ref)
		}
	}
}

func TestLoad_RejectsBothModes(t *testing.T) {
	kv := refEnv()
	kv["MYSQL_OP_ITEM"] = "op://Dev/mysql-dev"
	if _, err := Load(env(kv)); err == nil {
		t.Fatal("want error when MYSQL_OP_ITEM and MYSQL_HOST/... are both set")
	}
}
