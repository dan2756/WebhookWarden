package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// byteSize is a flag.Value for sizes such as "10MB", "512k" or "1048576".
type byteSize int64

func (b *byteSize) String() string { return formatSize(int64(*b)) }

func (b *byteSize) Set(s string) error {
	n, err := parseSize(s)
	if err != nil {
		return err
	}
	*b = byteSize(n)
	return nil
}

var sizeUnits = map[string]int64{
	"": 1, "B": 1,
	"K": 1 << 10, "KB": 1 << 10, "KIB": 1 << 10,
	"M": 1 << 20, "MB": 1 << 20, "MIB": 1 << 20,
	"G": 1 << 30, "GB": 1 << 30, "GIB": 1 << 30,
}

// parseSize parses a byte count with an optional unit. Units are binary
// and case-insensitive: 10MB, 10M and 10MiB all mean 10*1024*1024 bytes.
func parseSize(s string) (int64, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	num := strings.TrimRightFunc(t, func(r rune) bool { return 'A' <= r && r <= 'Z' })
	mult, ok := sizeUnits[t[len(num):]]
	f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if !ok || err != nil || f < 0 || math.IsNaN(f) {
		return 0, fmt.Errorf("invalid size %q (want a number of bytes, or e.g. 512KB, 10MB, 1GB)", s)
	}
	n := f * float64(mult)
	if n >= math.MaxInt64 {
		return 0, fmt.Errorf("size %q is too large", s)
	}
	// A positive size that rounds down to 0 would disable the limit, the
	// same as an explicit 0. Reject it so --max-body 0.5 can't quietly
	// turn the limit off.
	if f > 0 && n < 1 {
		return 0, fmt.Errorf("invalid size %q (smaller than 1 byte; use 0 for no limit)", s)
	}
	return int64(n), nil
}

// formatSize is the inverse of parseSize, using the largest unit that
// divides n evenly.
func formatSize(n int64) string {
	for _, u := range []struct {
		name string
		mult int64
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}} {
		if n != 0 && n%u.mult == 0 {
			return strconv.FormatInt(n/u.mult, 10) + u.name
		}
	}
	return strconv.FormatInt(n, 10)
}
