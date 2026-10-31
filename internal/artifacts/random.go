package artifacts

import (
	"crypto/rand"
	"encoding/hex"
)

func randomName() (string, error) {
	var b [16]byte
	_, e := rand.Read(b[:])
	return ".kit-write-" + hex.EncodeToString(b[:]), e
}
