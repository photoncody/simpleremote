package stamp

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	base := []byte("\x7fELF fake binary contents")
	s := Stamp{ServerURL: "https://support.example.com", Code: "123456789", OrgName: "Example"}

	var buf bytes.Buffer
	if err := Write(&buf, base, s); err != nil {
		t.Fatal(err)
	}
	got, err := Parse(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got != s {
		t.Fatalf("got %+v, want %+v", got, s)
	}
	if !bytes.Equal(Strip(buf.Bytes()), base) {
		t.Fatal("Strip did not restore the base binary")
	}

	path := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(path, buf.Bytes(), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != s {
		t.Fatalf("ReadFile got %+v", got)
	}
}

func TestRestampReplaces(t *testing.T) {
	base := []byte("binary")
	var first, second bytes.Buffer
	_ = Write(&first, base, Stamp{ServerURL: "https://a", Code: "111111111"})
	_ = Write(&second, first.Bytes(), Stamp{ServerURL: "https://b", Code: "222222222"})
	got, err := Parse(second.Bytes())
	if err != nil || got.Code != "222222222" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if !bytes.Equal(Strip(second.Bytes()), base) {
		t.Fatal("old stamp was not replaced")
	}
}

func TestUnstamped(t *testing.T) {
	if _, err := Parse([]byte("plain binary")); !errors.Is(err, ErrNotStamped) {
		t.Fatalf("got %v", err)
	}
	path := filepath.Join(t.TempDir(), "agent")
	_ = os.WriteFile(path, []byte("x"), 0o755)
	if _, err := ReadFile(path); !errors.Is(err, ErrNotStamped) {
		t.Fatalf("got %v", err)
	}
}

func TestRejectsBadStamp(t *testing.T) {
	if _, err := Trailer(Stamp{ServerURL: "ftp://x", Code: "123456789"}); err == nil {
		t.Fatal("expected error for bad URL")
	}
	if _, err := Trailer(Stamp{ServerURL: "https://x", Code: "12345"}); err == nil {
		t.Fatal("expected error for bad code")
	}
}

func TestCodeHelpers(t *testing.T) {
	if NormalizeCode(" 123-456 789 ") != "123456789" {
		t.Fatal("NormalizeCode")
	}
	if FormatCode("123456789") != "123-456-789" {
		t.Fatal("FormatCode")
	}
	cases := map[string]string{
		"SimpleRemote-123456789.exe":            "123456789",
		"/Downloads/SimpleRemote-123456789 (1)": "123456789",
		"SimpleRemote-123-456-789.app":          "123456789",
		"SimpleRemote.exe":                      "",
		"SimpleRemote-1234567890.exe":           "",
	}
	for in, want := range cases {
		if got := CodeFromFilename(in); got != want {
			t.Errorf("CodeFromFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
