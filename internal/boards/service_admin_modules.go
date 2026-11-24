package boards

import (
	"context"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// AdminListModules relies on board.read plus SQL-level module scoping.
func (s *Service) AdminListModules(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, query ListQuery) (Page[Module], error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return Page[Module]{}, err
	}
	if err := s.requireBoardRead(ctx, principal, boardID); err != nil {
		return Page[Module]{}, err
	}
	query.Limit, query.Offset = normalizePage(query.Limit, query.Offset)
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return Page[Module]{}, err
	}
	_, staff, moduleIDs, err := s.staffContext(ctx, principal, boardID)
	if err != nil {
		return Page[Module]{}, err
	}
	if !staff {
		return Page[Module]{}, ErrForbidden
	}
	modules, total, err := s.store.ListModules(ctx, boardID, ModuleFilter{
		Limit: query.Limit, Offset: query.Offset, Query: query.Query, Type: query.Type, Admin: true, ModuleIDs: moduleIDs,
	})
	if err != nil {
		return Page[Module]{}, err
	}
	now := s.clock.Now()
	for index := range modules {
		modules[index].Status = effectiveModuleStatus(board, modules[index], now)
	}
	return Page[Module]{Items: modules, Limit: query.Limit, Offset: query.Offset, Total: total}, nil
}

// AdminCreateModule requires board-wide module.create authority.
func (s *Service) AdminCreateModule(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, input CreateModuleInput) (*Module, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionModuleCreate, authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if board.Status == BoardStatusFinished || board.Status == BoardStatusArchived {
		return nil, ErrBoardStateConflict
	}
	module := Module{
		BoardID: boardID, Name: input.Name, Description: input.Description, Type: input.Type,
		ExtensionKey:     input.ExtensionKey,
		AllowedTaskTypes: input.AllowedTaskTypes, AudienceUUIDs: input.AudienceUUIDs,
		Enabled: input.Enabled, SortOrder: input.SortOrder, StartsAt: input.StartsAt, EndsAt: input.EndsAt,
		TaskInstanceSettings: input.InstanceSettings,
	}
	if err := validateModuleInput(module, *board); err != nil {
		return nil, err
	}
	if err := validateNewModuleFormat(module); err != nil {
		return nil, err
	}
	if module.Type == ModuleTypeExtension {
		if err := s.validatePluginCheckerConfig(ctx, principal, boardID, module.ID, module.ExtensionKey, ""); err != nil {
			return nil, err
		}
	}
	if err := s.store.AudiencesBelongToBoard(ctx, boardID, module.AudienceUUIDs); err != nil {
		return nil, err
	}
	created, err := s.store.CreateModule(ctx, CreateModuleParams{
		BoardID: boardID, Name: module.Name, Description: module.Description, Type: module.Type,
		ExtensionKey:     module.ExtensionKey,
		AllowedTaskTypes: module.AllowedTaskTypes, AudienceUUIDs: module.AudienceUUIDs,
		Enabled: module.Enabled, SortOrder: module.SortOrder,
		StartsAt: module.StartsAt, EndsAt: module.EndsAt, CreatedBy: principal.UserID,
		InstanceSettings: module.TaskInstanceSettings,
	}, auditEvent(principal, &boardID, nil, "module.create", "module", nil, map[string]any{"type": module.Type}))
	if err != nil {
		return nil, err
	}
	created.Status = effectiveModuleStatus(board, *created, s.clock.Now())
	return created, nil
}

