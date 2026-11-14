package main

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/plugins"
)

// pluginCheckerAdapter is the only bridge from boards to the installable plugin
// manager. Boards never imports plugin internals and core never trusts a
// checker-supplied score.
type pluginCheckerAdapter struct {
	manager *plugins.Manager
}

var _ boards.PluginChecker = pluginCheckerAdapter{}
var _ boards.PluginCheckerConfigValidator = pluginCheckerAdapter{}

func (a pluginCheckerAdapter) Check(ctx context.Context, request boards.PluginCheckRequest) (boards.PluginCheckResult, error) {
	if a.manager == nil {
		return boards.PluginCheckResult{}, boards.ErrDependencyUnavailable
	}
	result, err := a.manager.CheckChecker(ctx, plugins.CheckerRequest{
		CheckerKey:   request.CheckerKey,
		ActorUserID:  request.ActorUserID,
		BoardID:      request.BoardID,
		ModuleID:     request.ModuleID,
		TaskID:       request.TaskID,
		SubmissionID: request.SubmissionID,
		SubjectType:  request.SubjectType,
		SubjectID:    request.SubjectID,
		Text:         request.Text,
		MaxPoints:    request.MaxPoints,
		Config:       request.Config,
	})
	if err != nil {
		return boards.PluginCheckResult{}, mapPluginCheckerError(err, false)
	}
	return boards.PluginCheckResult{Verdict: result.Verdict, Score: result.Score, Message: result.Message}, nil
}

func (a pluginCheckerAdapter) ValidatePluginChecker(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID uuid.UUID,
	extensionKey, checkerKey string,
) error {
	if a.manager == nil {
		return boards.ErrDependencyUnavailable
	}
	if err := a.manager.ValidateChecker(ctx, principal, boardID, moduleID, extensionKey, checkerKey); err != nil {
		return mapPluginCheckerError(err, true)
	}
	return nil
}

func mapPluginCheckerError(err error, configuration bool) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, plugins.ErrPermissionDenied):
		if configuration {
			return boards.ErrForbidden
		}
		return boards.ErrDependencyUnavailable
	case errors.Is(err, plugins.ErrCheckerNotFound), errors.Is(err, plugins.ErrCheckerUnavailable),
		errors.Is(err, plugins.ErrIncompatible):
		return boards.ErrDependencyUnavailable
	case errors.Is(err, plugins.ErrCheckerInvalid):
		return boards.ErrCheckerError
	default:
		if configuration {
			return boards.ErrDependencyUnavailable
		}
		return boards.ErrCheckerError
	}
}
