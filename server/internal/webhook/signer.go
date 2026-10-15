package webhook

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Signatures follow Standard Webhooks (https://www.standardwebhooks.com):
// webhook-id, webhook-timestamp and webhook-signature "v1,<base64 HMAC-SHA256
// of id.timestamp.body>", with the key from a "whsec_<base64>" secret.

const secretPrefix = "whsec_"

// SignatureTolerance is how far a receiver should accept webhook-timestamp
// from its clock (Standard Webhooks recommends 5 minutes).
const SignatureTolerance = 5 * time.Minute

// NewSecret returns a new signing secret: whsec_ and 32 random bytes in base64.
func NewSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return secretPrefix + base64.StdEncoding.EncodeToString(b)
}

// ValidateSecret checks a signing secret: whsec_ and at least 24 bytes in base64.
func ValidateSecret(secret string) error {
	_, err := secretKey(secret)
	return err
}

func secretKey(secret string) ([]byte, error) {
	if !strings.HasPrefix(secret, secretPrefix) {
		return nil, errors.New("a signing secret starts with whsec_ followed by base64")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, secretPrefix))
	if err != nil {
		return nil, errors.New("the part after whsec_ must be base64")
	}
	if len(key) < 24 {
		return nil, errors.New("the signing secret must hold at least 24 bytes")
	}
	return key, nil
}

func signature(key []byte, msgID, ts string, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(msgID + "." + ts + "."))
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// SignStandardWebhook returns the webhook-timestamp and webhook-signature
// header values for a message sent at at.
func SignStandardWebhook(secret, msgID string, at time.Time, body []byte) (ts, sig string, err error) {
	key, err := secretKey(secret)
	if err != nil {
		return "", "", err
	}
	ts = strconv.FormatInt(at.Unix(), 10)
	return ts, signature(key, msgID, ts, body), nil
}

// VerifyStandardWebhook checks a request the way a receiver should: one of
// the space-separated signatures matches, and the timestamp is within
// tolerance of now (against replays).
func VerifyStandardWebhook(secret, msgID, ts, signatureHeader string, body []byte, now time.Time, tolerance time.Duration) bool {
	key, err := secretKey(secret)
	if err != nil {
		return false
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if d := now.Sub(time.Unix(sec, 0)); d > tolerance || d < -tolerance {
		return false
	}
	want := []byte(signature(key, msgID, ts, body))
	for _, s := range strings.Fields(signatureHeader) {
		if hmac.Equal([]byte(s), want) {
			return true
		}
	}
	return false
}
