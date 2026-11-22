package access

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
)

func (s *AccessService) ListAudiences(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, q ListQuery) (AudiencePage, error) {
	if err := s.ready(); err != nil {
		return AudiencePage{}, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardAudiencesManage, authorization.Scope{BoardID: boardID}); err != nil {
		return AudiencePage{}, err
	}
	limit, offset := clampPage(q.Limit, q.Offset)
	query := strings.TrimSpace(q.Query)
	rows, err := s.db.Pool.Query(ctx, `
		SELECT a.id, a.board_id, a.name, a.description, count(am.audience_id)::int
		FROM audiences a
		LEFT JOIN audience_members am ON am.audience_id = a.id
		WHERE a.board_id = $1 AND ($2 = '' OR a.name ILIKE '%' || $2 || '%')
		GROUP BY a.id, a.board_id, a.name, a.description
		ORDER BY a.name, a.id
		LIMIT $3 OFFSET $4
	`, boardID, query, limit, offset)
	if err != nil {
		return AudiencePage{}, fmt.Errorf("access: list audiences: %w", err)
	}
	defer rows.Close()
	page := AudiencePage{Items: make([]Audience, 0)}
	for rows.Next() {
		audience, err := scanAudience(rows)
		if err != nil {
			return AudiencePage{}, err
		}
		page.Items = append(page.Items, audience)
	}
	if err := rows.Err(); err != nil {
		return AudiencePage{}, fmt.Errorf("access: iterate audiences: %w", err)
	}
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audiences
		WHERE board_id = $1 AND ($2 = '' OR name ILIKE '%' || $2 || '%')
	`, boardID, query).Scan(&page.Total); err != nil {
		return AudiencePage{}, fmt.Errorf("access: count audiences: %w", err)
	}
	return page, nil
}

func (s *AccessService) CreateAudience(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, in AudienceInput) (*Audience, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 128 || len(in.Description) > 20000 {
		return nil, ErrInvalidInput
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardAudiencesManage, authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: begin audience create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "p1-audiences:"+boardID.String()); err != nil {
		return nil, fmt.Errorf("access: lock audiences: %w", err)
	}
	var audience Audience
	err = tx.QueryRow(ctx, `
		INSERT INTO audiences (board_id, name, description)
		VALUES ($1, $2, $3)
		RETURNING id, board_id, name, description
	`, boardID, name, in.Description).Scan(&audience.ID, &audience.BoardID, &audience.Name, &audience.Description)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAudienceExists
		}
		if isForeignKeyViolation(err) {
			return nil, ErrBoardNotFound
		}
		return nil, fmt.Errorf("access: create audience: %w", err)
	}
	if err := writeAccessAudit(ctx, tx, principal.UserID, &boardID, nil, "audience.create", "audience", &audience.ID, map[string]any{"name": audience.Name}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("access: commit audience create: %w", err)
	}
	return &audience, nil
}

func (s *AccessService) UpdateAudience(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID, in AudiencePatch) (*Audience, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len(name) > 128 {
			return nil, ErrInvalidInput
		}
		in.Name = &name
	}
	if in.Description != nil && len(*in.Description) > 20000 {
		return nil, ErrInvalidInput
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardAudiencesManage, authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: begin audience update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "p1-audiences:"+boardID.String()); err != nil {
		return nil, fmt.Errorf("access: lock audiences: %w", err)
	}
	var audience Audience
	err = tx.QueryRow(ctx, `
		UPDATE audiences
		SET name = COALESCE($1, name), description = COALESCE($2, description)
		WHERE id = $3 AND board_id = $4
		RETURNING id, board_id, name, description
	`, in.Name, in.Description, audienceID, boardID).Scan(&audience.ID, &audience.BoardID, &audience.Name, &audience.Description)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAudienceNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAudienceExists
		}
		return nil, fmt.Errorf("access: update audience: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM audience_members WHERE audience_id = $1`, audience.ID).Scan(&audience.MembersCount); err != nil {
		return nil, fmt.Errorf("access: count audience members: %w", err)
	}
	if err := writeAccessAudit(ctx, tx, principal.UserID, &boardID, nil, "audience.update", "audience", &audience.ID, map[string]any{"name": audience.Name}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("access: commit audience update: %w", err)
	}
	return &audience, nil
}

