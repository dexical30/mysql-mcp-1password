package query

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// convert turns a raw text-protocol value into a JSON-friendly value.
// BIGINT and DECIMAL stay strings so JSON consumers cannot lose precision.
func convert(typeName string, raw []byte) any {
	if raw == nil {
		return nil
	}
	switch strings.TrimPrefix(typeName, "UNSIGNED ") {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "YEAR":
		if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
			return n
		}
	case "FLOAT", "DOUBLE":
		if f, err := strconv.ParseFloat(string(raw), 64); err == nil {
			return f
		}
	case "BINARY", "VARBINARY", "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BIT", "GEOMETRY":
		return "base64:" + base64.StdEncoding.EncodeToString(raw)
	}
	return string(raw)
}
