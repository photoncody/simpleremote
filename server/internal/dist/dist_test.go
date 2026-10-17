package dist

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/photoncody/simpleremote/internal/stamp"
)

var testStamp = stamp.Stamp{ServerURL: "https://support.example.com", Code: "123456", OrgName: "Acme"}

func TestBinaryStamp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "simpleremote-agent-linux-amd64"), []byte("ELF"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := New(dir)
	av := d.Available()
	if len(av) != 1 || av[0].ID != "linux-amd64" || av[0].Size != 3 {
		t.Fatalf("available = %+v", av)
	}
	var buf bytes.Buffer
	if err := d.Write(&buf, av[0], testStamp); err != nil {
		t.Fatal(err)
	}
	got, err := stamp.Parse(buf.Bytes())
	if err != nil || got != testStamp {
		t.Fatalf("stamp = %+v, %v", got, err)
	}
	if Filename(av[0], "123456") != "SimpleRemote-123456" {
		t.Fatal("filename")
	}
	p, _ := Lookup("windows-amd64")
	if err := d.Write(io.Discard, p, testStamp); err != ErrUnavailable {
		t.Fatalf("missing build: %v", err)
	}
}

func TestZipStamp(t *testing.T) {
	var src bytes.Buffer
	zw := zip.NewWriter(&src)
	add := func(name string, mode fs.FileMode, body string) {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(mode)
		w, _ := zw.CreateHeader(h)
		_, _ = w.Write([]byte(body))
	}
	add("SimpleRemote.app/Contents/Info.plist", 0o644, "<plist/>")
	add("SimpleRemote.app/Contents/MacOS/simpleremote-agent", 0o755, "MACHO")
	_ = zw.Close()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "simpleremote-agent-darwin-universal.zip"), src.Bytes(), 0o644)
	d := New(dir)
	p, _ := Lookup("darwin-universal")
	var out bytes.Buffer
	if err := d.Write(&out, p, testStamp); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range zr.File {
		found[f.Name] = true
		if f.Name == "SimpleRemote-123456.app/Contents/MacOS/simpleremote-agent" {
			if f.Mode()&0o111 == 0 {
				t.Error("executable bit lost")
			}
			rc, _ := f.Open()
			data, _ := io.ReadAll(rc)
			rc.Close()
			got, err := stamp.Parse(data)
			if err != nil || got != testStamp || !bytes.HasPrefix(data, []byte("MACHO")) {
				t.Errorf("stamped exe = %q, %+v, %v", data, got, err)
			}
		}
	}
	if !found["SimpleRemote-123456.app/Contents/Info.plist"] {
		t.Fatalf("entries = %v", found)
	}
}

func TestReusableDownloads(t *testing.T) {
	for _, id := range []string{"linux-amd64", "windows-amd64"} {
		t.Run(id, func(t *testing.T) {
			p, _ := Lookup(id)
			dir := t.TempDir()
			base := []byte("binary")
			if err := os.WriteFile(filepath.Join(dir, p.file), base, 0755); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			want := stamp.Stamp{ServerURL: testStamp.ServerURL, OrgName: testStamp.OrgName}
			if err := New(dir).Write(&out, p, want); err != nil {
				t.Fatal(err)
			}
			got, err := stamp.Parse(out.Bytes())
			if err != nil || got != want {
				t.Fatalf("stamp = %+v %v", got, err)
			}
			if !bytes.Equal(stamp.Strip(out.Bytes()), base) {
				t.Fatal("unexpected payload")
			}
			if Filename(p, "") != "SimpleRemote-Technician"+p.ext {
				t.Fatal("reusable filename")
			}
		})
	}
}

