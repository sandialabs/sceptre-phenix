package jwt

import (
	"fmt"
	"time"

	"github.com/dgrijalva/jwt-go"
)

var userClaims = []string{"sub", "username", "user"}

func UsernameFromClaims(claims jwt.MapClaims) (string, error) {
	for _, claim := range userClaims {
		if user, ok := claims[claim].(string); ok && user != "" {
			return user, nil
		}
	}

	return "", fmt.Errorf("username not found in JWT claims")
}

func ValidateExpirationClaim(claims jwt.MapClaims) error {
	exp, ok := claims["exp"]
	if !ok {
		return fmt.Errorf("expiration (exp) missing from token claims")
	}

	epoch, ok := exp.(float64)
	if !ok {
		return fmt.Errorf("expiration (exp) claim is formatted incorrectly")
	}

	expires := time.Unix(int64(epoch), 0)

	if time.Now().UTC().After(expires) {
		return fmt.Errorf("token expired at %v", expires)
	}

	return nil
}
