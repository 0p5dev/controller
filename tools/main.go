package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/moby/moby/client"
)

type UserClaims struct {
	jwt.RegisteredClaims
	Email        string                 `json:"email"`
	Role         string                 `json:"role"`
	UserMetadata map[string]interface{} `json:"user_metadata"`
}

func GetUserClaims(authHeader string) (*UserClaims, error) {
	if authHeader == "" {
		return nil, fmt.Errorf("authorization header required")
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, fmt.Errorf("authorization header must contain Bearer token")
	}
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")

	jwtSecret := os.Getenv("SUPABASE_JWT_SECRET")
	token, err := jwt.ParseWithClaims(tokenString, &UserClaims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(jwtSecret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("invalid token: %v", err)
	}

	userClaims, ok := token.Claims.(*UserClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return userClaims, nil
}

type ImageDetails struct {
	ImageID   string
	ImageName string
}

func GetContainerImageDetails(imageLoadResponse client.LoadResponse) (ImageDetails, error) {
	var imageID string
	scanner := bufio.NewScanner(imageLoadResponse.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Loaded image ID:") {
			var result map[string]interface{}
			if err := json.Unmarshal([]byte(line), &result); err == nil {
				if stream, ok := result["stream"].(string); ok {
					if strings.HasPrefix(stream, "Loaded image ID: ") {
						imageID = strings.TrimPrefix(stream, "Loaded image ID: ")
						imageID = strings.TrimSpace(imageID)
						break
					}
				}
			}
		} else if strings.Contains(line, "Loaded image:") {
			var result map[string]interface{}
			if err := json.Unmarshal([]byte(line), &result); err == nil {
				if stream, ok := result["stream"].(string); ok {
					if strings.HasPrefix(stream, "Loaded image: ") {
						imageID = strings.TrimPrefix(stream, "Loaded image: ")
						imageID = strings.TrimSpace(imageID)
						break
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return ImageDetails{}, fmt.Errorf("error reading ImageLoad response: %v", err)
	}

	if imageID == "" {
		return ImageDetails{}, fmt.Errorf("could not extract image ID from ImageLoad response")
	}

	imageName := strings.Split(imageID, ":")[0]

	return ImageDetails{
		ImageID:   imageID,
		ImageName: imageName,
	}, nil
}
