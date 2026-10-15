// Package stamp embeds per-download connection details into an agent binary.
//
// The server appends a small trailer to the generic agent executable when a
// user downloads it with their session code:
//
//	<original binary> <JSON> <len:uint32 little-endian> "SRSTAMP1"
//
// Operating system loaders ignore trailing data on PE, ELF, and Mach-O files,
// so the binary runs unchanged. The agent reads the trailer from its own
// executable at startup.
package stamp

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Magic marks the end of a stamped binary.
const Magic = "SRSTAMP1"

const (
	trailerLen = 4 + len(Magic)
	maxJSON    = 8 << 10
)

// Stamp is the data embedded in a downloaded agent.
type Stamp struct {
	ServerURL string `json:"server_url"`
	Code      string `json:"code"` // empty for reusable server-only downloads
	OrgName   string `json:"org_name,omitempty"`
}

// ErrNotStamped means the binary carries no stamp.
var ErrNotStamped = errors.New("binary is not stamped")

// Validate checks the stamp's fields.
func (s Stamp) Validate() error {
	if err := ValidateServerURL(s.ServerURL); err != nil {
		return err
	}
	if s.Code != "" && !IsCode(s.Code) {
		return fmt.Errorf("invalid code %q", s.Code)
	}
	return nil
}

// ValidateServerURL accepts the HTTP(S) transport used by the agent. HTTP is
// retained for existing development deployments; production should use HTTPS.
// Credentials, query strings and fragments must never enter reusable assets.
func ValidateServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return errors.New("enter a valid HTTP(S) server URL without credentials, query or fragment")
	}
	return nil
}

// Trailer returns the bytes to append to a binary.
func Trailer(s Stamp) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	js, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	if len(js) > maxJSON {
		return nil, errors.New("stamp too large")
	}
	out := make([]byte, 0, len(js)+trailerLen)
	out = append(out, js...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(js)))
	return append(out, Magic...), nil
}

// Write copies base to w followed by the stamp trailer. If base already
// carries a stamp it is replaced.
func Write(w io.Writer, base []byte, s Stamp) error {
	t, err := Trailer(s)
	if err != nil {
		return err
	}
	if _, err := w.Write(Strip(base)); err != nil {
		return err
	}
	_, err = w.Write(t)
	return err
}

// Strip returns data without a trailing stamp, if any.
func Strip(data []byte) []byte {
	n, ok := jsonLen(data)
	if !ok {
		return data
	}
	return data[:len(data)-trailerLen-n]
}

// Parse extracts the stamp from the end of data.
func Parse(data []byte) (Stamp, error) {
	n, ok := jsonLen(data)
	if !ok {
		return Stamp{}, ErrNotStamped
	}
	end := len(data) - trailerLen
	return decode(data[end-n : end])
}

// ReadFile extracts the stamp from the file at path without loading it all.
func ReadFile(path string) (Stamp, error) {
	f, err := os.Open(path)
	if err != nil {
		return Stamp{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Stamp{}, err
	}
	size := fi.Size()
	if size < int64(trailerLen) {
		return Stamp{}, ErrNotStamped
	}
	tail := make([]byte, trailerLen)
	if _, err := f.ReadAt(tail, size-int64(trailerLen)); err != nil {
		return Stamp{}, err
	}
	if string(tail[4:]) != Magic {
		return Stamp{}, ErrNotStamped
	}
	n := int64(binary.LittleEndian.Uint32(tail))
	if n == 0 || n > maxJSON || n > size-int64(trailerLen) {
		return Stamp{}, ErrNotStamped
	}
	js := make([]byte, n)
	if _, err := f.ReadAt(js, size-int64(trailerLen)-n); err != nil {
		return Stamp{}, err
	}
	return decode(js)
}

func jsonLen(data []byte) (int, bool) {
	if len(data) < trailerLen || !bytes.Equal(data[len(data)-len(Magic):], []byte(Magic)) {
		return 0, false
	}
	n := int(binary.LittleEndian.Uint32(data[len(data)-trailerLen:]))
	if n == 0 || n > maxJSON || n > len(data)-trailerLen {
		return 0, false
	}
	return n, true
}

func decode(js []byte) (Stamp, error) {
	var s Stamp
	if err := json.Unmarshal(js, &s); err != nil {
		return Stamp{}, fmt.Errorf("corrupt stamp: %w", err)
	}
	if err := s.Validate(); err != nil {
		return Stamp{}, err
	}
	return s, nil
}

// CodeLength is the number of digits in a session code.
const CodeLength = 6

var (
	codeRE     = regexp.MustCompile(`^[0-9]{6}$`)
	nameCodeRE = regexp.MustCompile(`(?:^|[^0-9])([0-9]{6})(?:[^0-9]|$)`)
)

// IsCode reports whether s is a normalized session code (six digits).
func IsCode(s string) bool { return codeRE.MatchString(s) }

// NormalizeCode keeps only the digits of user input (dropping spaces and dashes).
func NormalizeCode(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// CodeFromFilename finds a session code in a download's file name, such as
// "SimpleRemote-123456.exe" or "SimpleRemote-123456 (1).app". It is a
// fallback for when the stamp can't be read.
func CodeFromFilename(path string) string {
	m := nameCodeRE.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return ""
	}
	return m[1]
}
