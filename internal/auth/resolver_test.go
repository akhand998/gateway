package auth

import (
	"net/http"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestResolveTenantIDFromAPIKey(t *testing.T) {
	resolver := NewResolver("secret", map[string]string{"api-key": "tenant-a"})

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("X-API-Key", "api-key")

	tenantID, err := resolver.ResolveTenantID(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenantID != "tenant-a" {
		t.Fatalf("expected tenant-a, got %s", tenantID)
	}
}

func TestResolveTenantIDFromJWT(t *testing.T) {
	secret := []byte("secret")
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": "tenant-b",
	})
	signed, err := token.SignedString(secret)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	resolver := NewResolver("secret", map[string]string{})
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Bearer "+signed)

	tenantID, err := resolver.ResolveTenantID(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenantID != "tenant-b" {
		t.Fatalf("expected tenant-b, got %s", tenantID)
	}
}
