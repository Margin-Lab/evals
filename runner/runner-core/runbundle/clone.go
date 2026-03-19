package runbundle

import (
	"encoding/json"
	"time"
)

func CloneForRerunExact(b Bundle, newBundleID string, createdAt time.Time, originRunID string) Bundle {
	copy := deepCopyBundle(b)
	copy.BundleID = newBundleID
	copy.CreatedAt = createdAt
	copy.Source.Kind = SourceKindRunSnapshot
	copy.Source.OriginRunID = originRunID
	copy.Integrity = nil
	return copy
}

func deepCopyBundle(b Bundle) Bundle {
	body, err := json.Marshal(b)
	if err != nil {
		return b
	}
	var out Bundle
	if err := json.Unmarshal(body, &out); err != nil {
		return b
	}
	return out
}
