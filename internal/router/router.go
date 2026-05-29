package router

import (
	"errors"
)

type TenantConfig struct {
	TenantID  string
	Upstreams []string
}

type Router struct {
	tenants map[string]TenantConfig
}

func NewRouter(tenants []TenantConfig) (*Router, error) {
	if len(tenants) == 0 {
		return nil, errors.New("at least one tenant is required")
	}

	mapped := make(map[string]TenantConfig, len(tenants))
	for _, tenant := range tenants {
		if tenant.TenantID == "" {
			return nil, errors.New("tenant id is required")
		}
		if len(tenant.Upstreams) == 0 {
			return nil, errors.New("tenant upstreams are required")
		}
		mapped[tenant.TenantID] = tenant
	}

	return &Router{tenants: mapped}, nil
}

func (r *Router) UpstreamsForTenant(tenantID string) ([]string, error) {
	if tenantID == "" {
		return nil, errors.New("tenant id is empty")
	}

	tenant, ok := r.tenants[tenantID]
	if !ok {
		return nil, errors.New("tenant not found")
	}

	return tenant.Upstreams, nil
}

type ContextTenantIDKey struct{}
