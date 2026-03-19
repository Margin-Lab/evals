package runbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

func CanonicalBytes(b Bundle) ([]byte, error) {
	copy := b
	// Integrity is derived from the canonical payload and excluded from hash bytes.
	copy.Integrity = nil
	out, err := json.Marshal(copy)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical run bundle: %w", err)
	}
	return out, nil
}

func HashSHA256(b Bundle) (string, error) {
	data, err := CanonicalBytes(b)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func WithComputedIntegrity(b Bundle) (Bundle, error) {
	hash, err := HashSHA256(b)
	if err != nil {
		return Bundle{}, err
	}
	copy := b
	copy.Integrity = &Integrity{BundleHashSHA256: hash}
	return copy, nil
}
