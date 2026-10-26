package mods

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"slices"
	"strings"
	"time"
)

type (
	packFile struct {
		path    string
		size    int64
		modTime time.Time
	}

	zipEntry struct {
		name         string
		flags        uint16
		crc          uint32
		size, offset uint64
	}

	zipBuf []byte

	countWriter struct {
		w io.Writer
		n uint64
	}
)

const (
	max16 = 0xFFFF
	max32 = 0xFFFFFFFF

	localSig   = 0x04034b50
	centralSig = 0x02014b50
	end64Sig   = 0x06064b50
	locatorSig = 0x07064b50
	endSig     = 0x06054b50
	zip64ID    = 0x0001
	utf8Flag   = 0x0800
	// dosDate is 1980-01-01 and the time is 00:00:00, so the bytes are the same on every machine.
	dosDate = 0x0021
)

var (
	errChanged = errors.New("changed while it was being packed")
)

// packFolder writes fsys as the stored zip that the allowlist MD5 of a package folder describes.
// The launcher fixes every byte, so archive/zip cannot write it.
func packFolder(w io.Writer, fsys fs.FS) error {
	files, err := packFiles(fsys)
	if err != nil {
		return err
	}
	cw := &countWriter{w: w}
	entries := make([]zipEntry, 0, len(files))
	for _, f := range files {
		e, err := writeEntry(cw, fsys, f.path)
		if err != nil {
			return fmt.Errorf("packing %s: %w", f.path, err)
		}
		entries = append(entries, e)
	}
	return writeDirectory(cw, entries)
}

// packFiles returns the regular files in the byte order of their paths, which is the zip order.
func packFiles(fsys fs.FS) ([]packFile, error) {
	var files []packFile
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, packFile{path: p, size: info.Size(), modTime: info.ModTime()})
		return nil
	})
	slices.SortFunc(files, func(a, b packFile) int { return strings.Compare(a.path, b.path) })
	return files, err
}

// writeEntry reads the file twice, because the local header holds the CRC before the bytes.
func writeEntry(cw *countWriter, fsys fs.FS, name string) (zipEntry, error) {
	e := zipEntry{name: name, offset: cw.n}
	if strings.ContainsFunc(name, func(r rune) bool { return r >= 0x80 }) {
		e.flags = utf8Flag
	}
	crc, size, err := checksum(fsys, name)
	if err != nil {
		return e, err
	}
	e.crc, e.size = crc, uint64(size)

	if _, err := cw.Write(e.local()); err != nil {
		return e, err
	}
	f, err := fsys.Open(name)
	if err != nil {
		return e, err
	}
	defer func() { _ = f.Close() }()
	h := crc32.NewIEEE()
	n, err := io.Copy(cw, io.TeeReader(f, h))
	if err != nil {
		return e, err
	}
	if n != size || h.Sum32() != crc {
		return e, errChanged
	}
	return e, nil
}

func checksum(fsys fs.FS, name string) (uint32, int64, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	h := crc32.NewIEEE()
	n, err := io.Copy(h, f)
	return h.Sum32(), n, err
}

func (e zipEntry) local() []byte {
	version := uint16(10)
	var extra zipBuf
	if e.size >= max32 {
		version = 45
		extra = extra.u16(zip64ID).u16(16).u64(e.size).u64(e.size)
	}
	b := zipBuf(nil).u32(localSig).u16(version).u16(e.flags).u16(0).u16(0).u16(dosDate).
		u32(e.crc).u32(sat32(e.size)).u32(sat32(e.size)).u16(uint16(len(e.name))).u16(uint16(len(extra)))
	return append(append(b, e.name...), extra...)
}

func (e zipEntry) central() []byte {
	var fields zipBuf
	if e.size >= max32 {
		fields = fields.u64(e.size).u64(e.size)
	}
	if e.offset >= max32 {
		fields = fields.u64(e.offset)
	}
	made, version := uint16(20), uint16(10)
	var extra zipBuf
	if len(fields) > 0 {
		made, version = 45, 45
		extra = append(extra.u16(zip64ID).u16(uint16(len(fields))), fields...)
	}
	b := zipBuf(nil).u32(centralSig).u16(made).u16(version).u16(e.flags).u16(0).u16(0).u16(dosDate).
		u32(e.crc).u32(sat32(e.size)).u32(sat32(e.size)).u16(uint16(len(e.name))).u16(uint16(len(extra))).
		u16(0).u16(0).u16(0).u32(0).u32(sat32(e.offset))
	return append(append(b, e.name...), extra...)
}

func writeDirectory(cw *countWriter, entries []zipEntry) error {
	start := cw.n
	for _, e := range entries {
		if _, err := cw.Write(e.central()); err != nil {
			return err
		}
	}
	size, count := cw.n-start, uint64(len(entries))

	var b zipBuf
	if count >= max16 || size >= max32 || start >= max32 {
		b = b.u32(end64Sig).u64(44).u16(45).u16(45).u32(0).u32(0).u64(count).u64(count).u64(size).u64(start).
			u32(locatorSig).u32(0).u64(cw.n).u32(1)
	}
	b = b.u32(endSig).u16(0).u16(0).u16(uint16(min(count, max16))).u16(uint16(min(count, max16))).
		u32(sat32(size)).u32(sat32(start)).u16(0)
	_, err := cw.Write(b)
	return err
}

func sat32(v uint64) uint32 {
	return uint32(min(v, max32))
}

func (b zipBuf) u16(v uint16) zipBuf { return binary.LittleEndian.AppendUint16(b, v) }
func (b zipBuf) u32(v uint32) zipBuf { return binary.LittleEndian.AppendUint32(b, v) }
func (b zipBuf) u64(v uint64) zipBuf { return binary.LittleEndian.AppendUint64(b, v) }

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += uint64(n)
	return n, err
}
