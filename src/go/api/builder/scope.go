package builder

import (
	"crypto/sha256"
	"encoding/hex"
)

// OwnerScope returns the record key segment that stands for a user: the
// lowercase hex SHA-256 of the user name, 64 characters. It is the only way
// a user name enters a record key of the per-user libraries.
//
// A user name may hold "/" and any other character, and a record key may not
// hold a "." or ".." segment. With the plain name, listing the records of
// user "a" by prefix would also return those of a user named "a/b". A hash
// has neither problem: no scope is the prefix of another.
func OwnerScope(user string) string {
	sum := sha256.Sum256([]byte(user))

	return hex.EncodeToString(sum[:])
}
