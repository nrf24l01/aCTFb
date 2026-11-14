package access

import (
	"context"
	"errors"
	"testing"

	"github.com/nrf24l01/actfb/internal/authorization"
)

func TestAudiencesServiceCRUDAndSubjectMode(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	svc := NewService(db, authorization.NewAuthorizer(db))
	ctx := context.Background()
	owner := authorization.Principal{UserID: f.User1}
	admin := authorization.Principal{UserID: f.Admin}

	if _, err := svc.CreateAudience(ctx, authorization.Principal{UserID: f.User3}, f.BoardPrivate, AudienceInput{Name: "nope"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-staff create audience = %v, want ErrForbidden", err)
	}
	audience, err := svc.CreateAudience(ctx, owner, f.BoardPrivate, AudienceInput{Name: "service-audience", Description: "desc"})
	if err != nil {
		t.Fatalf("create audience: %v", err)
	}
	if _, err := svc.CreateAudience(ctx, owner, f.BoardPrivate, AudienceInput{Name: "service-audience"}); !errors.Is(err, ErrAudienceExists) {
		t.Fatalf("duplicate audience = %v, want ErrAudienceExists", err)
	}
	description := "updated"
	updated, err := svc.UpdateAudience(ctx, owner, f.BoardPrivate, audience.ID, AudiencePatch{Description: &description})
	if err != nil || updated.Description != description {
		t.Fatalf("update audience = %+v, %v", updated, err)
	}
	page, err := svc.ListAudiences(ctx, owner, f.BoardPrivate, ListQuery{Limit: 100})
	if err != nil || page.Total < 3 {
		t.Fatalf("list audiences total=%d err=%v", page.Total, err)
	}

	member, err := svc.AddAudienceMember(ctx, owner, f.BoardPrivate, audience.ID, MemberInput{SubjectType: "USER", SubjectID: f.User3})
	if err != nil {
		t.Fatalf("add USER member: %v", err)
	}
	if _, err := svc.AddAudienceMember(ctx, owner, f.BoardPrivate, audience.ID, MemberInput{SubjectType: "USER", SubjectID: f.User3}); !errors.Is(err, ErrAudienceMemberExists) {
		t.Fatalf("duplicate member = %v, want ErrAudienceMemberExists", err)
	}
	if _, err := svc.AddAudienceMember(ctx, owner, f.BoardPrivate, audience.ID, MemberInput{SubjectType: "TEAM", SubjectID: f.TeamA}); !errors.Is(err, ErrInvalidSubject) {
		t.Fatalf("TEAM on INDIVIDUAL board = %v, want ErrInvalidSubject", err)
	}
	members, err := svc.ListAudienceMembers(ctx, owner, f.BoardPrivate, audience.ID, ListQuery{})
	if err != nil || members.Total != 1 || members.Items[0].Subject.ID != f.User3 {
		t.Fatalf("list members = %+v, %v", members, err)
	}
	if err := svc.RemoveAudienceMember(ctx, owner, f.BoardPrivate, audience.ID, member.Subject); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if err := svc.RemoveAudienceMember(ctx, owner, f.BoardPrivate, audience.ID, member.Subject); !errors.Is(err, ErrAudienceMemberNotFound) {
		t.Fatalf("double remove = %v, want ErrAudienceMemberNotFound", err)
	}

	// TEAM subject must be an actual team on the TEAM board.
	teamAudience, err := svc.CreateAudience(ctx, admin, f.BoardTeam, AudienceInput{Name: "team-service-audience"})
	if err != nil {
		t.Fatalf("create team audience: %v", err)
	}
	if _, err := svc.AddAudienceMember(ctx, admin, f.BoardTeam, teamAudience.ID, MemberInput{SubjectType: "USER", SubjectID: f.User1}); !errors.Is(err, ErrInvalidSubject) {
		t.Fatalf("USER on TEAM board = %v, want ErrInvalidSubject", err)
	}
	if _, err := svc.AddAudienceMember(ctx, admin, f.BoardTeam, teamAudience.ID, MemberInput{SubjectType: "TEAM", SubjectID: f.TeamA}); err != nil {
		t.Fatalf("add TEAM member: %v", err)
	}

	// In-use audience cannot be deleted; wrong-board audience is not found.
	if err := svc.DeleteAudience(ctx, owner, f.BoardPrivate, f.Audience1); !errors.Is(err, ErrAudienceInUse) {
		t.Fatalf("delete in-use audience = %v, want ErrAudienceInUse", err)
	}
	if _, err := svc.UpdateAudience(ctx, admin, f.BoardPublic, audience.ID, AudiencePatch{Name: &description}); !errors.Is(err, ErrAudienceNotFound) {
		t.Fatalf("wrong-board audience update = %v, want ErrAudienceNotFound", err)
	}
	if err := svc.DeleteAudience(ctx, owner, f.BoardPrivate, audience.ID); err != nil {
		t.Fatalf("delete unused audience: %v", err)
	}
}
