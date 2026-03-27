package sharedUtils

import "github.com/golang-jwt/jwt/v5"

type UserClaims struct {
	jwt.RegisteredClaims
	Email        string         `json:"email"`
	Role         string         `json:"role"`
	UserMetadata map[string]any `json:"user_metadata"`
}
