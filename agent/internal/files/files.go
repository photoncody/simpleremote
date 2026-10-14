// Package files handles file transfers over the "files" data channel.
//
// The tech can send files, which are saved in the user's Downloads/SimpleRemote
// folder, and browse and download files the user's account can read. Every
// transfer is reported to the user through Notify.
package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/photoncody/simpleremote/internal/proto"
)

// ChunkSize is the payload size of binary file messages.
const ChunkSize = 64 << 10

// Channel is the subset of a data channel the handler needs.
type Channel interface {
	SendText(string) error
	Send([]byte) error
	// WaitBuffered blocks until there is room to send more data.
	WaitBuffered(ctx context.Context) error
}

// Notice tells the user about a transfer.
type Notice struct {
	Direction string // "received" (tech -> user) or "sent" (user -> tech)
	Name      string
	Path      string
	Size      int64
	Err       error
}

// Handler serves file requests from the tech.
type Handler struct {
	ch      Channel
	notify  func(Notice)
	saveDir string
	home    string

	mu        sync.Mutex
	uploads   map[uint32]*upload
	downloads map[uint32]context.CancelFunc
	ctx       context.Context
	cancel    context.CancelFunc
}

type upload struct {
	f        *os.File
	path     string
	name     string
	size     int64
	received int64
}

// New creates a handler. saveDir is created on the first upload.
func New(ch Channel, saveDir string, notify func(Notice)) *Handler {
	home, _ := os.UserHomeDir()
	ctx, cancel := context.WithCancel(context.Background())
	return &Handler{ch: ch, notify: notify, saveDir: saveDir, home: home,
		uploads: map[uint32]*upload{}, downloads: map[uint32]context.CancelFunc{}, ctx: ctx, cancel: cancel}
}

// DefaultSaveDir returns ~/Downloads/SimpleRemote.
func DefaultSaveDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "SimpleRemote")
	}
	return filepath.Join(home, "Downloads", "SimpleRemote")
}

// Close aborts transfers in progress and removes partial uploads.
func (h *Handler) Close() {
	h.cancel()
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, u := range h.uploads {
		u.f.Close()
		os.Remove(u.path)
		delete(h.uploads, id)
	}
}

func (h *Handler) send(m proto.FileMsg) {
	b, _ := json.Marshal(m)
	_ = h.ch.SendText(string(b))
}

func (h *Handler) fail(id uint32, err error) {
	h.send(proto.FileMsg{T: proto.FileError, ID: id, Message: err.Error()})
}

// OnText handles a JSON message.
func (h *Handler) OnText(data []byte) {
	var m proto.FileMsg
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	switch m.T {
	case proto.FilePutBegin:
		h.putBegin(m)
	case proto.FilePutEnd:
		h.putEnd(m.ID)
	case proto.FileList:
		h.list(m.Path)
	case proto.FileGet:
		go h.get(m.ID, m.Path)
	case proto.FileCancel:
		h.cancelTransfer(m.ID)
	}
}

// OnBinary handles a file chunk.
func (h *Handler) OnBinary(data []byte) {
	id, chunk, err := proto.ParseFileChunk(data)
	if err != nil {
		return
	}
	h.mu.Lock()
	u := h.uploads[id]
	h.mu.Unlock()
	if u == nil {
		return
	}
	if u.received+int64(len(chunk)) > u.size {
		h.abortUpload(id, errors.New("received more data than announced"))
		return
	}
	if _, err := u.f.Write(chunk); err != nil {
		h.abortUpload(id, fmt.Errorf("could not write file: %w", err))
		return
	}
	u.received += int64(len(chunk))
}

// SafeName strips directories and characters that aren't allowed in file
// names on any of the supported systems.
func SafeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(strings.TrimSpace(name), ".")
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	if len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		name = name[:200-len(ext)] + ext
	}
	return name
}

// uniquePath finds a name in dir that isn't taken yet: "a.txt", "a (1).txt", ...
func uniquePath(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := filepath.Join(dir, candidate)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, p, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("too many files with the same name")
}

