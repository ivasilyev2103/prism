package audit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/helldriver666/prism/internal/types"
)

const genesisValue = "genesis"

// computeHMAC calculates the HMAC-SHA256 for a record chained to the previous HMAC.
// chain: HMAC(key, serialize(record) || prevHMAC)
func computeHMAC(key []byte, r *types.RequestRecord, prevHMAC string) string {
	data := serializeRecord(r)
	if prevHMAC == "" {
		prevHMAC = genesisValue
	}
	data = append(data, []byte(prevHMAC)...)

	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyHMAC checks whether the stored HMAC matches the expected value.
func verifyHMAC(key []byte, r *types.RequestRecord, prevHMAC, storedHMAC string) bool {
	expected := computeHMAC(key, r, prevHMAC)
	return hmac.Equal([]byte(expected), []byte(storedHMAC))
}

// serializeRecord produces a deterministic byte representation of a record
// for HMAC computation. Fields are joined with '|' separator.
func serializeRecord(r *types.RequestRecord) []byte {
	s := fmt.Sprintf("%s|%d|%s|%s|%s|%s|%d|%d|%d|%f|%f|%f|%s|%d|%f|%d|%v|%s|%s",
		r.ID, r.Timestamp, r.ProjectID, r.ProviderID, r.ServiceType, r.Model,
		r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.ImagesCount,
		r.Usage.AudioSeconds, r.Usage.ComputeUnits,
		r.CostUSD, r.BillingType, r.LatencyMS, r.PrivacyScore,
		r.PIIEntitiesFound, r.CacheHit, r.RouteMatched, r.Status)
	return []byte(s)
}
