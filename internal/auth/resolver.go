package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type Resolver struct {
	jwtSecret []byte
	apiKeys   map[string]string
}

func NewResolver(jwtSecret string, apiKeys map[string]string) *Resolver {
	keys := make(map[string]string, len(apiKeys))
	for key, tenantID := range apiKeys {
		keys[key] = tenantID
	}

	return &Resolver{
		jwtSecret: []byte(jwtSecret),
		apiKeys:   keys,
	}
}

func (r *Resolver) ResolveTenantID(req *http.Request) (string, error) {
	if req == nil {
		return "", errors.New("request is nil")
	}

	apiKey := strings.TrimSpace(req.Header.Get("X-API-Key"))
	if apiKey != "" {
		if tenantID, ok := r.apiKeys[apiKey]; ok {
			return tenantID, nil
		}
		return "", errors.New("invalid api key")
	}

	authHeader := strings.TrimSpace(req.Header.Get("Authorization"))
	if authHeader == "" {
		return "", errors.New("missing authorization")
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("invalid authorization header")
	}

	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(parts[1], claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected signing method")
		}
		return r.jwtSecret, nil
	})
	if err != nil {
		return "", err
	}

	rawTenantID, ok := claims["tenant_id"]
	if !ok {
		return "", errors.New("tenant_id claim missing")
	}

	switch value := rawTenantID.(type) {
	case string:
		if value == "" {
			return "", errors.New("tenant_id claim empty")
		}
		return value, nil
	default:
		return "", errors.New("tenant_id claim invalid")
	}
}
