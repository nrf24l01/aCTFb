package authorization

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CheckPermissionTx re-evaluates one permission against the caller's
// transaction, so a role demotion or binding revocation that happened while the
// caller waited on a lock is honored before the commit. It is the transaction
// counterpart of Authorize and uses the same live role matrix, participation
// state and explicit grants.
//
// The caller must pass the transaction it is about to commit, never the pool:
// reusing the pool while holding a transaction can deadlock a small pool. The
// helper fails closed (ErrForbidden) when the authorizer cannot evaluate inside
// a transaction.
func CheckPermissionTx(
	ctx context.Context,
	auth Authorizer,
	tx pgx.Tx,
	userID uuid.UUID,
	permission Permission,
	scope Scope,
) error {
	if userID == uuid.Nil {
		return ErrUnauthorized
	}
	if auth == nil {
		return ErrNoAuthorizer
	}
	service, ok := auth.(*AuthorizerService)
	if !ok {
		return ErrForbidden
	}
	user, err := loadLiveUserForShare(ctx, tx, userID)
	if err != nil {
		return err
	}
	decision, err := service.decide(ctx, tx, user, permission, scope)
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return ErrForbidden
	}
	return nil
}

// IsForbiddenTxError reports whether err is a transactional permission denial.
func IsForbiddenTxError(err error) bool {
	return errors.Is(err, ErrForbidden)
}
