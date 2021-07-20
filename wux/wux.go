package wux

const (
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
