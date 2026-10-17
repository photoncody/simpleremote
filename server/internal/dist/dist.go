// Package dist serves agent downloads stamped with the session code.
//
// AGENT_DIR holds the generic agent builds produced by CI:
//
//	simpleremote-agent-windows-amd64.exe
//	simpleremote-agent-windows-arm64.exe
//	simpleremote-agent-linux-amd64
//	simpleremote-agent-linux-arm64
//	simpleremote-agent-darwin-universal.zip   (contains SimpleRemote.app)
//
// Missing files are simply not offered.
package dist

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/photoncody/simpleremote/internal/stamp"
)

// Platform is one downloadable agent build.
type Platform struct {
	ID    string `json:"id"`
	OS    string `json:"os"`
	Arch  string `json:"arch"`
	Label string `json:"label"`
	Size  int64  `json:"size"`

	file string
	ext  string
}

var platforms = []Platform{
	{ID: "windows-amd64", OS: "windows", Arch: "amd64", Label: "Windows (64-bit)", file: "simpleremote-agent-windows-amd64.exe", ext: ".exe"},
	{ID: "windows-arm64", OS: "windows", Arch: "arm64", Label: "Windows (ARM)", file: "simpleremote-agent-windows-arm64.exe", ext: ".exe"},
	{ID: "darwin-universal", OS: "darwin", Arch: "universal", Label: "macOS", file: "simpleremote-agent-darwin-universal.zip", ext: ".zip"},
	{ID: "linux-amd64", OS: "linux", Arch: "amd64", Label: "Linux (x86-64)", file: "simpleremote-agent-linux-amd64", ext: ""},
	{ID: "linux-arm64", OS: "linux", Arch: "arm64", Label: "Linux (ARM64)", file: "simpleremote-agent-linux-arm64", ext: ""},
}

// Dist reads agent builds from a directory and caches them in memory.
type Dist struct {
	dir string

	mu    sync.Mutex
	cache map[string]cached

	// Serialize reusable artifact preparation, including cold concurrent requests.
	// There is at most one generated artifact per installed platform, never per
	// request or URL. Readers keep immutable snapshots while replacements build.
	reusableMu sync.Mutex
	reusable   map[string]reusableCached
}

type cached struct {
	mod  time.Time
	size int64
	data []byte
}

type reusableCached struct {
	mod   time.Time
	size  int64
	stamp stamp.Stamp
	data  []byte
}

// New creates a Dist for dir.
func New(dir string) *Dist {
	return &Dist{dir: dir, cache: map[string]cached{}, reusable: map[string]reusableCached{}}
}

// Available lists the platforms with a build present.
func (d *Dist) Available() []Platform {
	out := []Platform{}
	for _, p := range platforms {
		if fi, err := os.Stat(filepath.Join(d.dir, p.file)); err == nil && fi.Mode().IsRegular() {
			p.Size = fi.Size()
			out = append(out, p)
		}
	}
	return out
}

// Lookup finds a platform by ID.
func Lookup(id string) (Platform, bool) {
	for _, p := range platforms {
		if p.ID == id {
			return p, true
		}
	}
	return Platform{}, false
}

// ErrUnavailable means the build for a platform is not installed.
var ErrUnavailable = errors.New("agent build not available for this platform")

