package domain

import (
	"database/sql/driver"
	"testing"
)

func TestJSONMapValueAndScan(t *testing.T) {
	cases := []struct {
		name     string
		input    JSONMap
		scanData interface{}
	}{
		{name: "nil", input: nil, scanData: nil},
		{name: "string", input: JSONMap{"k": "v"}, scanData: `{"k":"v"}`},
		{name: "bytes", input: JSONMap{"num": float64(1)}, scanData: []byte(`{"num":1}`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			val, err := tc.input.Value()
			if err != nil {
				t.Fatalf("Value error: %v", err)
			}
			if tc.input == nil && val != nil {
				t.Fatalf("expected nil driver value for nil map, got %v", val)
			}

			var out JSONMap
			if err := out.Scan(tc.scanData); err != nil {
				t.Fatalf("Scan error: %v", err)
			}

			if len(out) != len(tc.input) {
				t.Fatalf("length mismatch: expected %d got %d", len(tc.input), len(out))
			}
		})
	}
}

func TestJSONMapScanUnsupportedType(t *testing.T) {
	var m JSONMap
	if err := m.Scan(driver.Value(123)); err == nil {
		t.Fatal("expected error for unsupported type")
	}
}
