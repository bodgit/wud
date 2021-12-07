package wux

import (
	"encoding/binary"
	"errors"
	"io"
	"unsafe"

	"github.com/bodgit/wud"
	"go4.org/readerutil"
)

type reader struct {
	r          io.ReaderAt
	base       int64
	off        int64
	limit      int64
	sectorSize int64
	table      []uint32
}

var (
	ErrBadMagic = errors.New("bad magic")
)

func NewReader(ra io.ReaderAt) (wud.Reader, error) {
	r := new(reader)
	r.r = ra

	h := header{}
	const headerSize = int64(unsafe.Sizeof(h))

	// Create SectionReader for just the header
	sr := io.NewSectionReader(r.r, 0, headerSize)

	// Read the header and sanity check it
	if err := binary.Read(sr, binary.LittleEndian, &h); err != nil {
		return nil, err
	}
	if h.Magic[0] != magic0 || h.Magic[1] != magic1 {
		return nil, ErrBadMagic
	}
	if h.SectorSize < 0x100 || h.SectorSize >= 0x10000000 {
		return nil, errors.New("bad sector size")
	}

	r.limit = int64(h.UncompressedSize)
	r.sectorSize = int64(h.SectorSize)

	// Calculate the number of sectors in the uncompressed image
	tableSize := (r.limit + r.sectorSize - 1) / r.sectorSize

	// Recreate SectionReader for the index table
	sr = io.NewSectionReader(r.r, headerSize, tableSize<<2)

	// Read in table
	r.table = make([]uint32, tableSize)
	if err := binary.Read(sr, binary.LittleEndian, &r.table); err != nil {
		return nil, err
	}

	// Calculate start of sectors, rounded up to the next whole sector
	r.base = (headerSize + tableSize<<2 + r.sectorSize - 1) & (-r.sectorSize)

	return r, nil
}

func (r *reader) Size() int64 {
	return r.limit
}

func (r *reader) newSizeReaderAt(l, off int64) readerutil.SizeReaderAt {
	sr := []readerutil.SizeReaderAt{}
	for l > 0 {
		sectorOffset := off % r.sectorSize
		sectorIndex := off / r.sectorSize
		limit := r.sectorSize - sectorOffset
		if limit > l {
			limit = l
		}
		sr = append(sr, io.NewSectionReader(r.r, r.base+int64(r.table[sectorIndex])*r.sectorSize+sectorOffset, limit))
		l -= sr[len(sr)-1].Size()
		off += sr[len(sr)-1].Size()
	}
	return readerutil.NewMultiReaderAt(sr...)
}

func (r *reader) Read(p []byte) (n int, err error) {
	if r.off >= r.limit {
		return 0, io.EOF
	}
	if max := r.limit - r.off; int64(len(p)) > max {
		p = p[0:max]
	}
	n, err = r.newSizeReaderAt(int64(len(p)), r.off).ReadAt(p, 0)
	r.off += int64(n)
	return
}

func (r *reader) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 || off >= r.limit {
		return 0, io.EOF
	}
	if max := r.limit - off; int64(len(p)) > max {
		p = p[0:max]
		n, err = r.newSizeReaderAt(int64(len(p)), off).ReadAt(p, 0)
		if err == nil {
			err = io.EOF
		}
		return n, err
	}
	return r.newSizeReaderAt(int64(len(p)), off).ReadAt(p, 0)
}

func (r *reader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	default:
		return 0, errors.New("Seek: invalid whence")
	case io.SeekStart:
		break
	case io.SeekCurrent:
		offset += r.off
	case io.SeekEnd:
		offset += r.limit
	}
	if offset < 0 {
		return 0, errors.New("Seek: invalid offset")
	}
	r.off = offset
	return offset, nil
}
