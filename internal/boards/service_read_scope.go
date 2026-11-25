package boards

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// readScope is the resolved read capability for one board/module pair.
//
// It exists because participant reads and staff management reads are different
// authorization paths that must not be merged:
//
//   - a participant read is gated by live participation, the board access group
//     and module audiences (revocation is immediate) and unlocks participant-own
//     actions such as submission history and instance start;
//   - a staff read is a management preview gated by a live admin role or a live
//     scoped binding for the exact board/module. It never unlocks any
//     participant-own action, score, history or instance.
//
// participant reports that the caller is eligible for participant-own actions on
// this exact scope. preview reports that this read is a staff management preview
// because that eligibility is absent (never joined, or the access group or
// audience was revoked). A caller can be both staff and participant-eligible; in
// that case the read is a participant read with own data, and preview is false.
type readScope struct {
	preview     bool
	participant bool
	manageTasks bool
}

// boardRead is the board-shell capability. A module-scoped staff member still
// reads the board shell so they can reach their assigned module, but they never
// see sibling modules.
type boardRead struct {
	allowed     bool
	preview     bool
	participant bool
}

// resolveBoardRead resolves the board shell capability.
//
// Order: participant eligibility (joined + live access group) first, then live
// staff management read (global admin or any live scoped binding). A joined
// participant whose group was revoked and who also holds a scoped role therefore
// keeps a management preview instead of a participant read, and gains no
// participant-own action from it.
//
// A published PUBLIC board stays readable as metadata for any authenticated
// caller (the existing law); that is neither participant eligibility nor a staff
// preview, so both flags stay false. A caller with neither is denied.
func (s *Service) resolveBoardRead(
	ctx context.Context,
	principal authorization.Principal,
	boardID uuid.UUID,
) (boardRead, error) {
	admin, staff, _, err := s.staffContext(ctx, principal, boardID)
	if err != nil {
		return boardRead{}, err
	}
	joined, err := s.isParticipant(ctx, boardID, principal.UserID)
	if err != nil {
		return boardRead{}, err
	}
	if joined {
		if s.accessPolicy == nil {
			return boardRead{allowed: true, participant: true}, nil
		}
		allowed, policyErr := s.accessPolicy.CanReadBoardAsParticipant(ctx, principal, boardID)
		if policyErr != nil {
			return boardRead{}, mapAccessError(policyErr)
		}
		if allowed {
			return boardRead{allowed: true, participant: true}, nil
		}
	}
	if admin || staff {
		return boardRead{allowed: true, preview: true}, nil
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return boardRead{}, err
	}
	if board.Visibility == VisibilityPublic {
		switch board.EffectiveStatus(s.clock.Now()) {
		case BoardStatusDraft, BoardStatusArchived:
		default:
			return boardRead{allowed: true}, nil
		}
	}
	if s.accessPolicy != nil {
		allowed, policyErr := s.accessPolicy.CanReadBoard(ctx, principal, boardID)
		if policyErr != nil {
			return boardRead{}, mapAccessError(policyErr)
		}
		if allowed {
			return boardRead{allowed: true}, nil
		}
	}
	return boardRead{}, nil
}

// resolveReadScope returns the resolved capability for one board/module.
//
// A module-scoped staff member whose binding does not cover moduleID is denied
// (ErrForbidden) even when they read the board shell: sibling modules never leak
// across the scope boundary. Draft tasks additionally require the live
// module.tasks.manage permission, which is what manageTasks reports.
func (s *Service) resolveReadScope(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID uuid.UUID,
) (readScope, error) {
	admin, staff, moduleIDs, err := s.staffContext(ctx, principal, boardID)
	if err != nil {
		return readScope{}, err
	}
	participantEligible, err := s.moduleParticipantEligible(ctx, principal, boardID, moduleID)
	if err != nil {
		return readScope{}, err
	}
	if participantEligible {
		return readScope{participant: true}, nil
	}
	if admin {
		return readScope{preview: true, manageTasks: true}, nil
	}
	if !staff {
		return readScope{}, nil
	}
	if !s.moduleInScope(moduleIDs, moduleID) {
		return readScope{}, ErrForbidden
	}
	manageTasks, err := s.requireManageTasks(ctx, principal, boardID, moduleID)
	if err != nil {
		return readScope{}, err
	}
	return readScope{preview: true, manageTasks: manageTasks}, nil
}

// boardParticipantEligible applies only the participant board rules: a joined
// participant with live access-group membership. It is used by the own-progress
// read (/boards/{id}/me) so a revoked group cannot keep exposing a score.
func (s *Service) boardParticipantEligible(
	ctx context.Context,
	principal authorization.Principal,
	boardID uuid.UUID,
) (bool, error) {
	participant, err := s.isParticipant(ctx, boardID, principal.UserID)
	if err != nil || !participant {
		return false, err
	}
	if s.accessPolicy == nil {
		return true, nil
	}
	allowed, err := s.accessPolicy.CanReadBoardAsParticipant(ctx, principal, boardID)
	if err != nil {
		return false, mapAccessError(err)
	}
	return allowed, nil
}

// moduleParticipantEligible applies only the participant module rules: joined,
// live access-group membership and live module-audience membership. It
// deliberately excludes staff roles so a scoped role cannot manufacture
// participant eligibility after a group or audience revocation.
func (s *Service) moduleParticipantEligible(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID uuid.UUID,
) (bool, error) {
	participant, err := s.isParticipant(ctx, boardID, principal.UserID)
	if err != nil || !participant {
		return false, err
	}
	if s.accessPolicy == nil {
		return true, nil
	}
	allowed, err := s.accessPolicy.CanAccessModuleAsParticipant(ctx, principal, boardID, moduleID)
	if err != nil {
		return false, mapAccessError(err)
	}
	return allowed, nil
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
