package authorization

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CheckPermissionTx re-evaluates one permission against the caller's
// transaction, so a role demotion, binding revocation or grant revocation that
// happened while the caller waited on a lock is honored before the commit. It is
// the transaction counterpart of Authorize and uses the same live role matrix,
// participation state and explicit grants.
//
// The caller must pass the transaction it is about to commit, never the pool: the
// whole evaluation (live user, active bindings, participation and permission
// grants) runs on that one querier, so it never acquires a second pool connection
// while the transaction is open. That is why the grant lookup is bound to the
// transaction rather than reusing the shared authorizer's pool-backed checker.
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
	grants, err := transactionGrantChecker(service.grants, tx)
	if err != nil {
		return err
	}
	decision, err := service.decideWithGrants(ctx, tx, user, permission, scope, grants)
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return ErrForbidden
	}
	return nil
}

// ErrGrantCheckerNotTransactional reports that the configured grant checker
// cannot evaluate a grant on a transaction. It is a bounded configuration error
// rather than a silent permission denial: the caller sees a failure, not an
// unexplained 403.
var ErrGrantCheckerNotTransactional = errors.New("authorization: grant checker does not support transactional lookup")

// transactionGrantChecker binds a grant checker to one querier.
//
// The default GrantStore implements TransactionalGrantChecker. A checker that
// cannot run on a transaction produces an explicit error instead of falling back
// to the pool (which is what this helper exists to prevent).
func transactionGrantChecker(inner PermissionGrantChecker, q rowsQuerier) (PermissionGrantChecker, error) {
	if inner == nil {
		return nil, nil
	}
	aware, ok := inner.(TransactionalGrantChecker)
	if !ok {
		return nil, fmt.Errorf("%w: %T", ErrGrantCheckerNotTransactional, inner)
	}
	return txGrantChecker{inner: aware, q: q}, nil
}

type txGrantChecker struct {
	inner TransactionalGrantChecker
	q     rowsQuerier
}

func (c txGrantChecker) HasGrant(ctx context.Context, userID uuid.UUID, permission Permission, scope Scope) (bool, error) {
	return c.inner.HasGrantWithQuerier(ctx, c.q, userID, permission, scope)
}
