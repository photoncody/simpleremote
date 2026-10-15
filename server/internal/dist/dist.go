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
}

type cached struct {
	mod  time.Time
	size int64
	data []byte
}

// New creates a Dist for dir.
func New(dir string) *Dist {
	return &Dist{dir: dir, cache: map[string]cached{}}
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

func (d *Dist) load(p Platform) ([]byte, error) {
	path := filepath.Join(d.dir, p.file)
	fi, err := os.Stat(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.cache[p.ID]; ok && c.mod.Equal(fi.ModTime()) && c.size == fi.Size() {
		return c.data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if p.ext == "" {
		data = stamp.Strip(data)
	}
	d.cache[p.ID] = cached{mod: fi.ModTime(), size: fi.Size(), data: data}
	return data, nil
}

// Filename is the name the browser saves the download as.
func Filename(p Platform, code string) string {
	return "SimpleRemote-" + code + p.ext
}

// Write streams the stamped agent for platform p to w.
func (d *Dist) Write(w io.Writer, p Platform, s stamp.Stamp) error {
	base, err := d.load(p)
	if err != nil {
		return err
	}
	if p.ext == ".zip" {
		return stampZip(w, base, s)
	}
	trailer, err := stamp.Trailer(s)
	if err != nil {
		return err
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
	noStamp := false
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".app/"+NoStampMarker) {
			noStamp = true
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
