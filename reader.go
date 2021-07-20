package wud

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hashicorp/go-multierror"
	"go4.org/readerutil"
)

const (
	multipart = "game_part"
)

type reader struct {
	r   readerutil.SizeReaderAt
	c   []io.Closer
	off int64
}

func OpenReader(name string) (WUDReadCloser, error) {
	f, err := fs.Open(name)
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		err = multierror.Append(err, f.Close())
		return nil, err
	}

	var sr readerutil.SizeReaderAt = io.NewSectionReader(f, 0, info.Size())
	files := []io.Closer{f}

	if filepath.Base(name) == fmt.Sprintf("%s1%s", multipart, Extension) {
		mr := []readerutil.SizeReaderAt{sr}
		for i := 2; true; i++ {
			if f, err = fs.Open(filepath.Join(filepath.Dir(name), fmt.Sprintf("%s%d%s", multipart, i, Extension))); err != nil {
				if os.IsNotExist(err) {
					break
				}
				for _, file := range files {
					err = multierror.Append(err, file.Close())
				}
				return nil, err
			}
			files = append(files, f)

			if info, err = f.Stat(); err != nil {
				for _, file := range files {
					err = multierror.Append(err, file.Close())
				}
				return nil, err
			}

			mr = append(mr, io.NewSectionReader(f, 0, info.Size()))
		}
		sr = readerutil.NewMultiReaderAt(mr...)
	}

	r := &reader{
		r: sr,
		c: files,
	}

	return r, nil
}

func (r *reader) Size() int64 {
	return r.r.Size()
}

func (r *reader) Close() (err error) {
	for _, c := range r.c {
		err = multierror.Append(err, c.Close())
	}
	return
}

func (r *reader) Read(p []byte) (n int, err error) {
	n, err = r.ReadAt(p, r.off)
	r.off += int64(n)
	if err == io.ErrUnexpectedEOF {
		err = io.EOF
		if n > 0 {
			err = nil
		}
	}
	return
}

func (r *reader) ReadAt(p []byte, off int64) (int, error) {
	return r.r.ReadAt(p, off)
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
		offset += r.Size()
	}
	if offset < 0 {
		return 0, errors.New("Seek: invalid offset")
	}
	r.off = offset
	return offset, nil
}
