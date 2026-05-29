package config

import (
	"errors"
	"sync/atomic"
)

type Store struct {
	value atomic.Value
}

func NewStore(initial *GatewayConfig) (*Store, error) {
	if initial == nil {
		return nil, errors.New("initial config is nil")
	}

	store := &Store{}
	store.value.Store(initial)
	return store, nil
}

func (s *Store) Load() *GatewayConfig {
	value := s.value.Load()
	if value == nil {
		return nil
	}
	config, _ := value.(*GatewayConfig)
	return config
}

func (s *Store) Store(newConfig *GatewayConfig) error {
	if newConfig == nil {
		return errors.New("config is nil")
	}
	s.value.Store(newConfig)
	return nil
}
