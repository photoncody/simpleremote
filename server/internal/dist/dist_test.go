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
	for _, mode := range []string{"unsigned", "signed", "legacy-marker", "wrong-server", "stamped-signed", "stamped-unsigned"} {
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
			err := stampZip(&out, src.Bytes(), stamp.Stamp{ServerURL: testStamp.ServerURL, OrgName: testStamp.OrgName})
			if mode == "legacy-marker" || mode == "wrong-server" || mode == "stamped-signed" || mode == "stamped-unsigned" {
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
				if mode == "signed" {
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
