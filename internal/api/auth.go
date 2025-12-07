package api

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

type UserClaims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

type SupabaseCredentials struct {
	SupabaseURL             string `json:"supabase_url"`
	SupabaseAnonPublicKey   string `json:"supabase_anon_public_key"`
}

// @Summary Get Supabase credentials
// @Description Retrieve Supabase URL and anon public key from GCP Secret Manager
// @Tags auth
// @Produce json
// @Success 200 {object} SupabaseCredentials "Supabase credentials"
// @Failure 500 {object} map[string]string "Failed to retrieve credentials"
// @Router /auth/supabase-credentials [get]
func (app *App) getSupabaseCredentials(c *gin.Context) {
	ctx := context.Background()
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		c.JSON(500, gin.H{
			"error":   "failed to create secret manager client",
			"message": err.Error(),
		})
		return
	}
	defer client.Close()

	accessSupabaseUrlReq := &secretmanagerpb.AccessSecretVersionRequest{Name: "projects/local-first-476300/secrets/ops-controller-supabase-url/versions/1"}
	accessSupabaseAnonPublicKeyReq := &secretmanagerpb.AccessSecretVersionRequest{Name: "projects/local-first-476300/secrets/ops-controller-supabase-anon-public-key/versions/1"}

	// Execute both secret access operations in parallel
	type secretResult struct {
		version *secretmanagerpb.AccessSecretVersionResponse
		err     error
	}

	urlChan := make(chan secretResult, 1)
	keyChan := make(chan secretResult, 1)

	go func() {
		version, err := client.AccessSecretVersion(ctx, accessSupabaseUrlReq)
		urlChan <- secretResult{version, err}
	}()

	go func() {
		version, err := client.AccessSecretVersion(ctx, accessSupabaseAnonPublicKeyReq)
		keyChan <- secretResult{version, err}
	}()

	urlResult := <-urlChan
	keyResult := <-keyChan

	if urlResult.err != nil {
		c.JSON(500, gin.H{
			"error":   "failed to get supabase url from secret manager",
			"message": urlResult.err.Error(),
		})
		return
	}
	if keyResult.err != nil {
		c.JSON(500, gin.H{
			"error":   "failed to get supabase anon public key from secret manager",
			"message": keyResult.err.Error(),
		})
		return
	}

	supabaseUrl := urlResult.version
	supabaseAnonPublicKey := keyResult.version

	credentials := SupabaseCredentials{
		SupabaseURL:           string(supabaseUrl.Payload.Data),
		SupabaseAnonPublicKey: string(supabaseAnonPublicKey.Payload.Data),
	}

	c.JSON(200, credentials)
}
