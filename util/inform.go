package util

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"math/rand"
	"strconv"

	"github.com/golang/snappy"
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

type InformBuilder struct {
	packet            InformPD
	tag               []byte
	aad               []byte
	Key               []byte
	compressedPayload []byte
	snappy            bool
	zlib              bool
	encrypted         bool
	aesgcm            bool
}

func NewInformBuilder(packet []byte) (*InformBuilder, error) {
	var (
		ib     *InformBuilder
		ipd    InformPD
		err    error
		tInt64 int64
	)
	ib = &InformBuilder{}

	ipd.Magic = int32(big.NewInt(0).SetBytes(packet[0:4]).Uint64())

	tInt64, err = strconv.ParseInt(hex.EncodeToString(packet[4:8]), 16, 32)
	if err != nil {
		return nil, err
	}

	ipd.Version = int32(tInt64)
	ipd.Mac = hex.EncodeToString(packet[8:14])
	tInt64, err = strconv.ParseInt(hex.EncodeToString(packet[14:16]), 16, 16)
	if err != nil {
		return nil, err
	}

	ipd.Flags = int16(tInt64)

	ipd.InitVector = packet[16:32]
	tInt64, err = strconv.ParseInt(hex.EncodeToString(packet[32:36]), 16, 32)
	if err != nil {
		return nil, err
	}

	ipd.DataVersion = int32(tInt64)
	tInt64, err = strconv.ParseInt(hex.EncodeToString(packet[36:40]), 16, 32)
	if err != nil {
		return nil, err
	}

	ipd.DataLength = int32(tInt64)
	if int(ipd.DataLength) > len(packet[40:]) {
		return nil, ErrDataLength
	}
	ipd.Payload = packet[40 : 40+ipd.DataLength]

	ipd.AAD = packet[:40]

	ipd.Tag = packet[:len(packet)-16]

	ib.Init(ipd)
	return ib, err
}

func (p *InformBuilder) Init(ipd InformPD) {
	p.packet = ipd

	p.parseFlags()
}

func (p InformBuilder) Uncompress() (io.Reader, error) {
	if p.zlib {
		b := bytes.NewReader(p.compressedPayload)
		return zlib.NewReader(b)

	} else if p.snappy {
		b := bytes.NewReader(p.compressedPayload)
		return snappy.NewReader(b), nil
	}

	return nil, fmt.Errorf("Unimplemented compression")
}

func (p InformBuilder) GetMac() string {
	return p.packet.Mac
}

func (p InformBuilder) String() string {
	var h [32]byte
	h = sha256.Sum256(p.packet.Payload)
	p.packet.Payload = h[:]
	h = sha256.Sum256(p.aad)
	p.aad = h[:]
	h = sha256.Sum256(p.tag)
	p.tag = h[:]
	return fmt.Sprintf("%#v", p)

}

func (p *InformBuilder) Decrypt() {
	if len(p.Key) == 0 {
		p.Key = MASTER_KEY
	}
	if !p.encrypted {
		log.Println("Note: packet was not marked encrypted")
		p.compressedPayload = p.packet.Payload
		return
	}
	if p.aesgcm {
		p.decryptGCM()
	} else {
		p.decryptCBC()
	}
}

func (p *InformBuilder) Encrypt(b []byte) error {
	if len(p.Key) == 0 {
		p.Key = MASTER_KEY
	}
	if !p.encrypted {
		log.Println("Note: packet was not marked encrypted")
		p.packet.Payload = p.compressedPayload
		return nil
	}
	if p.aesgcm {
		return p.encryptGCM(b)
	} else {
		return p.encryptCBC(b)
	}
}