// AdminUpdateModule enforces module.update at module scope, which lets a
// module-scoped editor change only its assigned module.
func (s *Service) AdminUpdateModule(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID uuid.UUID,
	input UpdateModuleInput,
) (*Module, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionModuleUpdate, authorization.Scope{BoardID: boardID, ModuleID: &moduleID}); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	current, err := s.store.GetModule(ctx, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	if board.Status == BoardStatusArchived {
		return nil, ErrBoardStateConflict
	}
	merged, removedTypes, err := mergeModule(current, input)
	if err != nil {
		return nil, err
	}
	if err := validateModuleInput(merged, *board); err != nil {
		return nil, err
	}
	if merged.Type == ModuleTypeExtension {
		if err := s.validatePluginCheckerConfig(ctx, principal, boardID, moduleID, merged.ExtensionKey, ""); err != nil {
			return nil, err
		}
	}
	if err := s.store.AudiencesBelongToBoard(ctx, boardID, merged.AudienceUUIDs); err != nil {
		return nil, err
	}
	for _, removed := range removedTypes {
		used, err := s.store.ModuleHasTasksUsingType(ctx, moduleID, removed)
		if err != nil {
			return nil, err
		}
		if used {
			return nil, ErrModuleStateConflict
		}
	}
	updated, err := s.store.UpdateModule(ctx, boardID, moduleID, UpdateModuleParams{
		Name: merged.Name, Description: merged.Description, ExtensionKey: merged.ExtensionKey,
		AllowedTaskTypes: merged.AllowedTaskTypes,
		AudienceUUIDs:    merged.AudienceUUIDs, Enabled: merged.Enabled, SortOrder: merged.SortOrder,
		StartsAt: merged.StartsAt, EndsAt: merged.EndsAt,
		InstanceSettings: merged.TaskInstanceSettings,
	}, auditEvent(principal, &boardID, &moduleID, "module.update", "module", &moduleID, map[string]any{}))
	if err != nil {
		return nil, err
	}
	updated.Status = effectiveModuleStatus(board, *updated, s.clock.Now())
	return updated, nil
}

func mergeModule(current *Module, input UpdateModuleInput) (Module, []string, error) {
	merged := *current
	if input.Name.Set {
		if input.Name.Value == nil {
			return Module{}, nil, Invalid("body.name", "must not be null", "INVALID_VALUE")
		}
		merged.Name = *input.Name.Value
	}
	if input.Description.Set {
		if input.Description.Value == nil {
			return Module{}, nil, Invalid("body.description", "must not be null", "INVALID_VALUE")
		}
		merged.Description = *input.Description.Value
	}
	if input.ExtensionKey.Set {
		if input.ExtensionKey.Value == nil {
			merged.ExtensionKey = ""
		} else {
			merged.ExtensionKey = *input.ExtensionKey.Value
		}
	}
	removed := []string{}
	if input.AllowedTaskTypes.Set {
		if input.AllowedTaskTypes.Value == nil {
			return Module{}, nil, Invalid("body.allowedTaskTypes", "must not be null", "INVALID_VALUE")
		}
		for _, existing := range merged.AllowedTaskTypes {
			if !containsString(input.AllowedTaskTypes.Value, existing) {
				removed = append(removed, existing)
			}
		}
		merged.AllowedTaskTypes = input.AllowedTaskTypes.Value
	}
	if input.AudienceUUIDs.Set {
		if input.AudienceUUIDs.Value == nil {
			return Module{}, nil, Invalid("body.audienceUuids", "must not be null", "INVALID_VALUE")
		}
		merged.AudienceUUIDs = merged.AudienceUUIDs[:0]
		for _, raw := range input.AudienceUUIDs.Value {
			parsed, err := uuid.Parse(raw)
			if err != nil {
				return Module{}, nil, Invalid("body.audienceUuids", "invalid UUID", "INVALID_FORMAT")
			}
			merged.AudienceUUIDs = append(merged.AudienceUUIDs, parsed)
		}
	}
	if input.Enabled.Set {
		if input.Enabled.Value == nil {
			return Module{}, nil, Invalid("body.enabled", "must not be null", "INVALID_VALUE")
		}
		merged.Enabled = *input.Enabled.Value
	}
	if input.SortOrder.Set {
		if input.SortOrder.Value == nil {
			return Module{}, nil, Invalid("body.order", "must not be null", "INVALID_VALUE")
		}
		merged.SortOrder = *input.SortOrder.Value
	}
	if input.InstanceSettingsSet {
		merged.TaskInstanceSettings = input.InstanceSettings
	}
	if input.StartsAt.Set {
		merged.StartsAt = input.StartsAt.Value
	}
	if input.EndsAt.Set {
		merged.EndsAt = input.EndsAt.Value
	}
	return merged, removed, nil
}

// AdminDeleteModule maps history FK restrictions to a documented 409.
func (s *Service) AdminDeleteModule(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionModuleDelete, authorization.Scope{BoardID: boardID, ModuleID: &moduleID}); err != nil {
		return err
	}
	return s.store.DeleteModule(ctx, boardID, moduleID,
		auditEvent(principal, &boardID, &moduleID, "module.delete", "module", &moduleID, map[string]any{}))
}