func TestReusableMacOS(t *testing.T) {
	for _, mode := range []string{"unsigned", "signed", "signed-slash", "signed-path", "wrong-path", "legacy-marker", "wrong-server", "stamped-signed", "stamped-unsigned"} {
		t.Run(mode, func(t *testing.T) {
			var src bytes.Buffer
			zw := zip.NewWriter(&src)
			executable := []byte("MACHO")
			if mode == "stamped-signed" || mode == "stamped-unsigned" {
				var b bytes.Buffer
				_ = stamp.Write(&b, executable, testStamp)
				executable = b.Bytes()
			}
			w, _ := zw.Create("SimpleRemote.app/Contents/MacOS/agent")
			_, _ = w.Write(executable)
			if mode != "unsigned" && mode != "stamped-unsigned" {
				marker := testStamp.ServerURL
				if mode == "signed-slash" {
					marker += "/\n"
				}
				if mode == "signed-path" {
					marker += "/support///\n"
				}
				if mode == "wrong-path" {
					marker += "/other/"
				}
				if mode == "legacy-marker" {
					marker = ""
				}
				if mode == "wrong-server" {
					marker = "https://other.example.com"
				}
				w, _ = zw.Create("SimpleRemote.app/" + NoStampMarker)
				_, _ = w.Write([]byte(marker))
			}
			_ = zw.Close()
			var out bytes.Buffer
			serverURL := testStamp.ServerURL
			if mode == "signed-path" || mode == "wrong-path" {
				serverURL += "/support"
			}
			err := stampZip(&out, src.Bytes(), stamp.Stamp{ServerURL: serverURL, OrgName: testStamp.OrgName})
			if mode == "wrong-path" || mode == "legacy-marker" || mode == "wrong-server" || mode == "stamped-signed" || mode == "stamped-unsigned" {
				if err == nil {
					t.Fatal("accepted unsafe reusable build")
				}
				if mode == "legacy-marker" || mode == "wrong-server" {
					var sessionOut bytes.Buffer
					if err := stampZip(&sessionOut, src.Bytes(), testStamp); err != nil {
						t.Fatalf("session-specific signed download regressed: %v", err)
					}
					zr, err := zip.NewReader(bytes.NewReader(sessionOut.Bytes()), int64(sessionOut.Len()))
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, f := range zr.File {
						if f.Name == "SimpleRemote-123456.app/Contents/MacOS/agent" {
							found = true
							rc, _ := f.Open()
							data, _ := io.ReadAll(rc)
							rc.Close()
							if !bytes.Equal(data, executable) {
								t.Fatal("session-specific signed bytes changed")
							}
						}
					}
					if !found {
						t.Fatal("session-specific bundle missing")
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, f := range zr.File {
				if f.Name != "SimpleRemote-Technician.app/Contents/MacOS/agent" {
					continue
				}
				found = true
				rc, _ := f.Open()
				data, _ := io.ReadAll(rc)
				rc.Close()
				if mode == "signed" || mode == "signed-slash" || mode == "signed-path" {
					if !bytes.Equal(data, executable) {
						t.Fatal("signed executable modified")
					}
				} else {
					got, err := stamp.Parse(data)
					if err != nil || got.Code != "" || got.ServerURL != testStamp.ServerURL {
						t.Fatalf("stamp = %+v %v", got, err)
					}
				}
			}
			if !found {
				t.Fatal("missing reusable bundle")
			}

		})
	}
}

func TestReusableRejectsStampedWindowsBase(t *testing.T) {
	p, _ := Lookup("windows-amd64")
	dir := t.TempDir()
	var base bytes.Buffer
	_ = stamp.Write(&base, []byte("MZ"), testStamp)
	_ = os.WriteFile(filepath.Join(dir, p.file), base.Bytes(), 0755)
	if err := New(dir).Write(io.Discard, p, stamp.Stamp{ServerURL: testStamp.ServerURL}); err == nil {
		t.Fatal("accepted session-specific base")
	}
}

func TestReusableRejectsStampedLinuxBase(t *testing.T) {
	p, _ := Lookup("linux-amd64")
	dir := t.TempDir()
	var base bytes.Buffer
	_ = stamp.Write(&base, []byte("ELF"), testStamp)
	_ = os.WriteFile(filepath.Join(dir, p.file), base.Bytes(), 0755)
	if err := New(dir).Write(io.Discard, p, stamp.Stamp{ServerURL: testStamp.ServerURL}); err == nil {
		t.Fatal("accepted session-specific base")
	}
	// Session-specific downloads still replace a previous Linux stamp.
	var out bytes.Buffer
	if err := New(dir).Write(&out, p, testStamp); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stamp.Strip(out.Bytes()), []byte("ELF")) {
		t.Fatal("old stamp retained")
	}
}

func TestReusableCacheSharingAndInvalidation(t *testing.T) {
	for _, id := range []string{"linux-amd64", "darwin-universal"} {
		t.Run(id, func(t *testing.T) {
			p, _ := Lookup(id)
			dir := t.TempDir()
			path := filepath.Join(dir, p.file)
			makeBase := func(content string) []byte {
				if p.ext != ".zip" {
					return []byte(content)
				}
				var b bytes.Buffer
				z := zip.NewWriter(&b)
				w, err := z.Create("SimpleRemote.app/Contents/MacOS/agent")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write([]byte(content)); err != nil {
					t.Fatal(err)
				}
				if err := z.Close(); err != nil {
					t.Fatal(err)
				}
				return b.Bytes()
			}
			if err := os.WriteFile(path, makeBase("original executable"), 0755); err != nil {
				t.Fatal(err)
			}
			d := New(dir)
			config := stamp.Stamp{ServerURL: testStamp.ServerURL, OrgName: testStamp.OrgName}
			// Cold concurrent requests must share the generated artifact and have
			// independent read offsets, including when macOS recompression is needed.
			const n = 16
			readers := make([]*bytes.Reader, n)
			errs := make([]error, n)
			var wg sync.WaitGroup
			for i := range n {
				wg.Add(1)
				go func() { defer wg.Done(); readers[i], errs[i] = d.Reusable(p, config) }()
			}
			wg.Wait()
			artifact := d.reusable[p.ID].data
			for i, r := range readers {
				if errs[i] != nil {
					t.Fatal(errs[i])
				}
				data, err := io.ReadAll(r)
				if err != nil || !bytes.Equal(data, artifact) {
					t.Fatalf("reader %d corrupted: %v", i, err)
				}
			}
			r, err := d.Reusable(p, config)
			if err != nil {
				t.Fatal(err)
			}
			if &artifact[0] != &d.reusable[p.ID].data[0] {
				t.Fatal("cache hit rebuilt artifact")
			}
			// Keep an old reader alive while configuration and source changes replace
			// the bounded entry. Existing downloads must remain unchanged.
			config.OrgName = "New organization"
			if _, err := d.Reusable(p, config); err != nil {
				t.Fatal(err)
			}
			if &artifact[0] == &d.reusable[p.ID].data[0] || len(d.reusable) != 1 {
				t.Fatal("configuration did not replace cache entry")
			}
			changed := d.reusable[p.ID].data
			if err := os.WriteFile(path, makeBase("replacement executable"), 0755); err != nil {
				t.Fatal(err)
			}
			future := time.Now().Add(time.Second)
			if err := os.Chtimes(path, future, future); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Reusable(p, config); err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(changed, d.reusable[p.ID].data) || len(d.reusable) != 1 {
				t.Fatal("source change did not replace artifact")
			}
			original, err := io.ReadAll(r)
			if err != nil || !bytes.Equal(original, artifact) {
				t.Fatal("replacement changed active reader")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Reusable(p, config); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("missing base served stale cache: %v", err)
			}
		})
	}
}

func TestReusablePreparationFailureIsNotCached(t *testing.T) {
	p, _ := Lookup("linux-amd64")
	dir := t.TempDir()
	path := filepath.Join(dir, p.file)
	var stamped bytes.Buffer
	if err := stamp.Write(&stamped, []byte("ELF"), testStamp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, stamped.Bytes(), 0755); err != nil {
		t.Fatal(err)
	}
	d := New(dir)
	config := stamp.Stamp{ServerURL: testStamp.ServerURL}
	if r, err := d.Reusable(p, config); err == nil || r != nil || len(d.reusable) != 0 {
		t.Fatal("failed preparation returned or cached partial bytes")
	}
	if err := os.WriteFile(path, []byte("ELF"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Reusable(p, config); err != nil {
		t.Fatalf("failed preparation prevented retry: %v", err)
	}
	config.Code = "123456"
	if _, err := d.Reusable(p, config); err == nil {
		t.Fatal("reusable cache accepted a session code")
	}
}

// Benchmark the hot path with a large executable so per-download copies show
// up in B/op even when the number of allocations appears small.
func BenchmarkReusableCached(b *testing.B) {
	p, _ := Lookup("linux-amd64")
	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, p.file), bytes.Repeat([]byte("x"), 1<<20), 0755); err != nil {
		b.Fatal(err)
	}
	d := New(dir)
	config := stamp.Stamp{ServerURL: testStamp.ServerURL}
	if _, err := d.Reusable(p, config); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r, err := d.Reusable(p, config)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := r.Seek(0, io.SeekEnd); err != nil {
			b.Fatal(err)
		}
	}
}
