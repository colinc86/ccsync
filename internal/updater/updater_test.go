package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestArchLabel(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"amd64", "x86_64", false},
		{"arm64", "arm64", false},
		{"386", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := archLabel(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("archLabel(%q) expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("archLabel(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("archLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsPrivateRepoErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("random"), false},
		{"status 404", &httpStatusErr{status: 404}, true},
		{"status 403", &httpStatusErr{status: 403}, true},
		{"status 401", &httpStatusErr{status: 401}, true},
		{"status 500", &httpStatusErr{status: 500}, false},
	}
	for _, c := range cases {
		if got := isPrivateRepoErr(c.err); got != c.want {
			t.Errorf("%s: isPrivateRepoErr = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsHomebrew(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/opt/homebrew/bin/ccsync", true},
		{"/usr/local/Cellar/ccsync/0.6.4/bin/ccsync", true},
		{"/home/linuxbrew/.linuxbrew/bin/ccsync", true},
		{"/usr/local/bin/ccsync", false},
		{"/Users/colin/.local/bin/ccsync", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsHomebrew(c.path); got != c.want {
			t.Errorf("IsHomebrew(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestExtractBinarySuccess drives a one-entry tarball through
// extractBinary and verifies the bytes round-trip.
func TestExtractBinarySuccess(t *testing.T) {
	body := []byte("#!/usr/bin/env bash\necho hi\n")
	tgz := makeTarGz(t, map[string][]byte{
		"ccsync": body,
	})

	var out bytes.Buffer
	if err := extractBinary(bytes.NewReader(tgz), &out); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(out.Bytes(), body) {
		t.Errorf("extracted bytes = %q, want %q", out.Bytes(), body)
	}
}

// TestExtractBinarySkipsNonMatching proves the function walks past
// other entries until it finds one named "ccsync".
func TestExtractBinarySkipsNonMatching(t *testing.T) {
	want := []byte("real binary")
	tgz := makeTarGz(t, map[string][]byte{
		"README.md":     []byte("docs"),
		"LICENSE":       []byte("mit"),
		"not-ccsync":    []byte("wrong"),
		"nested/ccsync": want,
	})

	var out bytes.Buffer
	if err := extractBinary(bytes.NewReader(tgz), &out); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Errorf("wrong entry extracted: %q", out.Bytes())
	}
}

func TestExtractBinaryMissingEntry(t *testing.T) {
	tgz := makeTarGz(t, map[string][]byte{
		"README.md": []byte("just docs"),
	})
	var out bytes.Buffer
	err := extractBinary(bytes.NewReader(tgz), &out)
	if err == nil || !strings.Contains(err.Error(), "not found in release tarball") {
		t.Errorf("expected 'not found' error, got %v", err)
	}
}

// TestExtractBinaryCaps enforces that an oversize binary is rejected.
// This pins the maxBinarySize hardening — the pre-fix code did an
// unbounded io.Copy that would happily fill disk with a gigabyte
// ccsync "binary" from a tampered tarball.
func TestExtractBinaryCaps(t *testing.T) {
	// A 2-byte-oversize blob is enough to trigger the check; the
	// CopyN reads maxBinarySize+1 before deciding.
	oversize := bytes.Repeat([]byte("A"), maxBinarySize+2)
	tgz := makeTarGz(t, map[string][]byte{
		"ccsync": oversize,
	})
	var out bytes.Buffer
	err := extractBinary(bytes.NewReader(tgz), &out)
	if err == nil {
		t.Fatal("expected error for oversize binary, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to extract") {
		t.Errorf("expected cap-exceeded error, got %v", err)
	}
}

// makeTarGz builds an in-memory gzip'd tarball from name → contents.
// Entry order is non-deterministic (map iteration) — fine for our
// tests which verify discovery, not ordering.
func makeTarGz(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(tw, bytes.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
