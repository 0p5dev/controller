package api

import (
	"context"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

type AuthRequestBody struct {
	Username string `json:"username" binding:"required,min=8,max=32"`
	Password string `json:"password" binding:"required,min=16"`
}

type UserClaims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

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

	c.JSON(200, gin.H{
		"supabase_url":             string(supabaseUrl.Payload.Data),
		"supabase_anon_public_key": string(supabaseAnonPublicKey.Payload.Data),
	})
}

func (app *App) register(c *gin.Context) {
	var req AuthRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{
			"error":   "invalid request payload",
			"message": err.Error(),
		})
		return
	}
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(500, gin.H{
			"error":   "failed to hash password",
			"message": err.Error(),
		})
		return
	}
	ctx := c.Request.Context()
	_, err = app.Pool.Exec(ctx, "INSERT INTO users (username, password_hash) VALUES ($1, $2)", req.Username, string(hashedPassword))
	if err != nil {
		c.JSON(500, gin.H{
			"error":   "failed to create user",
			"message": err.Error(),
		})
		return
	}
	c.JSON(201, gin.H{
		"message": "user registered successfully",
	})
}

func (app *App) login(c *gin.Context) {
	var req AuthRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{
			"error":   "invalid request payload",
			"message": err.Error(),
		})
		return
	}
	ctx := c.Request.Context()
	var storedHashedPassword string
	err := app.Pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE username = $1", req.Username).Scan(&storedHashedPassword)
	if err != nil {
		c.JSON(401, gin.H{
			"error":   "unauthorized",
			"message": "invalid username or password",
		})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(storedHashedPassword), []byte(req.Password)); err != nil {
		c.JSON(401, gin.H{
			"error":   "unauthorized",
			"message": "invalid username or password",
		})
		return
	}

	var jwtSecret = os.Getenv("JWT_SECRET")
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, UserClaims{
		Username: req.Username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	})
	tokenString, err := token.SignedString([]byte(jwtSecret))
	if err != nil {
		c.JSON(500, gin.H{
			"error":   "failed to generate token",
			"message": err.Error(),
		})
		return
	}

	c.JSON(200, gin.H{
		"token": tokenString,
	})
}
