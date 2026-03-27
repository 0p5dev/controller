package middleware

import (
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/vlad-tokarev/sloggcp"
)

func SloggerMiddleware() gin.HandlerFunc {
	gcpJsonHandler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level:       slog.LevelInfo,
		ReplaceAttr: sloggcp.ReplaceAttr,
		AddSource:   true,
	})
	slog.SetDefault(slog.New(gcpJsonHandler))

	return func(c *gin.Context) {}
}