func (h *Handler) putBegin(m proto.FileMsg) {
	if m.Size < 0 {
		h.fail(m.ID, errors.New("invalid size"))
		return
	}
	if err := os.MkdirAll(h.saveDir, 0o755); err != nil {
		h.fail(m.ID, fmt.Errorf("could not create %s: %w", h.saveDir, err))
		return
	}
	f, p, err := uniquePath(h.saveDir, SafeName(m.Name))
	if err != nil {
		h.fail(m.ID, err)
		return
	}
	h.mu.Lock()
	h.uploads[m.ID] = &upload{f: f, path: p, name: filepath.Base(p), size: m.Size}
	h.mu.Unlock()
}

func (h *Handler) putEnd(id uint32) {
	h.mu.Lock()
	u := h.uploads[id]
	delete(h.uploads, id)
	h.mu.Unlock()
	if u == nil {
		return
	}
	err := u.f.Close()
	if err == nil && u.received != u.size {
		err = fmt.Errorf("transfer incomplete (%d of %d bytes)", u.received, u.size)
	}
	if err != nil {
		os.Remove(u.path)
		h.fail(id, err)
		h.notify(Notice{Direction: "received", Name: u.name, Err: err})
		return
	}
	h.send(proto.FileMsg{T: proto.FilePutDone, ID: id, Path: u.path})
	h.notify(Notice{Direction: "received", Name: u.name, Path: u.path, Size: u.size})
}

func (h *Handler) abortUpload(id uint32, err error) {
	h.mu.Lock()
	u := h.uploads[id]
	delete(h.uploads, id)
	h.mu.Unlock()
	if u == nil {
		return
	}
	u.f.Close()
	os.Remove(u.path)
	h.fail(id, err)
}

func (h *Handler) cancelTransfer(id uint32) {
	h.mu.Lock()
	cancel := h.downloads[id]
	delete(h.downloads, id)
	u := h.uploads[id]
	delete(h.uploads, id)
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if u != nil {
		u.f.Close()
		os.Remove(u.path)
	}
}

func (h *Handler) list(path string) {
	if path == "" {
		path = h.home
	}
	path = filepath.Clean(path)
	entries, err := os.ReadDir(path)
	if err != nil {
		h.fail(0, fmt.Errorf("could not open %s: %v", path, err))
		return
	}
	out := make([]proto.FileEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		fe := proto.FileEntry{Name: e.Name(), Dir: info.IsDir(), MTime: info.ModTime().Unix()}
		if !fe.Dir {
			fe.Size = info.Size()
		}
		out = append(out, fe)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	parent := filepath.Dir(path)
	if parent == path { // filesystem root; "" goes back to the home folder
		parent = ""
	}
	h.send(proto.FileMsg{T: proto.FileListing, Path: path, Parent: parent, Entries: out})
}

func (h *Handler) get(id uint32, path string) {
	ctx, cancel := context.WithCancel(h.ctx)
	h.mu.Lock()
	h.downloads[id] = cancel
	h.mu.Unlock()
	defer func() {
		cancel()
		h.mu.Lock()
		delete(h.downloads, id)
		h.mu.Unlock()
	}()

	f, err := os.Open(path)
	if err != nil {
		h.fail(id, fmt.Errorf("could not open file: %v", err))
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		h.fail(id, errors.New("not a regular file"))
		return
	}
	name := filepath.Base(path)
	h.send(proto.FileMsg{T: proto.FileGetBegin, ID: id, Name: name, Size: info.Size()})
	buf := make([]byte, ChunkSize)
	msg := make([]byte, 0, ChunkSize+proto.FileChunkHeaderLen)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if werr := h.ch.WaitBuffered(ctx); werr != nil {
				return
			}
			msg = proto.AppendFileChunk(msg[:0], id, buf[:n])
			if serr := h.ch.Send(msg); serr != nil {
				return
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			h.fail(id, fmt.Errorf("read failed: %v", err))
			return
		}
	}
	h.send(proto.FileMsg{T: proto.FileGetEnd, ID: id})
	h.notify(Notice{Direction: "sent", Name: name, Path: path, Size: info.Size()})
}
