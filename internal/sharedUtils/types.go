package sharedUtils

import (
	"github.com/0p5dev/controller/internal/models"
	"github.com/golang-jwt/jwt/v5"
)

type OauthClaims struct {
	jwt.RegisteredClaims
	Email        string       `json:"email"`
	Role         string       `json:"role"`
	UserMetadata UserMetadata `json:"user_metadata"`
}

type UserClaims struct {
	OauthClaims
	// models.User
}

type UserMetadata struct {
	AppUser           *models.User `json:"app_user"`
	AvatarUrl         string       `json:"avatar_url"`
	Email             string       `json:"email"`
	EmailVerified     bool         `json:"email_verified"`
	FullName          string       `json:"full_name"`
	Iss               string       `json:"iss"`
	PhoneVerified     bool         `json:"phone_verified"`
	PreferredUsername string       `json:"preferred_username"`
	ProviederId       string       `json:"provider_id"`
	Sub               string       `json:"sub"`
	UserName          string       `json:"user_name"`
}
