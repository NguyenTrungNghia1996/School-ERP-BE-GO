package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// GenerateMFASecret generates a 160-bit (20-byte) cryptographically random Base32 TOTP secret.
func GenerateMFASecret() (string, error) {
	randomBytes := make([]byte, 20)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	encoder := base32.StdEncoding.WithPadding(base32.NoPadding)
	return encoder.EncodeToString(randomBytes), nil
}

// BuildOTPAuthURL builds the standard otpauth:// URI used to render a TOTP QR code.
func BuildOTPAuthURL(issuer, email, secret string) string {
	label := url.PathEscape(fmt.Sprintf("%s:%s", issuer, email))
	params := url.Values{}
	params.Set("secret", secret)
	params.Set("issuer", issuer)
	params.Set("algorithm", "SHA1")
	params.Set("digits", "6")
	params.Set("period", "30")
	return fmt.Sprintf("otpauth://totp/%s?%s", label, params.Encode())
}

// VerifyTOTP validates a 6-digit RFC 6238 TOTP code with +/- 1 step skew.
func VerifyTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}

	cleanedSecret := strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	if m := len(cleanedSecret) % 8; m != 0 {
		cleanedSecret += strings.Repeat("=", 8-m)
	}

	key, err := base32.StdEncoding.DecodeString(cleanedSecret)
	if err != nil {
		return false
	}

	counter := time.Now().Unix() / 30
	for i := int64(-1); i <= 1; i++ {
		if generateHOTP(key, uint64(counter+i)) == code {
			return true
		}
	}
	return false
}

func generateHOTP(key []byte, counter uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	binCode := (int(sum[offset])&0x7f)<<24 |
		(int(sum[offset+1])&0xff)<<16 |
		(int(sum[offset+2])&0xff)<<8 |
		(int(sum[offset+3]) & 0xff)

	otp := binCode % 1000000
	return fmt.Sprintf("%06d", otp)
}
