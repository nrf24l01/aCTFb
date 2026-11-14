package boards

import (
	"context"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// validatePluginCheckerConfig is the trusted configuration gate for
// plugin-backed EXTENSION modules/tasks. It requires an actual installed,
// enabled, compatible provider and a live explicit grant for the acting admin.
// It never creates a grant.
func (s *Service) validatePluginCheckerConfig(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID uuid.UUID,
	extensionKey, checkerKey string,
) error {
	if extensionKey == "" {
		return ErrDependencyUnavailable
	}
	if s.pluginChecker == nil {
		return ErrDependencyUnavailable
	}
	validator, ok := s.pluginChecker.(PluginCheckerConfigValidator)
	if !ok {
		return ErrDependencyUnavailable
	}
	return validator.ValidatePluginChecker(ctx, principal, boardID, moduleID, extensionKey, checkerKey)
}
