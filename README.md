[![Go Report Card](https://goreportcard.com/badge/github.com/bodgit/wud)](https://goreportcard.com/report/github.com/bodgit/wud)
[![GoDoc](https://godoc.org/github.com/bodgit/wud?status.svg)](https://godoc.org/github.com/bodgit/wud)
![Go version](https://img.shields.io/badge/Go-1.17-brightgreen.svg)
![Go version](https://img.shields.io/badge/Go-1.16-brightgreen.svg)

# Nintendo Wii-U disc images

The [github.com/bodgit/wud](https://godoc.org/github.com/bodgit/wud) package
provides read access to Wii-U disc images, such as those created by the
[github.com/FIX94/wudump](https://github.com/FIX94/wudump) homebrew.

How to read a disc image:

```golang
package main

import (
        "io"
        "os"

        "github.com/bodgit/wud"
        "github.com/bodgit/wud/wux"
        "github.com/hashicorp/go-multierror"
)

// openFile will first try and open name as a compressed image, then as
// a regular or split image.
func openFile(name string) (wud.ReadCloser, error) {
        f, err := os.Open(name)
        if err != nil {
                return nil, err
        }

        if rc, err := wux.NewReadCloser(f); err != nil {
                if err != wux.ErrBadMagic {
                        return nil, multierror.Append(err, f.Close())
                }
                if err = f.Close(); err != nil {
                        return nil, err
                }
        } else {
                return rc, nil
        }

        return wud.OpenReader(name)
}

func main() {
        rc, err := openFile(os.Args[1])
        if err != nil {
                panic(err)
        }
        defer rc.Close()

        commonKey, err := os.ReadFile(os.Args[2])
        if err != nil {
                panic(err)
        }

        gameKey, err := os.ReadFile(os.Args[3])
        if err != nil {
                panic(err)
        }

        w, err := wud.NewWUD(rc, commonKey, gameKey)
        if err != nil {
                panic(err)
        }

        if err = w.Extract(os.Args[4]); err != nil {
                panic(err)
        }
}
```

To compress a disc image:

```golang
package main

import (
	"io"
	"os"

	"github.com/bodgit/wud"
	"github.com/bodgit/wud/wux"
)

func main() {
	rc, err := wud.OpenReader(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer rc.Close()

	f, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer f.Close()

	w, err := wux.NewWriter(f, wud.SectorSize, wud.UncompressedSize)
	if err != nil {
		panic(err)
	}

	if _, err = io.Copy(w, r); err != nil {
		panic(err)
	}
}
```

And the reverse decompression operation:

```golang
package main

import (
	"io"
	"os"

	"github.com/bodgit/wud/wux"
)

func main() {
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer f.Close()

	r, err := wux.NewReader(f)
	if err != nil {
		panic(err)
	}

	w, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer w.Close()

	if _, err = io.Copy(w, r); err != nil {
		panic(err)
	}
}
```
