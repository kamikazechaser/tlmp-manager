package mods

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
)

type (
	// changingFS returns other bytes for a.txt after the first open.
	changingFS struct {
		fstest.MapFS

		opens int
	}

	// headTail keeps the first and the last bytes of a stream that is too large for memory.
	headTail struct {
		head, tail []byte
		n          uint64
	}
)

func md5hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func (c *changingFS) Open(name string) (fs.File, error) {
	if name == "a.txt" {
		if c.opens++; c.opens > 1 {
			c.MapFS["a.txt"] = &fstest.MapFile{Data: []byte("ho")}
		}
	}
	return c.MapFS.Open(name)
}

func (h *headTail) Write(p []byte) (int, error) {
	if len(h.head) < 64 {
		h.head = append(h.head, p[:min(len(p), 64-len(h.head))]...)
	}
	h.tail = append(h.tail, p[max(0, len(p)-512):]...)
	h.tail = h.tail[max(0, len(h.tail)-512):]
	h.n += uint64(len(p))
	return len(p), nil
}

func TestPackFolder(t *testing.T) {
	fsys := fstest.MapFS{
		"b/x.txt":    {Data: []byte("bee\n")},
		"a.txt":      {Data: []byte("hi")},
		"B/y":        {Data: []byte{}},
		"\u00fc.txt": {Data: []byte("uml")},
		"empty":      {Mode: fs.ModeDir},
	}
	var buf bytes.Buffer
	if err := packFolder(&buf, fsys); err != nil {
		t.Fatal(err)
	}

	// The golden sum comes from a separate implementation of the MODS.md format.
	if got := md5hex(buf.Bytes()); got != "936fcbdc402a2bf2db439a19da43104e" {
		t.Errorf("MD5 = %s, want the golden sum", got)
	}

	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil || !bytes.Equal(b, fsys[f.Name].Data) || f.Method != zip.Store {
			t.Errorf("%s: content %q, method %d, err %v", f.Name, b, f.Method, err)
		}
	}
	if want := []string{"B/y", "a.txt", "b/x.txt", "\u00fc.txt"}; !slices.Equal(names, want) {
		t.Errorf("entries = %q, want %q", names, want)
	}
}

func TestPackFolderChanged(t *testing.T) {
	fsys := &changingFS{MapFS: fstest.MapFS{"a.txt": {Data: []byte("hi")}}}
	if err := packFolder(io.Discard, fsys); !errors.Is(err, errChanged) {
		t.Errorf("packFolder() = %v, want errChanged", err)
	}
}

// TestPackFolderZip64 packs a sparse file of 0xFFFFFFFF bytes, the first size that needs Zip64.
func TestPackFolderZip64(t *testing.T) {
	if testing.Short() {
		t.Skip("reads 8 GiB")
	}
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(max32); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "z.txt"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out headTail
	if err := packFolder(&out, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	if out.n < max32 {
		t.Fatalf("packFolder() wrote %d bytes", out.n)
	}
	le := binary.LittleEndian

	local := out.head
	if le.Uint16(local[4:]) != 45 || le.Uint32(local[18:]) != max32 || le.Uint32(local[22:]) != max32 {
		t.Errorf("big.bin local header: version %d, sizes %#x %#x", le.Uint16(local[4:]), le.Uint32(local[18:]), le.Uint32(local[22:]))
	}
	extra := local[30+len("big.bin"):]
	if le.Uint16(extra) != 1 || le.Uint16(extra[2:]) != 16 || le.Uint64(extra[4:]) != max32 || le.Uint64(extra[12:]) != max32 {
		t.Errorf("big.bin local Zip64 extra = %x", extra[:20])
	}

	// The tail is the two central headers, the Zip64 end record and locator, and the classic end record.
	const bigCentral, smallCentral = 46 + 7 + 20, 46 + 5 + 12
	tail := out.tail[len(out.tail)-(bigCentral+smallCentral+56+20+22):]
	big, small, end64, loc, end := tail[:bigCentral], tail[bigCentral:bigCentral+smallCentral], tail[bigCentral+smallCentral:], tail[len(tail)-42:], tail[len(tail)-22:]
	cdOffset := out.n - uint64(len(tail))

	if le.Uint16(big[4:]) != 45 || le.Uint16(big[6:]) != 45 || le.Uint64(big[46+7+4:]) != max32 || le.Uint64(big[46+7+12:]) != max32 {
		t.Errorf("big.bin central header = %x", big)
	}
	if le.Uint16(small[4:]) != 45 || le.Uint32(small[42:]) != max32 || le.Uint16(small[46+5+2:]) != 8 || le.Uint64(small[46+5+4:]) != 30+7+20+max32 {
		t.Errorf("z.txt central header = %x", small)
	}
	if le.Uint32(end64) != 0x06064b50 || le.Uint64(end64[4:]) != 44 || le.Uint16(end64[12:]) != 45 || le.Uint64(end64[24:]) != 2 ||
		le.Uint64(end64[40:]) != bigCentral+smallCentral || le.Uint64(end64[48:]) != cdOffset {
		t.Errorf("Zip64 end record = %x", end64[:56])
	}
	if le.Uint32(loc) != 0x07064b50 || le.Uint64(loc[8:]) != cdOffset+bigCentral+smallCentral || le.Uint32(loc[16:]) != 1 {
		t.Errorf("Zip64 locator = %x", loc[:20])
	}
	if le.Uint16(end[10:]) != 2 || le.Uint32(end[12:]) != bigCentral+smallCentral || le.Uint32(end[16:]) != max32 {
		t.Errorf("end record = %x", end)
	}
}
