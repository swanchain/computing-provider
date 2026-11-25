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
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/hostcheck"
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

// authDisabled is set by `run --no-auth`. The token exists because the API
// can take models out of service and read back operational settings; on a
// developer's own machine, bound to loopback, it only gets in the way. The
// daemon refuses the flag on any other bind, so a disabled token never faces
// a network — but it does trust every local user, which is why a shared box
// keeps it on.
var authDisabled atomic.Bool

// SetAuthDisabled turns the control token off (true) or back on (false).
func SetAuthDisabled(disabled bool) { authDisabled.Store(disabled) }

// AuthDisabled reports whether the control token is being enforced.
func AuthDisabled() bool { return authDisabled.Load() }

// Role is what a request's token entitles it to. Roles are ordered: each one
// includes everything the roles below it may do.
type Role int

const (
	// RoleNone is a request with no token, or one that matched neither.
	// It still reads the plain monitoring routes (status, models, health).
	RoleNone Role = iota
	// RoleViewer reads what an operator would not post publicly: proxy
	// accounts (emails, plans, usage), the node's log, profiling data.
	RoleViewer
	// RoleOperator changes the node: takes models in and out of service,
	// sets limits, reads and writes settings.
	RoleOperator
)

// Tokens maps the node's bearer tokens to roles. Operator is the control
// token (dashboard.token). Viewer is optional and empty unless the operator
// created dashboard.viewer.token, for a monitoring client that should read the
// sensitive routes without being able to change anything.
type Tokens struct {
	Operator string
	Viewer   string
}

func tokenMatches(provided, want string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(want)) == 1
}

// RoleOf returns the role the request's bearer token grants.
func (t Tokens) RoleOf(c *gin.Context) Role {
	if authDisabled.Load() {
		return RoleOperator
	}
	const prefix = "Bearer "
	header := c.GetHeader("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return RoleNone
	}
	provided := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	switch {
	case tokenMatches(provided, t.Operator):
		return RoleOperator
	case tokenMatches(provided, t.Viewer):
		return RoleViewer
	}
	return RoleNone
}

// RequireRole refuses a request whose token grants less than min.
func (t Tokens) RequireRole(min Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		if t.RoleOf(c) < min {
			rejectUnauthorized(c)
			return
		}
		c.Next()
	}
}

// ProtectWrites lets reads through, for the route groups to decide, and
// requires the operator role for every mutating method.
func (t Tokens) ProtectWrites() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if t.RoleOf(c) < RoleOperator {
			rejectUnauthorized(c)
			return
		}
		c.Next()
	}
}

// ViewerTokenFile holds the optional read-only token. It is never created
// automatically: a node has no viewer unless its operator makes one.
const ViewerTokenFile = "dashboard.viewer.token"

// LoadViewerToken reads the viewer token if the operator created one. A
// missing file is not an error and returns "". A file readable by other users
// is tightened to the secret mode, as the control token's is.
func LoadViewerToken(cpRepoPath string) (string, error) {
	path := filepath.Join(cpRepoPath, ViewerTokenFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read viewer token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", errors.New("viewer token file is empty")
	}
	if info, statErr := os.Stat(path); statErr == nil && info.Mode().Perm()&0o077 != 0 {
		if chmodErr := os.Chmod(path, conf.SecretFileMode); chmodErr != nil {
			return "", fmt.Errorf("secure viewer token: %w", chmodErr)
		}
	}
	return token, nil
}

func authorized(c *gin.Context, token string) bool {
	return Tokens{Operator: token}.RoleOf(c) >= RoleOperator
}

func rejectUnauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", `Bearer realm="computing-provider dashboard"`)
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": "dashboard access token required",
	})
}

// ProtectWrites allows the read-only monitoring API to remain available while
// requiring the local control token for every mutating method.
func ProtectWrites(token string) gin.HandlerFunc { return Tokens{Operator: token}.ProtectWrites() }

// RequireAccess protects settings reads as well as writes because those
// responses contain operational addresses and account identifiers.
func RequireAccess(token string) gin.HandlerFunc {
	return Tokens{Operator: token}.RequireRole(RoleOperator)
}

// AllowedHosts refuses a request whose Host header is not this machine's (see
// package hostcheck), which is what a DNS-rebinding page sends.
func AllowedHosts(extra ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !hostcheck.Allowed(c.Request.Host, extra...) {
			c.AbortWithStatusJSON(http.StatusMisdirectedRequest, gin.H{"error": "host not allowed"})
			return
		}
		c.Next()
	}
}

// APIAnswersWithoutToken reports whether the API behind apiTarget serves a
// settings read with no Authorization header, which is what `run --no-auth`
// does. The dashboard is a reverse proxy to that API, so binding it to a
// non-loopback address would hand the open API to the network and defeat the
// loopback-only rule the daemon enforces on itself. The check is best effort:
// an API that cannot be reached yet reads as not open.
func APIAnswersWithoutToken(apiTarget string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimRight(apiTarget, "/") + "/api/v1/computing/inference/settings")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
