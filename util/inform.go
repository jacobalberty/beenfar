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
)

type InformPD struct {
	payloadVersion    string
	packetVersion     string
	Mac               string
	tag               []byte
	payload           []byte
	aad               []byte
	Key               []byte
	initVector        []byte
	compressedPayload []byte
	payloadLength     int64
	flags             int16
	magic             int32
	snappy            bool
	zlib              bool
	encrypted         bool
	aesgcm            bool
}

func NewInformPD(packet []byte) (*InformPD, error) {
	ipd := &InformPD{}
	err := ipd.Init(packet)
	return ipd, err
}

func (p *InformPD) Init(packet []byte) (err error) {
	p.magic = int32(big.NewInt(0).SetBytes(packet[0:4]).Uint64())
	p.packetVersion = hex.EncodeToString(packet[4:8])
	p.Mac = hex.EncodeToString(packet[8:14])
	flagtmp, err := strconv.ParseInt(hex.EncodeToString(packet[14:16]), 16, 16)
	if err != nil {
		return
	}
	p.flags = int16(flagtmp)

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

func (p InformPD) String() string {
	var h [32]byte
	h = sha256.Sum256(p.payload)
	p.payload = h[:]
	h = sha256.Sum256(p.aad)
	p.aad = h[:]
	h = sha256.Sum256(p.tag)
	p.tag = h[:]
	return fmt.Sprintf("%#v", p)

}

func (p *InformPD) Decrypt() {
	if len(p.Key) == 0 {
		p.Key = MASTER_KEY
	}
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

func (p *InformPD) Encrypt(b []byte) error {
	if len(p.Key) == 0 {
		p.Key = MASTER_KEY
	}
	if !p.encrypted {
		log.Println("Note: packet was not marked encrypted")
		p.payload = p.compressedPayload
		return nil
	}
	if p.aesgcm {
		return p.encryptGCM(b)
	} else {
		return p.encryptCBC(b)
	}
}

func (p InformPD) BuildResponse(r any) ([]byte, error) {
	var err error

	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}

	p.compressedPayload = b
	err = p.Encrypt(b)
	if err != nil {
		return nil, err
	}
	buf := new(bytes.Buffer)

	err = binary.Write(buf, binary.BigEndian, p.magic)
	if err != nil {
		return nil, err
	}
	b, err = hex.DecodeString(p.packetVersion)
	if err != nil {
		return nil, err
	}
	_, err = buf.Write(b)
	if err != nil {
		return nil, err
	}
	b, err = hex.DecodeString(p.Mac)
	if err != nil {
		return nil, err
	}
	_, err = buf.Write(b)
	if err != nil {
		return nil, err
	}
	err = binary.Write(buf, binary.BigEndian, p.flags)
	if err != nil {
		return nil, err
	}
	_, err = buf.Write(p.initVector)
	if err != nil {
		return nil, err
	}
	b, err = hex.DecodeString(p.payloadVersion)
	if err != nil {
		return nil, err
	}
	_, err = buf.Write(b)
	if err != nil {
		return nil, err
	}
	err = binary.Write(buf, binary.BigEndian, int32(len(p.payload)))
	if err != nil {
		return nil, err
	}
	_, err = buf.Write(p.payload)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (p *InformPD) decryptGCM() {
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

	p.compressedPayload, err = aesGCM.Open(nil, p.initVector, p.payload, p.aad)
	if err != nil {
		log.Printf("error decrypting: %s", err)
	}
}

func (p *InformPD) decryptCBC() {
	var block cipher.Block
	var err error

	if len(p.Key) != 16 {
		log.Println("invalid key")
		return
	}
	if len(p.payload)%aes.BlockSize != 0 {
		log.Println("data is not padded")
		return
	}

	block, err = aes.NewCipher(p.Key)
	if err != nil {
		log.Printf("error initializing cipher: %s", err)
		return
	}
	p.compressedPayload = make([]byte, len(p.payload))
	cbc := cipher.NewCBCDecrypter(block, p.initVector)
	cbc.CryptBlocks(p.compressedPayload, p.payload)
}

func (p *InformPD) encryptGCM(b []byte) error {
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
	p.payload = aesgcm.Seal(nil, nonce, b, nil)
	return nil
}

func (p *InformPD) encryptCBC(b []byte) error {
	block, _ := aes.NewCipher(p.Key)
	plainText := PKCS5Padding(b, aes.BlockSize, len(b))
	p.payload = make([]byte, len(plainText))
	n, err := rand.Read(p.initVector)
	if err != nil || n != 16 {
		return fmt.Errorf("error creating IV")
	}

	mode := cipher.NewCBCEncrypter(block, p.initVector)
	mode.CryptBlocks(p.payload, plainText)
	return nil
}

func PKCS5Padding(ciphertext []byte, blockSize int, after int) []byte {
	padding := (blockSize - len(ciphertext)%blockSize)
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}
