package util

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"strconv"

	"github.com/golang/snappy"
)

var (
	// md5sum of "ubnt"
	MASTER_KEY = []byte{0xba, 0x86, 0xf2, 0xbb, 0xe1, 0x07, 0xc7, 0xc5, 0x7e, 0xb5, 0xf2, 0x69, 0x07, 0x75, 0xc7, 0x12}
)

type InformPD struct {
	key               []byte
	magic             []byte
	packetVersion     string
	mac               string
	flags             int64
	encrypted         bool
	zlib              bool
	snappy            bool
	aesgcm            bool
	initVector        []byte
	payloadVersion    string
	payloadLength     int64
	aad               []byte
	payload           []byte
	tag               []byte
	compressedPayload []byte
}

func NewInformPD(packet []byte) (*InformPD, error) {
	ipd := &InformPD{}
	err := ipd.Init(packet)
	return ipd, err
}

func (p *InformPD) Init(packet []byte) (err error) {
	log.Printf("Packet length: %d", len(packet))

	// Need to support pulling per device keys from database
	p.key = MASTER_KEY

	p.magic = packet[0:4]
	p.packetVersion = hex.EncodeToString(packet[4:8])
	p.mac = fmt.Sprintf("%x", packet[8:14])
	p.flags, err = strconv.ParseInt(hex.EncodeToString(packet[14:16]), 16, 64)
	if err != nil {
		return
	}

	p.encrypted = (p.flags & 0x1) == 1
	p.zlib = (p.flags & 0x2) == 2
	p.snappy = (p.flags & 0x4) == 4
	p.aesgcm = (p.flags & 0x8) == 8

	p.initVector = packet[16:32]
	p.payloadVersion = hex.EncodeToString(packet[32:36])
	p.payloadLength, err = strconv.ParseInt(hex.EncodeToString(packet[36:40]), 16, 32)
	if err != nil {
		return
	}

	p.aad = packet[:40]
	p.payload = packet[40 : 40+p.payloadLength]
	p.tag = packet[:len(packet)-16]

	p.dump()
	p.decrypt()

	return
}

func (p InformPD) Uncompress() (io.Reader, error) {
	if p.zlib {
		b := bytes.NewReader(p.compressedPayload)
		return zlib.NewReader(b)

	} else if p.snappy {
		b := bytes.NewReader(p.compressedPayload)
		return snappy.NewReader(b), nil
	}

	return nil, fmt.Errorf("Unimplemented compression")
}

func (p InformPD) dump() {
	var h [16]byte
	h = md5.Sum(p.payload)
	p.payload = h[:]
	h = md5.Sum(p.aad)
	p.aad = h[:]
	h = md5.Sum(p.tag)
	p.tag = h[:]
	log.Printf("%+v", p)

}

func (p *InformPD) decrypt() {
	if !p.encrypted {
		log.Println("Note: packet was not marked encrypted")
		p.compressedPayload = p.payload
		return
	}
	if p.aesgcm {
		p.decryptGCM()
	} else {
		p.decryptCBC()
	}
}

func (p *InformPD) decryptGCM() {
	var block cipher.Block
	var err error

	block, err = aes.NewCipher(p.key)
	if err != nil {
		log.Printf("error initializing cipher: %s", err)
		return
	}

	aesGCM, err := cipher.NewGCMWithNonceSize(block, 16)
	if err != nil {
		log.Printf("error initializing gcm: %s", err)
		return
	}

	p.compressedPayload, err = aesGCM.Open(nil, p.initVector, p.payload, p.aad)
	if err != nil {
		log.Printf("error decrypting: %s", err)
	}
}

func (p *InformPD) decryptCBC() {
	var block cipher.Block
	var err error

	if len(p.key) != 16 {
		log.Println("invalid key")
		return
	}
	if len(p.payload)%aes.BlockSize != 0 {
		log.Println("data is not padded")
		return
	}

	block, err = aes.NewCipher(p.key)
	if err != nil {
		log.Printf("error initializing cipher: %s", err)
		return
	}
	p.compressedPayload = make([]byte, len(p.payload))
	cbc := cipher.NewCBCDecrypter(block, p.initVector)
	cbc.CryptBlocks(p.compressedPayload, p.payload)
}
