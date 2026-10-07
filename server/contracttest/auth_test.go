//go:build contract

package contracttest

import (
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func attemptDevice(t *testing.T, hdr http.Header) connOutcome {
	t.Helper()
	settleAfterClose(t)
	return attempt(t, "device", fx.Paths.Device, hdr)
}

func requireRejected(t *testing.T, o connOutcome) {
	t.Helper()
	if !o.rejected {
		t.Fatalf("expected the connection to be rejected, got: %s", o.how)
	}
	t.Logf("rejected as expected: %s", o.how)
}

func requireAccepted(t *testing.T, o connOutcome) {
	t.Helper()
	if o.rejected {
		t.Fatalf("expected the connection to be accepted, got: %s", o.how)
	}
}

func TestDeviceAuth(t *testing.T) {
	t.Run("ValidToken_RS256", func(t *testing.T) {
		d := dev(t, "main")
		requireAccepted(t, attemptDevice(t, deviceHeader(deviceToken(t, d))))
	})

	t.Run("ValidToken_ES256", func(t *testing.T) {
		d := dev(t, "second")
		requireAccepted(t, attemptDevice(t, deviceHeader(deviceToken(t, d))))
	})

	t.Run("BadSignature", func(t *testing.T) {
		d := dev(t, "main")
		tok := signWith(t, jwt.SigningMethodRS256, freshRSAKey(t),
			jwt.MapClaims{"id": d.ID, "exp": time.Now().Add(time.Hour).Unix()})
		requireRejected(t, attemptDevice(t, deviceHeader(tok)))
	})

	t.Run("AlgorithmMismatch", func(t *testing.T) {
		// main has an RS256 key; sign its id with an ES256 key. The algorithm
		// must come from the stored key, never from the token header.
		main, second := dev(t, "main"), dev(t, "second")
		tok := signWith(t, jwt.SigningMethodES256, parsePrivateKey(t, second.PrivateKeyPEM),
			jwt.MapClaims{"id": main.ID, "exp": time.Now().Add(time.Hour).Unix()})
		requireRejected(t, attemptDevice(t, deviceHeader(tok)))
	})

	t.Run("AlgNone", func(t *testing.T) {
		d := dev(t, "main")
		tok, err := jwt.NewWithClaims(jwt.SigningMethodNone,
			jwt.MapClaims{"id": d.ID, "exp": time.Now().Add(time.Hour).Unix()}).
			SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatal(err)
		}
		requireRejected(t, attemptDevice(t, deviceHeader(tok)))
	})

	t.Run("ExpiredToken", func(t *testing.T) {
		d := dev(t, "main")
		tok := signJWT(t, d, jwt.MapClaims{"id": d.ID, "exp": time.Now().Add(-time.Hour).Unix()})
		requireRejected(t, attemptDevice(t, deviceHeader(tok)))
	})

	t.Run("UnknownDevice", func(t *testing.T) {
		d := dev(t, "main")
		tok := signJWT(t, d, jwt.MapClaims{
			"id":  "00000000-0000-4000-8000-000000000000",
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		requireRejected(t, attemptDevice(t, deviceHeader(tok)))
	})

	t.Run("MissingIdClaim", func(t *testing.T) {
		d := dev(t, "main")
		tok := signJWT(t, d, jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()})
		requireRejected(t, attemptDevice(t, deviceHeader(tok)))
	})

	t.Run("DeviceWithoutKey", func(t *testing.T) {
		d := dev(t, "nokey")
		requireRejected(t, attemptDevice(t, deviceHeader(deviceToken(t, d))))
	})

	t.Run("GarbageToken", func(t *testing.T) {
		requireRejected(t, attemptDevice(t, deviceHeader("not.a.jwt")))
	})

	t.Run("NoAuthorizationHeader", func(t *testing.T) {
		requireRejected(t, attemptDevice(t, http.Header{}))
	})

	t.Run("BearerWithoutToken", func(t *testing.T) {
		h := http.Header{}
		h.Set("Authorization", "Bearer")
		requireRejected(t, attemptDevice(t, h))
	})

	t.Run("B1_InactiveKey", func(t *testing.T) {
		d := dev(t, "inactive")
		o := attemptDevice(t, deviceHeader(deviceToken(t, d)))
		checkBug(t, "B1", o.rejected, "device with an inactive key was %s", o.how)
	})

	t.Run("B2_TokenWithoutExp", func(t *testing.T) {
		d := dev(t, "main")
		tok := signJWT(t, d, jwt.MapClaims{"id": d.ID})
		o := attemptDevice(t, deviceHeader(tok))
		checkBug(t, "B2", o.rejected, "token without exp was %s", o.how)
	})
}

func TestBrowserAuth(t *testing.T) {
	t.Run("NoCookie", func(t *testing.T) {
		requireRejected(t, attempt(t, "browser", fx.Paths.Browser, browserHeader("", fx.Origin)))
	})

	t.Run("InvalidSession", func(t *testing.T) {
		requireRejected(t, attempt(t, "browser", fx.Paths.Browser,
			browserHeader("sessionid=doesnotexist0000000000000000000", fx.Origin)))
	})

	t.Run("ValidCookie", func(t *testing.T) {
		u := user(t, "rc")
		requireAccepted(t, attempt(t, "browser", fx.Paths.Browser, browserHeader(u.Cookie, fx.Origin)))
	})

	t.Run("B4_ForeignOrigin", func(t *testing.T) {
		u := user(t, "rc")
		o := attempt(t, "browser", fx.Paths.Browser, browserHeader(u.Cookie, "http://evil.example"))
		checkBug(t, "B4", o.rejected, "cookie-authenticated socket from Origin http://evil.example was %s", o.how)
	})
}
