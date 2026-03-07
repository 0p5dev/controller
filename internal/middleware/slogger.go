package middleware

import (
	"fmt"
	"log/slog"
	"os"
	"time"

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

	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		duration := time.Since(start)
		statusCode := c.Writer.Status()

		logger := slog.Default().With(
			slog.String("client_ip", c.ClientIP()),
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", statusCode),
			slog.Duration("duration", duration),
		)

		msg := fmt.Sprintf("[%s](%d)%s from %s in %v", c.Request.Method, statusCode, c.Request.URL.Path, c.ClientIP(), duration)
		logger.Log(c.Request.Context(), slog.LevelInfo, msg)
	}
}
