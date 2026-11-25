package boards

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// readScope is the resolved read capability for one board/module pair.
//
// It exists because participant reads and staff reads are different
// authorization paths that must not be merged:
//
//   - a participant read is gated by live participation, the board access group
//     and module audiences (revocation is immediate);
//   - a staff read is a management preview gated by a live scoped role for the
//     exact board/module, and never grants participation-only actions.
//
// preview is true when the caller may read the module/tasks as staff without
// being a participant. manageTasks is true when that staff scope also holds
// module.tasks.manage, which is what makes draft (invisible) tasks readable in
// the preview.
type readScope struct {
	preview     bool
	manageTasks bool
}

// resolveReadScope returns the staff preview scope for principal on this
// board/module. A non-participant without a live scoped role gets a zero scope
// and the caller must apply the participant path (which denies).
//
// Participation is checked first so that a participant whose access group was
// revoked keeps the participant denial even when they also hold a scoped role
// (access-authorization.md §3). A live global admin, and a participant that is
// a global admin, still gets the staff preview: platform admin read of native
// core resources is intentional and is not a participation claim.
func (s *Service) resolveReadScope(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID uuid.UUID,
) (readScope, bool, error) {
	admin, staff, moduleIDs, err := s.staffContext(ctx, principal, boardID)
	if err != nil {
		return readScope{}, false, err
	}
	participant, err := s.isParticipant(ctx, boardID, principal.UserID)
	if err != nil {
		return readScope{}, false, err
	}
	if admin {
		return readScope{preview: true, manageTasks: true}, participant, nil
	}
	if participant || !staff {
		return readScope{}, participant, nil
	}
	if !s.moduleInScope(moduleIDs, moduleID) {
		// Scoped staff never read a sibling module they are not assigned.
		return readScope{}, false, ErrForbidden
	}
	manageTasks, err := s.requireManageTasks(ctx, principal, boardID, moduleID)
	if err != nil {
		return readScope{}, false, err
	}
	return readScope{preview: true, manageTasks: manageTasks}, false, nil
}

// requireManageTasks reports whether the caller holds module.tasks.manage in
// this module scope. It uses the same live authorizer as the admin task routes,
// so a revoked binding is denied on the next request.
func (s *Service) requireManageTasks(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID uuid.UUID,
) (bool, error) {
	decision, err := s.authorize(ctx, principal, authorization.PermissionModuleTasksManage,
		authorization.Scope{BoardID: boardID, ModuleID: &moduleID})
	if err != nil {
		return false, err
	}
	return decision.Allowed, nil
}

func (s *Service) isParticipant(ctx context.Context, boardID, userID uuid.UUID) (bool, error) {
	_, err := s.store.GetParticipant(ctx, boardID, userID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrBoardNotJoined) {
		return false, nil
	}
	return false, err
}
