package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignStandardWebhook produces the header values required by the Standard Webhooks specification.
// The secret can be raw bytes, hex, or base64.
func SignStandardWebhook(secret string, msgID string, timestamp time.Time, body []byte) (string, string, string) {
	tsStr := strconv.FormatInt(timestamp.Unix(), 10)
	toSign := fmt.Sprintf("%s.%s.%s", msgID, tsStr, string(body))

	key := []byte(secret)
	// If secret starts with "whsec_", strip prefix (common convention in Svix/StandardWebhooks)
	if strings.HasPrefix(secret, "whsec_") {
		if decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_")); err == nil {
			key = decoded
		}
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(toSign))
	sig := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return msgID, tsStr, sig
}

// VerifyStandardWebhook verifies the signature against the Standard Webhooks specification.
func VerifyStandardWebhook(secret string, msgID string, timestampStr string, signatureHeader string, body []byte) bool {
	toSign := fmt.Sprintf("%s.%s.%s", msgID, timestampStr, string(body))
	key := []byte(secret)
	if strings.HasPrefix(secret, "whsec_") {
		if decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_")); err == nil {
			key = decoded
		}
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(toSign))
	expected := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	// The header can contain multiple space-delimited signatures for key rotation
	sigs := strings.Split(signatureHeader, " ")
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(expected)) {
			return true
		}
	}
	return false
}
