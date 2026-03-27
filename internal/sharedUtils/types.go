package sharedUtils

import (
	"github.com/0p5dev/controller/internal/models"
	"github.com/golang-jwt/jwt/v5"
)

type OauthClaims struct {
	jwt.RegisteredClaims
	Email        string         `json:"email"`
	Role         string         `json:"role"`
	UserMetadata map[string]any `json:"user_metadata"`
}

type UserClaims struct {
	OauthClaims
	models.User
}
