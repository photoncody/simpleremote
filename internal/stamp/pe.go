package stamp

import (
	"encoding/binary"
	"errors"
)

// AppendPE appends a trailer to a Windows executable without invalidating
// an Authenticode signature.
//
// Authenticode hashes the whole file except the certificate table, which is
// stored at the end of the file. For signed files the trailer is therefore
// placed inside the certificate table: it is appended to the last
// WIN_CERTIFICATE entry and the table sizes are updated. The file still ends
// with the trailer, so Parse and ReadFile work unchanged. Unsigned files get
// a plain trailer.
func AppendPE(base, trailer []byte) ([]byte, error) {
	secDir, secOff, secSize, err := certTable(base)
	if err != nil {
		return nil, err
	}
	if secSize == 0 {
		return append(append(make([]byte, 0, len(base)+len(trailer)), base...), trailer...), nil
	}
	if int(secOff)+int(secSize) != len(base) {
		return nil, errors.New("certificate table is not at the end of the file")
	}

	// Entries in the certificate table are 8-byte aligned. Padding goes in
	// front of the trailer, where the stamp reader ignores it.
	pad := (8 - len(trailer)%8) % 8
	add := pad + len(trailer)

	// Find the last WIN_CERTIFICATE entry.
	last := -1
	for off := int(secOff); off+8 <= len(base); {
		n := int(binary.LittleEndian.Uint32(base[off:]))
		if n < 8 {
			return nil, errors.New("corrupt certificate table")
		}
		last = off
		off += (n + 7) &^ 7
	}
	if last < 0 {
		return nil, errors.New("empty certificate table")
	}

	out := make([]byte, 0, len(base)+add)
	out = append(out, base...)
	out = append(out, make([]byte, pad)...)
	out = append(out, trailer...)
	binary.LittleEndian.PutUint32(out[secDir+4:], secSize+uint32(add))
	// The last entry now runs to the end of the file.
	binary.LittleEndian.PutUint32(out[last:], uint32(len(out)-last))
	return out, nil
}

// certTable locates the Security data directory of a PE file. It returns the
// file offset of the directory entry and the table's offset and size.
func certTable(b []byte) (dirOff int, off, size uint32, err error) {
	if len(b) < 0x40 || b[0] != 'M' || b[1] != 'Z' {
		return 0, 0, 0, errors.New("not a PE file")
	}
	pe := int(binary.LittleEndian.Uint32(b[0x3C:]))
	if pe+24 > len(b) || string(b[pe:pe+4]) != "PE\x00\x00" {
		return 0, 0, 0, errors.New("not a PE file")
	}
	opt := pe + 24
	if opt+2 > len(b) {
		return 0, 0, 0, errors.New("truncated PE header")
	}
	var dd int
	switch binary.LittleEndian.Uint16(b[opt:]) {
	case 0x10b: // PE32
		dd = opt + 96
	case 0x20b: // PE32+
		dd = opt + 112
	default:
		return 0, 0, 0, errors.New("unknown PE optional header")
	}
	dirOff = dd + 4*8 // IMAGE_DIRECTORY_ENTRY_SECURITY
	if dirOff+8 > len(b) {
		return 0, 0, 0, errors.New("truncated PE header")
	}
	return dirOff, binary.LittleEndian.Uint32(b[dirOff:]), binary.LittleEndian.Uint32(b[dirOff+4:]), nil
}
