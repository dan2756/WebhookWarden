package main

import (
	"strings"
	"testing"
)

func TestPrefixWriter(t *testing.T) {
	var out syncBuffer
	w := &prefixWriter{log: newLogger(&out), prefix: "[#7] "}
	for _, chunk := range []string{"hel", "lo\nwor", "ld\n", "\n", "no newline"} {
		w.Write([]byte(chunk))
	}
	w.Flush()
	want := "[#7] hello\n[#7] world\n[#7] \n[#7] no newline\n"
	if got := out.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrefixWriterLongLine(t *testing.T) {
	var out syncBuffer
	w := &prefixWriter{log: newLogger(&out), prefix: "> "}
	chunk := []byte(strings.Repeat("x", 1024))
	for range maxLine / len(chunk) {
		w.Write(chunk)
	}
	w.Write([]byte("tail"))
	w.Flush()
	want := "> " + strings.Repeat("x", maxLine) + "\n> tail\n"
	if got := out.String(); got != want {
		t.Errorf("got %d bytes, want a %d byte line and then \"tail\"", len(got), maxLine)
	}
}

func TestCapBuffer(t *testing.T) {
	b := &capBuffer{max: 5}
	for _, s := range []string{"ab", "cd", "efg", "h"} {
		if n, err := b.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	if got := b.buf.String(); got != "abcde" || !b.truncated {
		t.Errorf("got %q truncated=%v, want %q truncated=true", got, b.truncated, "abcde")
	}
}