func (d *Dist) load(p Platform) (cached, error) {
	path := filepath.Join(d.dir, p.file)
	fi, err := os.Stat(path)
	if err != nil {
		return cached{}, ErrUnavailable
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.cache[p.ID]; ok && c.mod.Equal(fi.ModTime()) && c.size == fi.Size() {
		return c, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cached{}, err
	}
	c := cached{mod: fi.ModTime(), size: fi.Size(), data: data}
	d.cache[p.ID] = c
	return c, nil
}

// Filename is the name the browser saves the download as.
func Filename(p Platform, code string) string {
	if code == "" {
		return "SimpleRemote-Technician" + p.ext
	}
	return "SimpleRemote-" + code + p.ext
}

// Write streams the stamped agent for platform p to w.
func (d *Dist) Write(w io.Writer, p Platform, s stamp.Stamp) error {
	build, err := d.load(p)
	if err != nil {
		return err
	}
	return write(w, p, build.data, s)
}

// Reusable prepares a public-configuration-only artifact once and returns an
// independent reader over its immutable cached bytes. A changed source build
// (mtime/size, as with the base cache) or configuration replaces that platform's
// entry. Failed preparation is never published or served as a partial download.
func (d *Dist) Reusable(p Platform, s stamp.Stamp) (*bytes.Reader, error) {
	if s.Code != "" {
		return nil, errors.New("reusable agent cannot contain a session code")
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	d.reusableMu.Lock()
	defer d.reusableMu.Unlock()
	build, err := d.load(p)
	if err != nil {
		return nil, err
	}
	if c, ok := d.reusable[p.ID]; ok && c.mod.Equal(build.mod) && c.size == build.size && c.stamp == s {
		return bytes.NewReader(c.data), nil
	}
	var out bytes.Buffer
	if err := write(&out, p, build.data, s); err != nil {
		return nil, err
	}
	data := out.Bytes()
	d.reusable[p.ID] = reusableCached{mod: build.mod, size: build.size, stamp: s, data: data}
	return bytes.NewReader(data), nil
}

func write(w io.Writer, p Platform, base []byte, s stamp.Stamp) error {
	if s.Code == "" && p.ext != ".zip" {
		if _, err := stamp.Parse(base); !errors.Is(err, stamp.ErrNotStamped) {
			return errors.New("reusable agent requires an unstamped base executable")
		}
	}
	if p.ext == ".zip" {
		return stampZip(w, base, s)
	}
	trailer, err := stamp.Trailer(s)
	if err != nil {
		return err
	}
	if p.ext == "" {
		base = stamp.Strip(base)
	}
	if p.ext == ".exe" {
		// Keeps an Authenticode signature valid. Anything that doesn't parse
		// as a PE file gets the plain trailer.
		if out, err := stamp.AppendPE(base, trailer); err == nil {
			_, err = w.Write(out)
			return err
		}
	}
	if _, err := w.Write(base); err != nil {
		return err
	}
	_, err = w.Write(trailer)
	return err
}

// NoStampMarker in a macOS app bundle means the app is notarized and must not
// be modified; the server then only puts the code in the bundle's name, and
// the agent uses the server address it was built with.
const NoStampMarker = "Contents/Resources/simpleremote-nostamp"

// stampZip copies a macOS app zip, renaming the bundle to include the code
// and appending the stamp to the executables in Contents/MacOS.
func stampZip(w io.Writer, base []byte, s stamp.Stamp) error {
	zr, err := zip.NewReader(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		return fmt.Errorf("read agent zip: %w", err)
	}
	trailer, err := stamp.Trailer(s)
	if err != nil {
		return err
	}
	const bundle = "SimpleRemote.app/"
	newBundle := "SimpleRemote-" + s.Code + ".app/"
	if s.Code == "" {
		newBundle = "SimpleRemote-Technician.app/"
	}
	noStamp := false
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".app/"+NoStampMarker) {
			noStamp = true
			if s.Code == "" {
				rc, err := f.Open()
				if err != nil {
					return err
				}
				configured, err := io.ReadAll(io.LimitReader(rc, 8193))
				rc.Close()
				if err != nil {
					return err
				}
				// Config loading removes all trailing slashes from PUBLIC_URL.
				// Match equivalent build-time URLs without modifying the signed bundle.
				if strings.TrimRight(strings.TrimSpace(string(configured)), "/") != strings.TrimRight(s.ServerURL, "/") {
					return errors.New("signed macOS reusable agent must be rebuilt with the matching public server URL marker")
				}
			}
		}
	}
	if noStamp && s.Code == "" {
		for _, f := range zr.File {
			if strings.Contains(f.Name, ".app/Contents/MacOS/") && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					return err
				}
				data, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					return err
				}
				if _, err := stamp.Parse(data); !errors.Is(err, stamp.ErrNotStamped) {
					return errors.New("signed reusable agent contains a stamp")
				}
			}
		}
	}
	zw := zip.NewWriter(w)
	stamped := 0
	for _, f := range zr.File {
		hdr := f.FileHeader
		if strings.HasPrefix(hdr.Name, bundle) {
			hdr.Name = newBundle + strings.TrimPrefix(hdr.Name, bundle)
		}
		isExec := !noStamp && strings.Contains(f.Name, ".app/Contents/MacOS/") && !f.FileInfo().IsDir()
		if !isExec {
			raw, err := f.OpenRaw()
			if err != nil {
				return err
			}
			dst, err := zw.CreateRaw(&hdr)
			if err != nil {
				return err
			}
			if _, err := io.Copy(dst, raw); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		if s.Code == "" {
			if _, err := stamp.Parse(data); !errors.Is(err, stamp.ErrNotStamped) {
				return errors.New("reusable agent requires unstamped bundle executables")
			}
		}
		hdr.CRC32, hdr.CompressedSize64, hdr.UncompressedSize64 = 0, 0, 0
		hdr.Method = zip.Deflate
		dst, err := zw.CreateHeader(&hdr)
		if err != nil {
			return err
		}
		if _, err := dst.Write(stamp.Strip(data)); err != nil {
			return err
		}
		if _, err := dst.Write(trailer); err != nil {
			return err
		}
		stamped++
	}
	if stamped == 0 && !noStamp {
		return errors.New("agent zip has no SimpleRemote.app/Contents/MacOS executable")
	}
	return zw.Close()
}
