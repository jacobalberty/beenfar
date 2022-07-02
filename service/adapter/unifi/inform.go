package unifi

import (
	"fmt"
)

var (
	// md5sum of "ubnt"
	MASTER_KEY = []byte{0xba, 0x86, 0xf2, 0xbb, 0xe1, 0x07, 0xc7, 0xc5, 0x7e, 0xb5, 0xf2, 0x69, 0x07, 0x75, 0xc7, 0x12}

	ErrDataLength = fmt.Errorf("Data length is larger than packet size")
)

type InformPD struct {
	// Layout of the packet:
	Magic       int32  // must be 1414414933
	Version     int32  // int32
	Mac         string // 6 bytes
	Flags       int16  // encrypted, compressed, snappy, aesgcm
	InitVector  []byte // 16 bytes
	DataVersion int32  //  must be < 1
	DataLength  int32
	Payload     []byte

	// These fields are used for crypto
	// AAD is the header of the packet
	AAD []byte
	// Tag is the last 16 bytes of the packet
	Tag []byte
}
