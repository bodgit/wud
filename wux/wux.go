/*
Package wux implements compression of Nintendo Wii-U disc images. The technique
deduplicates the original disc image on a sector-by-sector basis and relies on
the fact that despite the disc image being of a fixed size of around 23 GB, the
majority of that space will be unused.
*/
package wux

const (
	// Extension is the conventional file extension used
	Extension        = ".wux"
	magic0    uint32 = 0x30585557 // "WUX0"
	magic1    uint32 = 0x1099d02e
)

// The original tool read/wrote this using fread/fwrite so there's padding involved
type header struct {
	Magic            [2]uint32
	SectorSize       uint32
	_                uint32
	UncompressedSize uint64
	Flags            uint32
	_                uint32
}
