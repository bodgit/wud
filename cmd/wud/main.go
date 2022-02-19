package main

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/bodgit/wud"
	"github.com/bodgit/wud/wux"
	"github.com/hashicorp/go-multierror"
	"github.com/spf13/afero"
	"github.com/urfave/cli/v2"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var fs = afero.NewOsFs()

func init() {
	cli.VersionFlag = &cli.BoolFlag{
		Name:    "version",
		Aliases: []string{"V"},
		Usage:   "print the version",
	}
}

func compressWUD(name, target string, verify bool) error {
	rc, err := wud.OpenReader(name)
	if err != nil {
		return err
	}
	defer rc.Close()

	if rc.Size() != int64(wud.UncompressedSize) {
		return fmt.Errorf("%s file is not %d bytes", wud.Extension, wud.UncompressedSize)
	}

	dst, err := fs.Create(target)
	if err != nil {
		return err
	}

	w, err := wux.NewWriter(dst, wud.SectorSize, wud.UncompressedSize)
	if err != nil {
		return multierror.Append(err, dst.Close())
	}

	var r io.Reader = rc
	h := sha1.New() // Good enough, this isn't cryptographic
	if verify {
		r = io.TeeReader(rc, h)
	}

	if _, err := io.Copy(w, r); err != nil {
		err = multierror.Append(err, w.Close())
		err = multierror.Append(err, dst.Close())
		return err
	}

	if err = w.Close(); err != nil {
		return err
	}
	if err = dst.Close(); err != nil {
		return err
	}

	if verify {
		sum := h.Sum(nil)

		f, err := fs.Open(target)
		if err != nil {
			return err
		}
		defer f.Close()

		r, err := wux.NewReader(f)
		if err != nil {
			return err
		}

		h.Reset()
		if _, err = io.Copy(h, r); err != nil {
			return err
		}

		if bytes.Compare(sum, h.Sum(nil)) != 0 {
			return errors.New("verification failed")
		}
	}

	return nil
}

func decompressWUX(name, target string) error {
	f, err := fs.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()

	r, err := wux.NewReader(f)
	if err != nil {
		return err
	}

	w, err := fs.Create(target)
	if err != nil {
		return err
	}
	defer w.Close()

	if _, err = io.Copy(w, r); err != nil {
		return err
	}

	return nil
}

func openFile(name string) (wud.ReadCloser, error) {
	f, err := fs.Open(name)
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

func extractWUD(name, common, game, directory string) error {
	rc, err := openFile(name)
	if err != nil {
		return err
	}
	defer rc.Close()

	commonKey, err := afero.ReadFile(fs, common)
	if err != nil {
		return err
	}

	gameKey, err := afero.ReadFile(fs, game)
	if err != nil {
		return err
	}

	w, err := wud.NewWUD(rc, commonKey, gameKey)
	if err != nil {
		return err
	}

	if fi, err := fs.Stat(directory); err != nil || !fi.IsDir() {
		if err != nil {
			return err
		}
		return errors.New("not a directory")
	}

	if err = w.Extract(directory); err != nil {
		return err
	}

	return nil
}

func main() {
	app := cli.NewApp()

	app.Name = "wud"
	app.Usage = "Wii U disc image utility"
	app.Version = fmt.Sprintf("%s, commit %s, built at %s", version, commit, date)

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	app.Commands = []*cli.Command{
		{
			Name:        "compress",
			Usage:       "Compress a " + wud.Extension + " file into a " + wux.Extension + " file",
			Description: "",
			ArgsUsage:   "FILE",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					cli.ShowCommandHelpAndExit(c, c.Command.Name, 1)
				}

				file := c.Args().First()

				target := c.Path("output")
				if target == "" {
					target = strings.TrimSuffix(file, filepath.Ext(file)) + wux.Extension
				}

				if err := compressWUD(file, target, c.Bool("verify")); err != nil {
					return err
				}

				return nil
			},
			Flags: []cli.Flag{
				&cli.PathFlag{
					Name:    "output",
					Aliases: []string{"o"},
					Usage:   "write output to `FILE`",
				},
				&cli.BoolFlag{
					Name:  "verify",
					Usage: "verify the file matches",
					Value: true,
				},
			},
		},
		{
			Name:        "decompress",
			Usage:       "Decompress a " + wux.Extension + " file back to a " + wud.Extension + " file",
			Description: "",
			ArgsUsage:   "FILE",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					cli.ShowCommandHelpAndExit(c, c.Command.Name, 1)
				}

				file := c.Args().First()

				target := c.Path("output")
				if target == "" {
					target = strings.TrimSuffix(file, filepath.Ext(file)) + wud.Extension
				}

				if err := decompressWUX(file, target); err != nil {
					return err
				}

				return nil
			},
			Flags: []cli.Flag{
				&cli.PathFlag{
					Name:    "output",
					Aliases: []string{"o"},
					Usage:   "write output to `FILE`",
				},
			},
		},
		{
			Name:        "extract",
			Usage:       "Extract .cert, .tik, .tmd & .app files from a " + wud.Extension + " or " + wux.Extension + " file",
			Description: "",
			ArgsUsage:   "FILE [KEY]...",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					cli.ShowCommandHelpAndExit(c, c.Command.Name, 1)
				}

				file := c.Args().Get(0)

				common := c.Args().Get(1)
				if common == "" {
					common = filepath.Join(filepath.Dir(file), wud.CommonKeyFile)
				}

				game := c.Args().Get(2)
				if game == "" {
					game = filepath.Join(filepath.Dir(common), wud.GameKeyFile)
				}

				if err := extractWUD(file, common, game, c.Path("directory")); err != nil {
					return err
				}

				return nil
			},
			Flags: []cli.Flag{
				&cli.PathFlag{
					Name:    "directory",
					Aliases: []string{"d"},
					Usage:   "extract to `DIRECTORY`",
					Value:   cwd,
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
