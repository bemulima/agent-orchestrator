package domain

import (
	"crypto/sha256"
	"encoding/hex"
)

// PlannerFingerprint binds the serialized planner input and output to the plan
// version approved by its owner. Keep the NUL separator to preserve the
// existing PostgreSQL plan fingerprint format.
func PlannerFingerprint(input, output []byte) string {
	material := make([]byte, 0, len(input)+len(output)+1)
	material = append(material, input...)
	material = append(material, 0)
	material = append(material, output...)
	digest := sha256.Sum256(material)
	return hex.EncodeToString(digest[:])
}
