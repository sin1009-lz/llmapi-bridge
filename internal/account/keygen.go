package account

import (
	"crypto/rand"
	"encoding/hex"
)

func GenerateAccountKey() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return "sk-bridge-" + hex.EncodeToString(bytes)
}
