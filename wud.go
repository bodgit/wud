package wud

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/connesc/cipherio"
	"github.com/spf13/afero"
	"go4.org/readerutil"
)

const (
	Extension               = ".wud"
	SectorSize       uint32 = 0x8000
	UncompressedSize uint64 = 25025314816
	CommonKeyFile           = "common.key"
	GameKeyFile             = "game.key"
	keySize                 = 16
	magic            uint32 = 0xcca6e67b
)

var fs = afero.NewOsFs()

type WUDReader interface {
	Size() int64
	io.Reader
	io.ReaderAt
	io.Seeker
}

type WUDReadCloser interface {
	WUDReader
	io.Closer
}

type partitionTable map[string]int64

func newPartitionTable(r io.Reader) (partitionTable, error) {
	pt := make(partitionTable)

	// Read partition table header
	pth := struct {
		Magic         uint32
		_             uint32
		Checksum      [sha1.Size]byte
		NumPartitions uint32
	}{}
	if err := binary.Read(r, binary.BigEndian, &pth); err != nil {
		return nil, err
	}
	if pth.Magic != magic {
		return nil, errors.New("bad magic")
	}

	// Skip to offset 0x800
	if _, err := io.CopyN(ioutil.Discard, r, 0x800-int64(unsafe.Sizeof(pth))); err != nil {
		return nil, err
	}

	h := sha1.New()
	tr := io.TeeReader(r, h)

	pte := struct {
		Name   [0x1f]byte
		_      byte
		Offset uint32
		_      [0x5c]byte
	}{}
	for i := 0; i < int(pth.NumPartitions); i++ {
		if err := binary.Read(tr, binary.BigEndian, &pte); err != nil {
			return nil, err
		}
		pt[string(bytes.TrimRight(pte.Name[:], "\x00"))] = int64(pte.Offset) * int64(SectorSize)
	}

	// Read the rest of the sector to calculate the SHA-1
	if _, err := io.Copy(ioutil.Discard, tr); err != nil {
		return nil, err
	}

	// Check the checksum is correct
	if bytes.Compare(h.Sum(nil), pth.Checksum[:]) != 0 {
		return nil, errors.New("bad TOC checksum")
	}

	return pt, nil
}

func (pt partitionTable) findPartition(prefix string) (string, int64, error) {
	for k, v := range pt {
		if strings.HasPrefix(k, prefix) {
			return k, v, nil
		}
	}
	return "", 0, errors.New("can't find partition")
}

const (
	titleCert = "title.cert"
	titleTik  = "title.tik"
	titleTmd  = "title.tmd"
)

type file struct {
	iv     []byte
	offset int64
	size   int64
}

func (f file) reader(r io.ReaderAt, block cipher.Block) io.Reader {
	sr := io.NewSectionReader(r, f.offset, int64((int(f.size)+block.BlockSize()-1)&(-block.BlockSize())))
	cbc := cipherio.NewBlockReader(sr, cipher.NewCBCDecrypter(block, f.iv))
	return io.LimitReader(cbc, f.size)
}

type WUD struct {
	r      io.ReaderAt
	common cipher.Block
	game   cipher.Block
	title  string
	pt     partitionTable
	files  map[string]file
}

