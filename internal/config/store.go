// Package config contains shared runtime configuration state.
package config

import "sync"

// AutoScalerConfig holds all user-configurable elastic-scaling parameters.
type AutoScalerConfig struct {
	UpperThreshold   float64 `json:"upper_threshold"`   // % to trigger scale-out
	LowerThreshold   float64 `json:"lower_threshold"`   // % to trigger scale-in
	SampleInterval   int     `json:"sample_interval"`   // seconds between CPU polls
	EvaluationWindow int     `json:"evaluation_window"` // seconds to average CPU over
	MaxInstances     int     `json:"max_instances"`
	MinInstances     int     `json:"min_instances"`
}

// Store is a thread-safe holder for runtime configuration.
type Store struct {
	mu       sync.RWMutex
	Config   AutoScalerConfig
	onChange func()
}

// NewStore creates a Store with an initial config and optional change callback.
func NewStore(initial AutoScalerConfig, onChange func()) *Store {
	return &Store{Config: initial, onChange: onChange}
}

// SetOnChange updates the callback invoked after config changes.
func (s *Store) SetOnChange(onChange func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = onChange
}

func (s *Store) GetConfig() AutoScalerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Config
}

func (s *Store) SetConfig(cfg AutoScalerConfig) {
	s.mu.Lock()
	changeFn := s.onChange
	s.Config = cfg
	s.mu.Unlock()
	if changeFn != nil {
		changeFn()
	}
}