func (s *AccessService) DeleteAudience(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardAudiencesManage, authorization.Scope{BoardID: boardID}); err != nil {
		return err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("access: begin audience delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "p1-audiences:"+boardID.String()); err != nil {
		return fmt.Errorf("access: lock audiences: %w", err)
	}
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM audiences WHERE id = $1 AND board_id = $2 FOR UPDATE`, audienceID, boardID).Scan(&name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAudienceNotFound
		}
		return fmt.Errorf("access: lock audience: %w", err)
	}
	var used int
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM module_audiences WHERE board_id = $1 AND audience_id = $2`, boardID, audienceID).Scan(&used); err != nil {
		return fmt.Errorf("access: check audience use: %w", err)
	}
	if used > 0 {
		return ErrAudienceInUse
	}
	if _, err := tx.Exec(ctx, `DELETE FROM audiences WHERE id = $1 AND board_id = $2`, audienceID, boardID); err != nil {
		return fmt.Errorf("access: delete audience: %w", err)
	}
	if err := writeAccessAudit(ctx, tx, principal.UserID, &boardID, nil, "audience.delete", "audience", &audienceID, map[string]any{"name": name}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("access: commit audience delete: %w", err)
	}
	return nil
}

func (s *AccessService) ListAudienceMembers(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID, q ListQuery) (MemberPage, error) {
	if err := s.ready(); err != nil {
		return MemberPage{}, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardAudiencesManage, authorization.Scope{BoardID: boardID}); err != nil {
		return MemberPage{}, err
	}
	if err := s.audienceBelongsToBoard(ctx, s.db.Pool, boardID, audienceID); err != nil {
		return MemberPage{}, err
	}
	limit, offset := clampPage(q.Limit, q.Offset)
	// Resolve the subject display name in the same query: the roster must show a
	// username or team name, never a bare identifier the organizer has to decode.
	rows, err := s.db.Pool.Query(ctx, `
		SELECT am.audience_id, am.subject_type, am.subject_id, am.assigned_at,
		       CASE am.subject_type
		         WHEN 'USER' THEN COALESCE(u.username, '')
		         WHEN 'TEAM' THEN COALESCE(t.name, '')
		         ELSE ''
		       END
		FROM audience_members am
		LEFT JOIN users u ON am.subject_type = 'USER' AND u.id = am.subject_id
		LEFT JOIN teams t ON am.subject_type = 'TEAM' AND t.id = am.subject_id
		WHERE am.audience_id = $1
		ORDER BY am.subject_type, am.subject_id
		LIMIT $2 OFFSET $3
	`, audienceID, limit, offset)
	if err != nil {
		return MemberPage{}, fmt.Errorf("access: list audience members: %w", err)
	}
	defer rows.Close()
	page := MemberPage{Items: make([]AudienceMember, 0)}
	for rows.Next() {
		var member AudienceMember
		var name string
		if err := rows.Scan(&member.AudienceID, &member.Subject.Type, &member.Subject.ID, &member.AssignedAt, &name); err != nil {
			return MemberPage{}, fmt.Errorf("access: scan audience member: %w", err)
		}
		member.Subject.Name = name
		page.Items = append(page.Items, member)
	}
	if err := rows.Err(); err != nil {
		return MemberPage{}, fmt.Errorf("access: iterate audience members: %w", err)
	}
	if err := s.db.Pool.QueryRow(ctx, `SELECT count(*) FROM audience_members WHERE audience_id = $1`, audienceID).Scan(&page.Total); err != nil {
		return MemberPage{}, fmt.Errorf("access: count audience members: %w", err)
	}
	return page, nil
}

func (s *AccessService) AddAudienceMember(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID, in MemberInput) (*AudienceMember, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if !validSubjectType(in.SubjectType) || in.SubjectID == uuid.Nil {
		return nil, ErrInvalidSubject
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardAudiencesManage, authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: begin audience member add: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "p1-audiences:"+boardID.String()); err != nil {
		return nil, fmt.Errorf("access: lock audiences: %w", err)
	}
	board, err := loadBoard(ctx, tx, boardID)
	if err != nil {
		return nil, err
	}
	if err := s.validateSubjectParticipation(ctx, tx, board, in.SubjectType, in.SubjectID); err != nil {
		return nil, err
	}
	if err := s.audienceBelongsToBoard(ctx, tx, boardID, audienceID); err != nil {
		return nil, err
	}
	var member AudienceMember
	err = tx.QueryRow(ctx, `
		INSERT INTO audience_members (audience_id, subject_type, subject_id)
		VALUES ($1, $2, $3)
		RETURNING audience_id, subject_type, subject_id, assigned_at
	`, audienceID, in.SubjectType, in.SubjectID).Scan(&member.AudienceID, &member.Subject.Type, &member.Subject.ID, &member.AssignedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAudienceMemberExists
		}
		return nil, fmt.Errorf("access: add audience member: %w", err)
	}
	if err := writeAccessAudit(ctx, tx, principal.UserID, &boardID, nil, "audience.member.add", "audience", &audienceID, map[string]any{
		"subjectType": member.Subject.Type,
		"subjectId":   member.Subject.ID.String(),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("access: commit audience member add: %w", err)
	}
	return &member, nil
}

