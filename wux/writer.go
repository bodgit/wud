package wux

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"io/ioutil"
	"unsafe"
)

type writer struct {
	w          io.WriteSeeker
	b          *bytes.Buffer
	h          hash.Hash
	err        error
	m          map[string]uint32
	off        int64
	limit      int64
	sectorSize int64
	unique     uint32
	sector     int
	table      []uint32
}

// NewWriter returns an io.WriteCloser that compresses and writes to ws in sectorSize chunks.
func NewWriter(ws io.WriteSeeker, sectorSize uint32, uncompressedSize uint64) (io.WriteCloser, error) {
	w := &writer{
		w: ws,
		b: new(bytes.Buffer),
		h: sha1.New(),
		m: make(map[string]uint32),
	}

	// Just to be sure
	if _, err := w.w.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	h := header{
		Magic:            [2]uint32{magic0, magic1},
		SectorSize:       sectorSize,
		UncompressedSize: uncompressedSize,
	}
	const headerSize = int64(unsafe.Sizeof(h))

	// Write out header
	if err := binary.Write(w.w, binary.LittleEndian, &h); err != nil {
		return nil, err
	}

	w.limit = int64(h.UncompressedSize)
	w.sectorSize = int64(h.SectorSize)

	// Calculate the number of sectors in the uncompressed image
	tableSize := (w.limit + w.sectorSize - 1) / w.sectorSize
	w.table = make([]uint32, tableSize)

	// Calculate start of sectors, rounded up to the next whole sector
	off := (headerSize + tableSize<<2 + w.sectorSize - 1) & (-w.sectorSize)

	// Seek to the start of the sectors
	if _, err := w.w.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}

	return w, nil
}

func (w *writer) Write(p []byte) (n int, err error) {
	if w.err != nil {
		return 0, w.err
	}

	// Append new bytes to the buffer
	n, _ = w.b.Write(p)
	w.off += int64(n)

	// We have at least a sectors worth of data
	for int64(w.b.Len()) >= w.sectorSize {
		// Calculate the digest of the sector
		w.h.Reset()
		_, _ = w.h.Write(w.b.Bytes()[0:w.sectorSize])
		k := string(w.h.Sum(nil))

		v, ok := w.m[k]

		// Never seen this sector before, assign it the next index
		if !ok {
			v = w.unique
			w.unique++
			w.m[k] = v
		}

		// Record which index this sector uses
		w.table[w.sector] = v
		w.sector++

		// Append the sector to the underyling writer, or drop it if
		// we've seen it before
		var writer io.Writer = ioutil.Discard
		if !ok {
			writer = w.w
		}
		if _, err := io.CopyN(writer, w.b, w.sectorSize); err != nil {
			w.err = err
			return n, err
		}
	}

	return n, nil
}

func (w *writer) Close() error {
	if w.err != nil {
		return w.err
	}

	if w.b.Len() != 0 || w.off != w.limit {
		return errors.New("wux: not enough data written")
	}

	h := header{}
	const headerSize = int64(unsafe.Sizeof(h))

	if _, err := w.w.Seek(headerSize, io.SeekStart); err != nil {
		return err
	}
	if err := binary.Write(w.w, binary.LittleEndian, &w.table); err != nil {
		return err
	}

	return nil
}