func NewWUD(r readerutil.SizeReaderAt, commonKey, gameKey []byte) (*WUD, error) {
	w := new(WUD)
	w.r = r

	if r.Size() != int64(UncompressedSize) {
		return nil, errors.New("wrong size")
	}

	var err error

	if len(commonKey) != keySize {
		return nil, errors.New("wrong common key size")
	}
	w.common, err = aes.NewCipher(commonKey)
	if err != nil {
		return nil, err
	}

	if len(gameKey) != keySize {
		return nil, errors.New("wrong game key size")
	}
	w.game, err = aes.NewCipher(gameKey)
	if err != nil {
		return nil, err
	}

	// Read title
	sr := io.NewSectionReader(w.r, 0, 10)
	title := make([]byte, 10)
	if _, err = io.ReadFull(sr, title); err != nil {
		return nil, err
	}
	w.title = string(title)

	// Fourth sector
	sr = io.NewSectionReader(w.r, 3*int64(SectorSize), int64(SectorSize))
	cbc := cipherio.NewBlockReader(sr, cipher.NewCBCDecrypter(w.game, make([]byte, w.game.BlockSize())))

	// Read the partition table
	if w.pt, err = newPartitionTable(cbc); err != nil {
		return nil, err
	}

	si, ok := w.pt["SI"]
	if !ok {
		return nil, errors.New("can't find SI partition")
	}

	// SI partition, skipping the first sector
	sr = io.NewSectionReader(w.r, si+int64(SectorSize), int64(SectorSize))
	cbc = cipherio.NewBlockReader(sr, cipher.NewCBCDecrypter(w.game, make([]byte, w.game.BlockSize())))

	fh := struct {
		Magic                uint32
		FileOffsetFactor     uint32
		SecondaryHeaderCount uint32
		_                    [20]byte
	}{}
	if err = binary.Read(cbc, binary.BigEndian, &fh); err != nil {
		return nil, err
	}
	if fh.Magic != 0x46535400 { // "FST"+0
		return nil, errors.New("bad magic")
	}

	// Skip over secondary headers
	if _, err = io.CopyN(ioutil.Discard, cbc, int64(fh.SecondaryHeaderCount)<<5); err != nil {
		return nil, err
	}

	// Decrypt the rest of the sector
	b := new(bytes.Buffer)
	if _, err = io.Copy(b, cbc); err != nil {
		return nil, err
	}
	br := bytes.NewReader(b.Bytes())

	fe := struct {
		TypeName            uint32 // 8 + 24
		Offset              uint32
		Size                uint32
		Flags               uint16
		StorageClusterIndex uint16
	}{}
	if err = binary.Read(br, binary.BigEndian, &fe); err != nil {
		return nil, err
	}
	if fe.TypeName>>24 != 1 || fe.TypeName&0xffffff != 0 {
		return nil, errors.New("bad root entry")
	}

	entries := int(fe.Size)
	nameTableOffset := int64(entries * int(unsafe.Sizeof(fe)))
	w.files = make(map[string]file)

	for i := 1; i < entries; i++ {
		if err = binary.Read(br, binary.BigEndian, &fe); err != nil {
			return nil, err
		}

		// Not a file
		if fe.TypeName>>24 != 0 {
			continue
		}

		// Remember where we are
		pos, err := br.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, err
		}

		// Seek to the filename
		if _, err = br.Seek(nameTableOffset+int64(fe.TypeName&0xffffff), io.SeekStart); err != nil {
			return nil, err
		}

		// Read until null
		rawBytes, err := bufio.NewReader(br).ReadBytes(0)
		if err != nil {
			return nil, err
		}
		// XXX Any encoding here such as japanese.ShiftJIS?
		filename := string(rawBytes[:len(rawBytes)-1])

		// Seek back to where we were
		if _, err = br.Seek(pos, io.SeekStart); err != nil {
			return nil, err
		}

		if _, ok = w.files[filename]; ok {
			continue
		}

		f := file{
			iv:     make([]byte, w.game.BlockSize()),
			offset: si + 2*int64(SectorSize) + int64(fe.Offset*fh.FileOffsetFactor),
			size:   int64(fe.Size),
		}
		binary.BigEndian.PutUint64(f.iv[8:], uint64(fe.Offset*fh.FileOffsetFactor>>16))

		if filename == titleCert {
			w.files[titleCert] = f
			continue
		}

		lr := f.reader(w.r, w.game)

		offsets := map[string]int64{
			titleTik: 0x1dc,
			titleTmd: 0x18c,
		}

		if offset, ok := offsets[filename]; ok {
			if _, err = io.CopyN(ioutil.Discard, lr, offset); err != nil {
				return nil, err
			}
		} else {
			continue
		}

		var tid uint32
		if err = binary.Read(lr, binary.BigEndian, &tid); err != nil {
			return nil, err
		}
		if tid != 0x50000 {
			continue
		}

		w.files[filename] = f
	}

	return w, nil
}

func (w *WUD) extractFile(filename, target string) (io.Reader, io.Closer, error) {
	f, ok := w.files[filename]
	if !ok {
		return nil, nil, errors.New("file not found")
	}
	wc, err := fs.Create(target)
	if err != nil {
		return nil, nil, err
	}
	return io.TeeReader(f.reader(w.r, w.game), wc), wc, nil
}