func (s *AccessService) RemoveAudienceMember(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID, subject Subject) error {
	if err := s.ready(); err != nil {
		return err
	}
	if !validSubjectType(subject.Type) || subject.ID == uuid.Nil {
		return ErrInvalidSubject
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardAudiencesManage, authorization.Scope{BoardID: boardID}); err != nil {
		return err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("access: begin audience member remove: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "p1-audiences:"+boardID.String()); err != nil {
		return fmt.Errorf("access: lock audiences: %w", err)
	}
	if err := s.audienceBelongsToBoard(ctx, tx, boardID, audienceID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		DELETE FROM audience_members
		WHERE audience_id = $1 AND subject_type = $2 AND subject_id = $3
	`, audienceID, subject.Type, subject.ID)
	if err != nil {
		return fmt.Errorf("access: remove audience member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAudienceMemberNotFound
	}
	if err := writeAccessAudit(ctx, tx, principal.UserID, &boardID, nil, "audience.member.remove", "audience", &audienceID, map[string]any{
		"subjectType": subject.Type,
		"subjectId":   subject.ID.String(),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("access: commit audience member remove: %w", err)
	}
	return nil
}

func (s *AccessService) MyAudiences(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) ([]Audience, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	user, err := loadLiveUser(ctx, s.db.Pool, principal.UserID)
	if err != nil {
		return nil, err
	}
	board, err := loadBoard(ctx, s.db.Pool, boardID)
	if err != nil {
		return nil, err
	}
	if board.Visibility != "PUBLIC" {
		groupAllowed, err := participantGroupAllowed(ctx, s.db.Pool, board, user.ID)
		if err != nil {
			return nil, err
		}
		if !groupAllowed {
			return nil, ErrForbidden
		}
	}
	subject, ok, err := resolveSubject(ctx, s.db.Pool, board, user.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []Audience{}, nil
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT a.id, a.board_id, a.name, a.description,
		       (SELECT count(*)::int FROM audience_members am2 WHERE am2.audience_id = a.id)
		FROM audiences a
		JOIN audience_members am ON am.audience_id = a.id
		WHERE a.board_id = $1 AND am.subject_type = $2 AND am.subject_id = $3
		ORDER BY a.name, a.id
	`, boardID, subject.Type, subject.ID)
	if err != nil {
		if isUndefinedTable(err) {
			return []Audience{}, nil
		}
		return nil, fmt.Errorf("access: list my audiences: %w", err)
	}
	defer rows.Close()
	audiences := make([]Audience, 0)
	for rows.Next() {
		audience, err := scanAudience(rows)
		if err != nil {
			return nil, err
		}
		audiences = append(audiences, audience)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("access: iterate my audiences: %w", err)
	}
	return audiences, nil
}

func (s *AccessService) SetBoardAccessGroups(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, groupIDs []uuid.UUID) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardParticipantsManage, authorization.Scope{BoardID: boardID}); err != nil {
		return err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("access: begin board access groups: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, accessGroupAdvisoryLockKey); err != nil {
		return fmt.Errorf("access: lock board access groups: %w", err)
	}
	board, err := loadBoard(ctx, tx, boardID)
	if err != nil {
		return err
	}
	unique := dedupeUUIDs(groupIDs)
	if board.Visibility == "PUBLIC" && len(unique) > 0 {
		return ErrPublicBoardGroups
	}
	if len(unique) > 0 {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM access_groups WHERE id = ANY($1::uuid[])`, unique).Scan(&count); err != nil {
			return fmt.Errorf("access: validate access groups: %w", err)
		}
		if count != len(unique) {
			return ErrAccessGroupNotFound
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM board_access_groups WHERE board_id = $1`, boardID); err != nil {
		return fmt.Errorf("access: clear board access groups: %w", err)
	}
	for _, groupID := range unique {
		if _, err := tx.Exec(ctx, `INSERT INTO board_access_groups (board_id, group_id) VALUES ($1, $2)`, boardID, groupID); err != nil {
			return fmt.Errorf("access: insert board access group: %w", err)
		}
	}
	if err := writeAccessAudit(ctx, tx, principal.UserID, &boardID, nil, "board.access_groups.set", "board", &boardID, map[string]any{"groupIds": uuidStrings(unique)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("access: commit board access groups: %w", err)
	}
	return nil
}

func (s *AccessService) BoardAccessGroups(ctx context.Context, boardID uuid.UUID) ([]uuid.UUID, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := loadBoard(ctx, s.db.Pool, boardID); err != nil {
		return nil, err
	}
	rows, err := s.db.Pool.Query(ctx, `SELECT group_id FROM board_access_groups WHERE board_id = $1 ORDER BY group_id`, boardID)
	if err != nil {
		return nil, fmt.Errorf("access: list board access groups: %w", err)
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("access: scan board access group: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("access: iterate board access groups: %w", err)
	}
	return ids, nil
}

// SetModuleAudiences is the concrete handoff for the boards module create/patch
// path. It is not part of the frozen Service interface.
func (s *AccessService) SetModuleAudiences(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID, audienceIDs []uuid.UUID) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardAudiencesManage, authorization.Scope{BoardID: boardID}); err != nil {
		return err
	}
	unique := dedupeUUIDs(audienceIDs)
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("access: begin module audiences: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "p1-audiences:"+boardID.String()); err != nil {
		return fmt.Errorf("access: lock audiences: %w", err)
	}
	if err := moduleBelongsToBoard(ctx, tx, boardID, moduleID); err != nil {
		return err
	}
	if len(unique) > 0 {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM audiences WHERE board_id = $1 AND id = ANY($2::uuid[])`, boardID, unique).Scan(&count); err != nil {
			return fmt.Errorf("access: validate module audiences: %w", err)
		}
		if count != len(unique) {
			return ErrAudienceNotFound
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM module_audiences WHERE board_id = $1 AND module_id = $2`, boardID, moduleID); err != nil {
		return fmt.Errorf("access: clear module audiences: %w", err)
	}
	for _, audienceID := range unique {
		if _, err := tx.Exec(ctx, `INSERT INTO module_audiences (board_id, module_id, audience_id) VALUES ($1, $2, $3)`, boardID, moduleID, audienceID); err != nil {
			return fmt.Errorf("access: insert module audience: %w", err)
		}
	}
	if err := writeAccessAudit(ctx, tx, principal.UserID, &boardID, &moduleID, "module.audiences.set", "module", &moduleID, map[string]any{"audienceIds": uuidStrings(unique)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *AccessService) validateSubjectParticipation(ctx context.Context, q rowQuerier, board boardRecord, subjectType string, subjectID uuid.UUID) error {
	switch board.ParticipationMode {
	case "INDIVIDUAL":
		if subjectType != "USER" {
			return ErrInvalidSubject
		}
		joined, err := isBoardParticipant(ctx, q, board.ID, subjectID)
		if err != nil {
			return err
		}
		if !joined {
			return ErrInvalidSubject
		}
		return nil
	case "TEAM":
		if subjectType != "TEAM" {
			return ErrInvalidSubject
		}
		var exists bool
		if err := q.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM teams t
				WHERE t.id = $1 AND t.board_id = $2
				  AND EXISTS (SELECT 1 FROM team_members tm WHERE tm.board_id = t.board_id AND tm.team_id = t.id)
			)
		`, subjectID, board.ID).Scan(&exists); err != nil {
			return fmt.Errorf("access: validate team subject: %w", err)
		}
		if !exists {
			return ErrInvalidSubject
		}
		return nil
	default:
		return ErrInvalidSubject
	}
}

func (s *AccessService) audienceBelongsToBoard(ctx context.Context, q rowQuerier, boardID, audienceID uuid.UUID) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM audiences WHERE id = $1 AND board_id = $2)`, audienceID, boardID).Scan(&exists); err != nil {
		return fmt.Errorf("access: check audience parent: %w", err)
	}
	if !exists {
		return ErrAudienceNotFound
	}
	return nil
}

type audienceScanner interface {
	Scan(dest ...any) error
}

func scanAudience(row audienceScanner) (Audience, error) {
	var audience Audience
	if err := row.Scan(&audience.ID, &audience.BoardID, &audience.Name, &audience.Description, &audience.MembersCount); err != nil {
		return Audience{}, fmt.Errorf("access: scan audience: %w", err)
	}
	return audience, nil
}

func dedupeUUIDs(values []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(values))
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func uuidStrings(values []uuid.UUID) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.String())
	}
	return result
}
