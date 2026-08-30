package dashboard

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/swanchain/computing-provider-v2/conf"
)

const AccessTokenFile = "dashboard.token"

// EnsureAccessToken returns the local dashboard/API control token, creating it
// on first use. The token is deliberately separate from the provider API key:
// it grants control of this machine, not access to the Swan marketplace.
func EnsureAccessToken(cpRepoPath string) (token string, tokenPath string, err error) {
	tokenPath = filepath.Join(cpRepoPath, AccessTokenFile)
	data, err := os.ReadFile(tokenPath)
	if err == nil {
		token = strings.TrimSpace(string(data))
		if token == "" {
			return "", tokenPath, errors.New("dashboard token file is empty")
		}
		if info, statErr := os.Stat(tokenPath); statErr == nil && info.Mode().Perm()&0o077 != 0 {
			if chmodErr := os.Chmod(tokenPath, conf.SecretFileMode); chmodErr != nil {
				return "", tokenPath, fmt.Errorf("secure dashboard token: %w", chmodErr)
			}
		}
		return token, tokenPath, nil
	}
	if !os.IsNotExist(err) {
		return "", tokenPath, fmt.Errorf("read dashboard token: %w", err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", tokenPath, fmt.Errorf("generate dashboard token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)

	file, err := os.OpenFile(tokenPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, conf.SecretFileMode)
	if errors.Is(err, os.ErrExist) {
		// The API and dashboard can start together. If the other process won the
		// create race, read the same token instead of replacing it.
		return EnsureAccessToken(cpRepoPath)
	}
	if err != nil {
		return "", tokenPath, fmt.Errorf("create dashboard token: %w", err)
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		file.Close()
		_ = os.Remove(tokenPath)
		return "", tokenPath, fmt.Errorf("write dashboard token: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tokenPath)
		return "", tokenPath, fmt.Errorf("close dashboard token: %w", err)
	}
	return token, tokenPath, nil
}

func authorized(c *gin.Context, token string) bool {
	const prefix = "Bearer "
	header := c.GetHeader("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

func rejectUnauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", `Bearer realm="computing-provider dashboard"`)
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": "dashboard access token required",
	})
}

// ProtectWrites allows the read-only monitoring API to remain available while
// requiring the local control token for every mutating method.
func ProtectWrites(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if !authorized(c, token) {
			rejectUnauthorized(c)
			return
		}
		c.Next()
	}
}

// RequireAccess protects settings reads as well as writes because those
// responses contain operational addresses and account identifiers.
func RequireAccess(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authorized(c, token) {
			rejectUnauthorized(c)
			return
		}
		c.Next()
	}
}
