package observability

import "testing"

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"", "/"},
		{"/", "/"},
		{"/healthz", "/healthz"},
		{"/api/users/12345", "/api/users/:id"},
		{"/api/users/12345/orders/67890", "/api/users/:id/orders/:id"},
		{"/api/items/550e8400-e29b-41d4-a716-446655440000", "/api/items/:id"},
		{"/api/v2/users", "/api/v2/users"},
		{"/a/b/c/d/e/f/g", "/a/b/c/d/e/..."},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizePath(tt.input)
			if got != tt.want {
				t.Errorf("NormalizePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
