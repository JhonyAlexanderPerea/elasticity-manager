// Package api contains the HTTP router and handlers.
package api

import "elasticity-manager/internal/config"

// Aliases preserve existing api.Store/api.AutoScalerConfig usage.
type AutoScalerConfig = config.AutoScalerConfig
type Store = config.Store
