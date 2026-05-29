package auth

import (
	"net/http"
	"testing"
	"time"

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
		"exp":       time.Now().Add(time.Hour).Unix(),
		"iat":       time.Now().Unix(),
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

func TestResolveTenantID_InvalidAPIKey(t *testing.T) {
	resolver := NewResolver("secret", map[string]string{"valid-key": "tenant-a"})

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("X-API-Key", "invalid-key")

	_, err := resolver.ResolveTenantID(req)
	if err == nil {
		t.Fatal("expected error for invalid api key")
	}
}

func TestResolveTenantID_MissingAuth(t *testing.T) {
	resolver := NewResolver("secret", map[string]string{})

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)

	_, err := resolver.ResolveTenantID(req)
	if err == nil {
		t.Fatal("expected error for missing auth")
	}
}

func TestResolveTenantID_ExpiredJWT(t *testing.T) {
	secret := []byte("secret")
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": "tenant-a",
		"exp":       time.Now().Add(-time.Hour).Unix(),
		"iat":       time.Now().Add(-2 * time.Hour).Unix(),
	})
	signed, err := token.SignedString(secret)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	resolver := NewResolver("secret", map[string]string{})
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Bearer "+signed)

	_, err = resolver.ResolveTenantID(req)
	if err == nil {
		t.Fatal("expected error for expired JWT")
	}
}

func TestResolveTenantID_WrongSigningMethod(t *testing.T) {

	resolver := NewResolver("secret", map[string]string{})

	wrongSecret := []byte("wrong-secret")
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": "tenant-a",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	signed, _ := token.SignedString(wrongSecret)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Bearer "+signed)

	_, err := resolver.ResolveTenantID(req)
	if err == nil {
		t.Fatal("expected error for wrong signing secret")
	}
}

func TestResolveTenantID_MissingTenantClaim(t *testing.T) {
	secret := []byte("secret")
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-123",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, _ := token.SignedString(secret)

	resolver := NewResolver("secret", map[string]string{})
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Bearer "+signed)

	_, err := resolver.ResolveTenantID(req)
	if err == nil {
		t.Fatal("expected error for missing tenant_id claim")
	}
}

func TestResolveTenantID_EmptyTenantClaim(t *testing.T) {
	secret := []byte("secret")
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": "",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	signed, _ := token.SignedString(secret)

	resolver := NewResolver("secret", map[string]string{})
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Bearer "+signed)

	_, err := resolver.ResolveTenantID(req)
	if err == nil {
		t.Fatal("expected error for empty tenant_id claim")
	}
}

func TestResolveTenantID_InvalidAuthorizationFormat(t *testing.T) {
	resolver := NewResolver("secret", map[string]string{})
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	_, err := resolver.ResolveTenantID(req)
	if err == nil {
		t.Fatal("expected error for non-Bearer auth header")
	}
}

func TestResolveTenantID_NilRequest(t *testing.T) {
	resolver := NewResolver("secret", map[string]string{})
	_, err := resolver.ResolveTenantID(nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}
