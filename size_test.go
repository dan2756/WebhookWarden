package main

import "testing"

func TestParseSize(t *testing.T) {
	tests := map[string]int64{
		"0":       0,
		"1048576": 1 << 20,
		"512b":    512,
		"64k":     64 << 10,
		"64KB":    64 << 10,
		"10MB":    10 << 20,
		"10mb":    10 << 20,
		"10 MiB":  10 << 20,
		"1.5M":    3 << 19,
		"2G":      2 << 30,
		" 1kb ":   1 << 10,
		"0.5MB":   1 << 19,
		"0MB":     0,
		"0.0":     0,
	}
	for in, want := range tests {
		got, err := parseSize(in)
		if err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}

	for _, in := range []string{"", "MB", "ten", "-1", "10XB", "10 M B", "1e30GB", "NaN", "inf", "0.5", "0.9", "0.0001KB"} {
		if got, err := parseSize(in); err == nil {
			t.Errorf("parseSize(%q) = %d, want an error", in, got)
		}
	}
}

func TestFormatSize(t *testing.T) {
	for n, want := range map[int64]string{
		0:             "0",
		100:           "100",
		10 << 20:      "10MB",
		1536:          "1536",
		64 << 10:      "64KB",
		3 << 30:       "3GB",
		(1 << 20) + 1: "1048577",
	} {
		if got := formatSize(n); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", n, got, want)
		}
		if n == 0 {
			continue
		}
		if back, err := parseSize(formatSize(n)); err != nil || back != n {
			t.Errorf("parseSize(formatSize(%d)) = %d, %v", n, back, err)
		}
	}
}
