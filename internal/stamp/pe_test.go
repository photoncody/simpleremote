package stamp

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// fakePE builds a minimal PE32+ image with an optional certificate table.
func fakePE(cert []byte) []byte {
	b := make([]byte, 0x200)
	b[0], b[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(b[0x3C:], 0x80)
	copy(b[0x80:], "PE\x00\x00")
	opt := 0x80 + 24
	binary.LittleEndian.PutUint16(b[opt:], 0x20b)
	if cert != nil {
		dir := opt + 112 + 4*8
		binary.LittleEndian.PutUint32(b[dir:], uint32(len(b)))
		binary.LittleEndian.PutUint32(b[dir+4:], uint32(len(cert)))
		b = append(b, cert...)
	}
	return b
}

func winCert(payload int) []byte {
	n := 8 + payload
	c := make([]byte, (n+7)&^7)
	binary.LittleEndian.PutUint32(c, uint32(n))
	binary.LittleEndian.PutUint16(c[4:], 0x0200)
	binary.LittleEndian.PutUint16(c[6:], 0x0002)
	return c
}

func TestAppendPEUnsigned(t *testing.T) {
	base := fakePE(nil)
	tr, _ := Trailer(Stamp{ServerURL: "https://x", Code: "123456789"})
	out, err := AppendPE(base, tr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, append(append([]byte{}, base...), tr...)) {
		t.Fatal("unsigned PE should get a plain trailer")
	}
}

func TestAppendPESigned(t *testing.T) {
	cert := winCert(100) // 108 bytes, padded to 112
	base := fakePE(cert)
	s := Stamp{ServerURL: "https://support.example.com", Code: "987654321"}
	tr, _ := Trailer(s)
	out, err := AppendPE(base, tr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(out)
	if err != nil || got != s {
		t.Fatalf("Parse = %+v, %v", got, err)
	}
	dir, off, size, _ := certTable(out)
	_ = dir
	if int(off)+int(size) != len(out) {
		t.Fatalf("certificate table (%d+%d) doesn't reach end of file (%d)", off, size, len(out))
	}
	if size%8 != 0 {
		t.Fatalf("certificate table size %d not 8-byte aligned", size)
	}
	if l := binary.LittleEndian.Uint32(out[off:]); int(off)+int(l) != len(out) {
		t.Fatalf("last WIN_CERTIFICATE length %d doesn't reach end of file", l)
	}
	// Everything Authenticode hashes (before the table) is unchanged, except
	// the directory entry, which Authenticode also skips.
	if !bytes.Equal(out[:dir], base[:dir]) || !bytes.Equal(out[dir+8:off], base[dir+8:off]) {
		t.Fatal("hashed region changed")
	}
}

func TestAppendPERejectsGarbage(t *testing.T) {
	if _, err := AppendPE([]byte("not a pe"), []byte("x")); err == nil {
		t.Fatal("expected error")
	}
}
