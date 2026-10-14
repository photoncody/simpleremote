package dist

import (
	"archive/zip"
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

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
