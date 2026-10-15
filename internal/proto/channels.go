package proto

import (
	"encoding/binary"
	"errors"
)

// ---- video channel ---------------------------------------------------------
//
// The agent sends the screen as JPEG tiles. Each binary message is either a
// tile or a frame-end marker:
//
//	tile:      'T' seq:u32 x:u16 y:u16 w:u16 h:u16 screenW:u16 screenH:u16 jpeg...
//	frame end: 'F' seq:u32
//
// All integers are big-endian. The browser acknowledges each frame end with a
// control message {t:"ack", seq} so the agent can limit frames in flight.

const (
	VideoTile     byte = 'T'
	VideoFrameEnd byte = 'F'

	tileHeaderLen = 1 + 4 + 2*6
)

// TileHeader describes one JPEG tile.
type TileHeader struct {
	Seq              uint32
	X, Y, W, H       uint16
	ScreenW, ScreenH uint16
}

// AppendTile encodes a tile message.
func AppendTile(dst []byte, h TileHeader, jpeg []byte) []byte {
	dst = append(dst, VideoTile)
	dst = binary.BigEndian.AppendUint32(dst, h.Seq)
	for _, v := range []uint16{h.X, h.Y, h.W, h.H, h.ScreenW, h.ScreenH} {
		dst = binary.BigEndian.AppendUint16(dst, v)
	}
	return append(dst, jpeg...)
}

// FrameEnd encodes a frame-end marker.
func FrameEnd(seq uint32) []byte {
	b := []byte{VideoFrameEnd, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(b[1:], seq)
	return b
}

// ParseTile decodes a tile message. It is used by tests; the browser has its
// own decoder.
func ParseTile(b []byte) (TileHeader, []byte, error) {
	if len(b) < tileHeaderLen || b[0] != VideoTile {
		return TileHeader{}, nil, errors.New("not a tile message")
	}
	u16 := func(off int) uint16 { return binary.BigEndian.Uint16(b[off:]) }
	h := TileHeader{
		Seq: binary.BigEndian.Uint32(b[1:]),
		X:   u16(5), Y: u16(7), W: u16(9), H: u16(11),
		ScreenW: u16(13), ScreenH: u16(15),
	}
	return h, b[tileHeaderLen:], nil
}

// ---- input channel ---------------------------------------------------------

// Input is a pointer or keyboard event from the tech. Pointer coordinates are
// normalized to 0..1 within the display currently being shown.
type Input struct {
	T  string  `json:"t"` // mm, md, mu, wh, kd, ku
	X  float64 `json:"x,omitempty"`
	Y  float64 `json:"y,omitempty"`
	B  int     `json:"b,omitempty"`  // 0 left, 1 middle, 2 right
	DX float64 `json:"dx,omitempty"` // wheel delta in CSS pixels
	DY float64 `json:"dy,omitempty"`
	C  string  `json:"c,omitempty"` // KeyboardEvent.code
}

// Input event types.
const (
	InputMouseMove = "mm"
	InputMouseDown = "md"
	InputMouseUp   = "mu"
	InputWheel     = "wh"
	InputKeyDown   = "kd"
	InputKeyUp     = "ku"
)

// ---- control channel -------------------------------------------------------

// Display describes one monitor on the agent's computer.
type Display struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Primary bool   `json:"primary"`
}

// Control is a JSON message on the control channel, in either direction.
type Control struct {
	T string `json:"t"`

	// displays (agent -> tech)
	Displays []Display `json:"displays,omitempty"`
	Current  int       `json:"current,omitempty"`

	// display (tech -> agent)
	ID int `json:"id,omitempty"`

	// ack (tech -> agent)
	Seq uint32 `json:"seq,omitempty"`

	// quality (tech -> agent), 10..95
	Quality int `json:"quality,omitempty"`

	// clip, chat (both directions)
	Text string `json:"text,omitempty"`

	// clip_sync (tech -> agent)
	On bool `json:"on,omitempty"`

	// elevated (agent -> tech)
	Elevated bool `json:"elevated,omitempty"`
}

// Control message types.
const (
	CtlDisplays = "displays"
	CtlDisplay  = "display"
	CtlRefresh  = "refresh"
	CtlAck      = "ack"
	CtlQuality  = "quality"
	CtlClip     = "clip"
	CtlClipSync = "clip_sync"
	CtlChat     = "chat"
	CtlState    = "state"  // agent -> tech: Elevated
	CtlNotice   = "notice" // agent -> tech: a problem to show the tech (Text)
)

// ---- files channel ---------------------------------------------------------
//
// JSON text messages carry requests and metadata. Binary messages carry file
// data as: id:u32 bytes...

// FileMsg is a JSON message on the files channel.
type FileMsg struct {
	T       string      `json:"t"`
	ID      uint32      `json:"id"`
	Name    string      `json:"name,omitempty"`
	Path    string      `json:"path,omitempty"`
	Parent  string      `json:"parent,omitempty"`
	Size    int64       `json:"size,omitempty"`
	Entries []FileEntry `json:"entries,omitempty"`
	Message string      `json:"message,omitempty"`
}

// FileEntry is one item in a directory listing.
type FileEntry struct {
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

// File message types.
const (
	FilePutBegin = "put_begin" // tech -> agent: upload starts
	FilePutEnd   = "put_end"   // tech -> agent: upload finished
	FilePutDone  = "put_done"  // agent -> tech: saved (Path set)
	FileList     = "list"      // tech -> agent: list Path ("" = home)
	FileListing  = "listing"   // agent -> tech
	FileGet      = "get"       // tech -> agent: download Path
	FileGetBegin = "get_begin" // agent -> tech
	FileGetEnd   = "get_end"   // agent -> tech
	FileCancel   = "cancel"    // either side
	FileError    = "error"     // either side
)

// FileChunkHeaderLen is the size of the id prefix on binary file chunks.
const FileChunkHeaderLen = 4

// AppendFileChunk encodes a binary file chunk.
func AppendFileChunk(dst []byte, id uint32, data []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, id)
	return append(dst, data...)
}

// ParseFileChunk decodes a binary file chunk.
func ParseFileChunk(b []byte) (uint32, []byte, error) {
	if len(b) < FileChunkHeaderLen {
		return 0, nil, errors.New("short file chunk")
	}
	return binary.BigEndian.Uint32(b), b[FileChunkHeaderLen:], nil
}
