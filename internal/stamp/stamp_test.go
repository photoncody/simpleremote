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
	s := Stamp{ServerURL: "https://support.example.com", Code: "123456", OrgName: "Example"}

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
	_ = Write(&first, base, Stamp{ServerURL: "https://a", Code: "111111"})
	_ = Write(&second, first.Bytes(), Stamp{ServerURL: "https://b", Code: "222222"})
	got, err := Parse(second.Bytes())
	if err != nil || got.Code != "222222" {
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
	if _, err := Trailer(Stamp{ServerURL: "ftp://x", Code: "123456"}); err == nil {
		t.Fatal("expected error for bad URL")
	}
	if _, err := Trailer(Stamp{ServerURL: "https://x", Code: "123456789"}); err == nil {
		t.Fatal("expected error for bad code")
	}
}

func TestCodeHelpers(t *testing.T) {
	if NormalizeCode(" 123 456 ") != "123456" {
		t.Fatal("NormalizeCode")
	}
	if !IsCode("123456") || IsCode("12345") || IsCode("1234567") || IsCode("12345a") {
		t.Fatal("IsCode")
	}
	cases := map[string]string{
		"SimpleRemote-123456.exe":            "123456",
		"/Downloads/SimpleRemote-123456 (1)": "123456",
		"SimpleRemote-123456.app":            "123456",
		"SimpleRemote.exe":                   "",
		"SimpleRemote-1234567.exe":           "",
	}
	for in, want := range cases {
		if got := CodeFromFilename(in); got != want {
			t.Errorf("CodeFromFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReusableStampAndServerValidation(t *testing.T) {
	s := Stamp{ServerURL: "https://support.example.com", OrgName: "Acme"}
	var out bytes.Buffer
	if err := Write(&out, []byte("binary"), s); err != nil {
		t.Fatal(err)
	}
	got, err := Parse(out.Bytes())
	if err != nil || got != s {
		t.Fatalf("reusable stamp = %+v, %v", got, err)
	}
	for _, raw := range []string{"", "https://", "ftp://support.example.com", "https://user:secret@support.example.com", "https://support.example.com?token=secret", "https://support.example.com#secret"} {
		if err := (Stamp{ServerURL: raw}).Validate(); err == nil {
			t.Errorf("accepted invalid URL %q", raw)
		}
	}
	for _, raw := range []string{"https://support.example.com", "http://localhost:8080", "https://support.example.com/support"} {
		if err := ValidateServerURL(raw); err != nil {
			t.Errorf("rejected %q: %v", raw, err)
		}
	}
}
