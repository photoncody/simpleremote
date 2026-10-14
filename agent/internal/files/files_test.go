package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/photoncody/simpleremote/internal/proto"
)

type fakeChan struct {
	mu   sync.Mutex
	text []proto.FileMsg
	bin  [][]byte
}

func (f *fakeChan) SendText(s string) error {
	var m proto.FileMsg
	_ = json.Unmarshal([]byte(s), &m)
	f.mu.Lock()
	f.text = append(f.text, m)
	f.mu.Unlock()
	return nil
}

func (f *fakeChan) Send(b []byte) error {
	f.mu.Lock()
	f.bin = append(f.bin, append([]byte(nil), b...))
	f.mu.Unlock()
	return nil
}

func (f *fakeChan) WaitBuffered(context.Context) error { return nil }

func (f *fakeChan) last() proto.FileMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.text) == 0 {
		return proto.FileMsg{}
	}
	return f.text[len(f.text)-1]
}

func msg(m proto.FileMsg) []byte { b, _ := json.Marshal(m); return b }

func TestUpload(t *testing.T) {
	dir := t.TempDir()
	ch := &fakeChan{}
	var notices []Notice
	h := New(ch, dir, func(n Notice) { notices = append(notices, n) })

	h.OnText(msg(proto.FileMsg{T: proto.FilePutBegin, ID: 1, Name: "../../evil/notes.txt", Size: 11}))
	h.OnBinary(proto.AppendFileChunk(nil, 1, []byte("hello ")))
	h.OnBinary(proto.AppendFileChunk(nil, 1, []byte("world")))
	h.OnText(msg(proto.FileMsg{T: proto.FilePutEnd, ID: 1}))

	done := ch.last()
	if done.T != proto.FilePutDone || filepath.Dir(done.Path) != dir || filepath.Base(done.Path) != "notes.txt" {
		t.Fatalf("put_done = %+v", done)
	}
	data, _ := os.ReadFile(done.Path)
	if string(data) != "hello world" {
		t.Fatalf("file contents = %q", data)
	}
	if len(notices) != 1 || notices[0].Direction != "received" {
		t.Fatalf("notices = %+v", notices)
	}

	// Same name again gets a numbered copy.
	h.OnText(msg(proto.FileMsg{T: proto.FilePutBegin, ID: 2, Name: "notes.txt", Size: 1}))
	h.OnBinary(proto.AppendFileChunk(nil, 2, []byte("x")))
	h.OnText(msg(proto.FileMsg{T: proto.FilePutEnd, ID: 2}))
	if p := ch.last().Path; filepath.Base(p) != "notes (1).txt" {
		t.Fatalf("second upload path = %s", p)
	}
}

func TestUploadOverrun(t *testing.T) {
	dir := t.TempDir()
	ch := &fakeChan{}
	h := New(ch, dir, func(Notice) {})
	h.OnText(msg(proto.FileMsg{T: proto.FilePutBegin, ID: 1, Name: "a.bin", Size: 2}))
	h.OnBinary(proto.AppendFileChunk(nil, 1, []byte("too long")))
	if m := ch.last(); m.T != proto.FileError {
		t.Fatalf("expected error, got %+v", m)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("partial file left behind: %v", entries)
	}
}

func TestListAndGet(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bee"), 0o644)
	ch := &fakeChan{}
	var notices []Notice
	var mu sync.Mutex
	h := New(ch, t.TempDir(), func(n Notice) { mu.Lock(); notices = append(notices, n); mu.Unlock() })

	h.OnText(msg(proto.FileMsg{T: proto.FileList, Path: dir}))
	l := ch.last()
	if l.T != proto.FileListing || len(l.Entries) != 2 || !l.Entries[0].Dir || l.Entries[1].Size != 3 {
		t.Fatalf("listing = %+v", l)
	}

	h.OnText(msg(proto.FileMsg{T: proto.FileGet, ID: 9, Path: filepath.Join(dir, "b.txt")}))
	deadline := time.Now().Add(2 * time.Second)
	for ch.last().T != proto.FileGetEnd && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ch.last().T != proto.FileGetEnd || len(ch.bin) != 1 {
		t.Fatalf("get: last=%+v chunks=%d", ch.last(), len(ch.bin))
	}
	id, data, _ := proto.ParseFileChunk(ch.bin[0])
	if id != 9 || string(data) != "bee" {
		t.Fatalf("chunk = %d %q", id, data)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(notices) != 1 || notices[0].Direction != "sent" {
		t.Fatalf("notices = %+v", notices)
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":          "report.pdf",
		`C:\Users\x\evil.exe`: "evil.exe",
		"../../etc/passwd":    "passwd",
		"a<b>c:d?.txt":        "a_b_c_d_.txt",
		"..":                  "file",
		"trailing.dots...":    "trailing.dots",
	} {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}
