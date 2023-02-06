package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/bodgit/plumbing"
	"github.com/bodgit/wud"
	"github.com/bodgit/wud/wux"
	"github.com/hashicorp/go-multierror"
	"github.com/schollz/progressbar/v3"
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

func compress(src, dst string, verbose bool) error {
	if dst == "" {
		if ext := filepath.Ext(src); ext == wux.Extension {
			return fmt.Errorf("source file %s already has %s extension", src, wux.Extension)
		}

		dst = strings.TrimSuffix(src, wud.Extension) + wux.Extension
	}

	rc, err := wud.OpenReader(src)
	if err != nil {
		return err
	}
	defer rc.Close()

	if rc.Size() != int64(wud.UncompressedSize) {
		return fmt.Errorf("%s file is not %d bytes", wud.Extension, wud.UncompressedSize)
	}

	var r io.Reader = rc

	if verbose {
		pb := progressbar.DefaultBytes(rc.Size())
		r = io.TeeReader(r, pb)
	}

	f, err := fs.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	w, err := wux.NewWriter(f, wud.SectorSize, wud.UncompressedSize)
	if err != nil {
		return err
	}
	defer w.Close()

	_, err = io.Copy(w, r)

	return err
}

func decompress(src, dst string, verbose bool) error {
	if dst == "" {
		if ext := filepath.Ext(src); ext == wud.Extension {
			return fmt.Errorf("source file %s already has %s extension", src, wud.Extension)
		}

		dst = strings.TrimSuffix(src, wux.Extension) + wud.Extension
	}

	f, err := fs.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	r, err := wux.NewReader(f)
	if err != nil {
		return err
	}

	var w io.WriteCloser

	w, err = fs.Create(dst)
	if err != nil {
		return err
	}

	if verbose {
		pb := progressbar.DefaultBytes(r.Size())
		w = plumbing.MultiWriteCloser(w, plumbing.NopWriteCloser(pb))
	}

	defer w.Close()

	_, err = io.Copy(w, r)

	return err
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

func extract(name, common, game, directory string) error {
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
			ArgsUsage:   "SOURCE [TARGET]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					cli.ShowCommandHelpAndExit(c, c.Command.Name, 1)
				}

				return compress(c.Args().Get(0), c.Args().Get(1), c.Bool("verbose"))
			},
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:    "verbose",
					Aliases: []string{"v"},
					Usage:   "increase verbosity",
				},
			},
		},
		{
			Name:        "decompress",
			Usage:       "Decompress a " + wux.Extension + " file back to a " + wud.Extension + " file",
			Description: "",
			ArgsUsage:   "SOURCE [TARGET]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					cli.ShowCommandHelpAndExit(c, c.Command.Name, 1)
				}

				return decompress(c.Args().Get(0), c.Args().Get(1), c.Bool("verbose"))
			},
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:    "verbose",
					Aliases: []string{"v"},
					Usage:   "increase verbosity",
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

				if err := extract(file, common, game, c.Path("directory")); err != nil {
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
