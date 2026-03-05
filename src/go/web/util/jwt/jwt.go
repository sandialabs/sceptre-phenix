package jwt

import (
	"errors"
	"fmt"
	"time"

	"github.com/dgrijalva/jwt-go"
)

var userClaims = []string{"sub", "username", "user"} //nolint:gochecknoglobals // global constant

func UsernameFromClaims(claims jwt.MapClaims) (string, error) {
	for _, claim := range userClaims {
		if user, ok := claims[claim].(string); ok && user != "" {
			return user, nil
		}
	}

	return "", errors.New("username not found in JWT claims")
}

func ValidateExpirationClaim(claims jwt.MapClaims) error {
	exp, ok := claims["exp"]
	if !ok {
		return errors.New("expiration (exp) missing from token claims")
	}

	epoch, ok := exp.(float64)
	if !ok {
		return errors.New("expiration (exp) claim is formatted incorrectly")
	}

	expires := time.Unix(int64(epoch), 0)

	if time.Now().After(expires) {
		return fmt.Errorf("token expired at %v", expires)
	}

	return nil
}