func (p InformBuilder) BuildResponse(r any) ([]byte, error) {
	var (
		b   []byte
		err error
		buf = new(bytes.Buffer)
	)

	if len(p.packet.InitVector) != 16 {
		p.packet.InitVector = make([]byte, 16)
	}
	p.parseFlags()

	b, err = json.Marshal(r)
	if err != nil {
		return nil, err
	}

	p.compressedPayload = b
	err = p.Encrypt(b)
	if err != nil {
		return nil, err
	}

	err = binary.Write(buf, binary.BigEndian, p.packet.Magic)
	if err != nil {
		return nil, err
	}

	err = binary.Write(buf, binary.BigEndian, int32(len(p.packet.Payload)))
	if err != nil {
		return nil, err
	}

	b, err = hex.DecodeString(p.packet.Mac)
	if err != nil {
		return nil, err
	}

	_, err = buf.Write(b)
	if err != nil {
		return nil, err
	}

	err = binary.Write(buf, binary.BigEndian, p.packet.Flags)
	if err != nil {
		return nil, err
	}

	_, err = buf.Write(p.packet.InitVector)
	if err != nil {
		return nil, err
	}

	err = binary.Write(buf, binary.BigEndian, p.packet.DataVersion)
	if err != nil {
		return nil, err
	}

	err = binary.Write(buf, binary.BigEndian, int32(len(p.packet.Payload)))
	if err != nil {
		return nil, err
	}

	_, err = buf.Write(p.packet.Payload)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (p *InformBuilder) parseFlags() {
	p.encrypted = (p.packet.Flags & 0x1) == 1
	p.zlib = (p.packet.Flags & 0x2) == 2
	p.snappy = (p.packet.Flags & 0x4) == 4
	p.aesgcm = (p.packet.Flags & 0x8) == 8
}

func (p *InformBuilder) decryptGCM() {
	var block cipher.Block
	var err error

	block, err = aes.NewCipher(p.Key)
	if err != nil {
		log.Printf("error initializing cipher: %s", err)
		return
	}

	aesGCM, err := cipher.NewGCMWithNonceSize(block, 16)
	if err != nil {
		log.Printf("error initializing gcm: %s", err)
		return
	}

	p.compressedPayload, err = aesGCM.Open(nil, p.packet.InitVector, p.packet.Payload, p.aad)
	if err != nil {
		log.Printf("error decrypting: %s", err)
	}
}

func (p *InformBuilder) decryptCBC() {
	var block cipher.Block
	var err error

	if len(p.Key) != 16 {
		log.Println("invalid key")
		return
	}
	if len(p.packet.Payload)%aes.BlockSize != 0 {
		log.Println("data is not padded")
		return
	}

	block, err = aes.NewCipher(p.Key)
	if err != nil {
		log.Printf("error initializing cipher: %s", err)
		return
	}
	p.compressedPayload = make([]byte, len(p.packet.Payload))
	cbc := cipher.NewCBCDecrypter(block, p.packet.InitVector)
	cbc.CryptBlocks(p.compressedPayload, p.packet.Payload)
}

func (p *InformBuilder) encryptGCM(b []byte) error {
	block, err := aes.NewCipher(p.Key)
	if err != nil {
		return err
	}

	nonce := make([]byte, 12)
	if n, err := rand.Read(nonce); err != nil || n != 12 {
		return fmt.Errorf("error generating nonce")
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	p.packet.Payload = aesgcm.Seal(nil, nonce, b, nil)
	return nil
}

func (p *InformBuilder) encryptCBC(b []byte) error {
	block, _ := aes.NewCipher(p.Key)
	plainText := PKCS5Padding(b, aes.BlockSize, len(b))
	p.packet.Payload = make([]byte, len(plainText))
	n, err := rand.Read(p.packet.InitVector)
	if err != nil || n != 16 {
		return fmt.Errorf("error creating IV")
	}

	mode := cipher.NewCBCEncrypter(block, p.packet.InitVector)
	mode.CryptBlocks(p.packet.Payload, plainText)
	return nil
}

func PKCS5Padding(ciphertext []byte, blockSize int, after int) []byte {
	padding := (blockSize - len(ciphertext)%blockSize)
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}
