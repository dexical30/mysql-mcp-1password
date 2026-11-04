package query

import (
	"reflect"
	"testing"
)

func TestConvert(t *testing.T) {
	tests := []struct {
		typ  string
		raw  []byte
		want any
	}{
		{"INT", nil, nil},
		{"INT", []byte("42"), int64(42)},
		{"TINYINT", []byte("-1"), int64(-1)},
		{"BIGINT", []byte("9007199254740993"), "9007199254740993"},
		{"UNSIGNED BIGINT", []byte("18446744073709551615"), "18446744073709551615"},
		{"DECIMAL", []byte("1.10"), "1.10"},
		{"DOUBLE", []byte("1.5"), float64(1.5)},
		{"DATETIME", []byte("2026-11-02 10:00:00"), "2026-11-02 10:00:00"},
		{"VARCHAR", []byte("héllo"), "héllo"},
		{"BLOB", []byte{0xff, 0x00}, "base64:/wA="},
		{"JSON", []byte(`{"a":1}`), `{"a":1}`},
	}
	for _, tt := range tests {
		got := convert(tt.typ, tt.raw)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("convert(%q, %q) = %#v, want %#v", tt.typ, tt.raw, got, tt.want)
		}
	}
}