func (w *WUD) Extract(directory string) error {
	directory = filepath.Join(directory, w.title)

	if err := fs.MkdirAll(directory, os.ModePerm|os.ModeDir); err != nil {
		return err
	}

	tr, c, err := w.extractFile(titleTmd, filepath.Join(directory, titleTmd))
	if err != nil {
		return err
	}
	defer c.Close()

	tmd := struct {
		SignatureType    uint32
		Signature        [0x100]byte
		_                [0x3c]byte
		Issuer           [0x40]byte
		Version          byte
		CACRLVersion     byte
		SignerCRLVersion byte
		_                byte
		SystemVersion    uint64
		TitleID          uint64
		TitleType        uint32
		GroupID          uint16
		_                [62]byte
		AccessRights     uint32
		TitleVersion     uint16
		ContentCount     uint16
		BootIndex        uint16
		_                [2]byte
		SHA2             [sha256.Size]byte

		ContentInfos [64]struct {
			IndexOffset  uint16
			CommandCount uint16
			SHA2         [sha256.Size]byte
		}
	}{}

	if err = binary.Read(tr, binary.BigEndian, &tmd); err != nil {
		return err
	}

	contents := make([]struct {
		ID    uint32
		Index uint16
		Type  uint16
		Size  uint64
		SHA2  [sha256.Size]byte
	}, tmd.ContentCount)

	if err = binary.Read(tr, binary.BigEndian, &contents); err != nil {
		return err
	}

	if _, err = io.Copy(ioutil.Discard, tr); err != nil {
		return err
	}

	_, gm, err := w.pt.findPartition(fmt.Sprintf("GM%016X", tmd.TitleID))
	if err != nil {
		return err
	}

	sr := io.NewSectionReader(w.r, gm, int64(SectorSize))
	if _, err = io.CopyN(ioutil.Discard, sr, 0x10); err != nil {
		return err
	}
	var headerCount uint32
	if err = binary.Read(sr, binary.BigEndian, &headerCount); err != nil {
		return err
	}
	if _, err = io.CopyN(ioutil.Discard, sr, 0x2c+int64(headerCount)<<2); err != nil {
		return err
	}
	// sr is now pointing to the first hash

	if tr, c, err = w.extractFile(titleTik, filepath.Join(directory, titleTik)); err != nil {
		return err
	}
	defer c.Close()
	if _, err = io.CopyN(ioutil.Discard, tr, 0x1bf); err != nil {
		return err
	}
	key := make([]byte, keySize)
	if _, err = io.ReadFull(tr, key); err != nil {
		return err
	}
	if _, err = io.CopyN(ioutil.Discard, tr, 0x1dc-(aes.BlockSize+0x1bf)); err != nil {
		return err
	}
	iv := make([]byte, w.common.BlockSize())
	if _, err = io.ReadFull(tr, iv[:8]); err != nil {
		return err
	}
	if _, err = io.Copy(ioutil.Discard, tr); err != nil {
		return err
	}
	cipher.NewCBCDecrypter(w.common, iv).CryptBlocks(key, key)

	tik, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	iv = make([]byte, tik.BlockSize())
	binary.BigEndian.PutUint16(iv[:2], contents[0].Index)

	f, err := fs.Create(filepath.Join(directory, fmt.Sprintf("%08x.app", contents[0].ID)))
	if err != nil {
		return err
	}
	defer f.Close()

	tr = io.TeeReader(io.NewSectionReader(w.r, gm+int64(SectorSize), int64((int(contents[0].Size)+tik.BlockSize()-1)&(-tik.BlockSize()))), f)
	cbc := cipherio.NewBlockReader(tr, cipher.NewCBCDecrypter(tik, iv))

	app := make([]struct {
		Offset uint32
		Size   uint32
		TID    uint64
		GID    uint32
		_      [0xc]byte
	}, tmd.ContentCount)

	if _, err = io.CopyN(ioutil.Discard, cbc, 0x20); err != nil {
		return err
	}
	if err = binary.Read(cbc, binary.BigEndian, &app); err != nil {
		return err
	}
	if _, err = io.Copy(ioutil.Discard, cbc); err != nil {
		return err
	}

	for i := 1; i < int(tmd.ContentCount); i++ {
		f, err = fs.Create(filepath.Join(directory, fmt.Sprintf("%08x.app", contents[i].ID)))
		if err != nil {
			return err
		}
		defer f.Close()

		if _, err = io.Copy(f, io.NewSectionReader(w.r, gm+int64(app[i].Offset)*int64(SectorSize), int64(contents[i].Size))); err != nil {
			return err
		}

		if contents[i].Type&0x2 != 0 {
			f, err = fs.Create(filepath.Join(directory, fmt.Sprintf("%08x.h3", contents[i].ID)))
			if err != nil {
				return err
			}
			defer f.Close()

			if _, err = io.CopyN(f, sr, int64(20*(contents[i].Size/0x10000000+1))); err != nil {
				return err
			}
		}
	}

	if tr, c, err = w.extractFile(titleCert, filepath.Join(directory, titleCert)); err != nil {
		return err
	}
	defer c.Close()
	if _, err = io.Copy(ioutil.Discard, tr); err != nil {
		return err
	}

	return nil
}
