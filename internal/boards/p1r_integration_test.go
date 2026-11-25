package boards

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

func TestP1RTeamsManualAudienceGroupsAdjustments(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run P1R integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	sealer, err := secrets.NewCipher(key, 1)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	argon := config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}
	authz := authorization.NewAuthorizer(db)
	accessSvc := access.NewService(db, authz)
	outbox := events.NewOutbox(db)
	service := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(512),
		WithArgon2Config(argon), WithIdempotencySecret("p1r-secret"),
		WithAccessPolicy(accessSvc), WithAccessService(accessSvc), WithSealer(sealer), WithOutbox(outbox))

	adminID := createBoardUser(t, ctx, db, "p1r_admin", []string{"admin"})
	p1 := createBoardUser(t, ctx, db, "p1r_p1", []string{"user"})
	p2 := createBoardUser(t, ctx, db, "p1r_p2", []string{"user"})
	p3 := createBoardUser(t, ctx, db, "p1r_p3", []string{"user"})
	r1 := createBoardUser(t, ctx, db, "p1r_r1", []string{"user"})
	r2 := createBoardUser(t, ctx, db, "p1r_r2", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	pp1 := authorization.Principal{UserID: p1, GlobalRoles: []string{"user"}}
	pp2 := authorization.Principal{UserID: p2, GlobalRoles: []string{"user"}}
	pp3 := authorization.Principal{UserID: p3, GlobalRoles: []string{"user"}}
	pr1 := authorization.Principal{UserID: r1, GlobalRoles: []string{"user"}}
	pr2 := authorization.Principal{UserID: r2, GlobalRoles: []string{"user"}}

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(time.Hour)
	teamBoard, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "p1r-team", Name: "P1R Team", ParticipationMode: ParticipationTeam,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPublic, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
		TeamSettings: &TeamSettings{MinMembers: 1, MaxMembers: 2, AllowSelfCreate: true, AllowLeaveAfterStart: true},
	})
	if err != nil {
		t.Fatalf("create team board: %v", err)
	}
	// Simulate a legacy mixed-format module inserted before the single-format
	// rule. Existing data must stay readable; only new creation is restricted.
	module, err := NewPostgresStore(db).CreateModule(ctx, CreateModuleParams{
		BoardID: teamBoard.ID, Name: "Tasks", Type: ModuleTypeTasks,
		AllowedTaskTypes: []string{TaskTypeFlag, TaskTypeManual}, Enabled: true, CreatedBy: adminID,
	}, AuditEventParams{Action: "test.module.seed", ResourceType: "module", ActorUserID: adminID})
	if err != nil {
		t.Fatalf("create legacy mixed module: %v", err)
	}
	flagTask, err := service.AdminCreateTask(ctx, admin, teamBoard.ID, module.ID, CreateTaskInput{
		Title: "Flag", Type: TaskTypeFlag, Category: "web", Points: 100, Visible: true,
		Submission:      SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerAutomatic},
		AcceptedAnswers: []string{"flag{team}"}, Instance: TaskInstanceInput{Enabled: false},
	})
	if err != nil {
		t.Fatalf("create flag task: %v", err)
	}
	manualTask, err := service.AdminCreateTask(ctx, admin, teamBoard.ID, module.ID, CreateTaskInput{
		Title: "Manual", Type: TaskTypeManual, Category: "web", Points: 40, Visible: true,
		Submission: SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerManual},
		Instance:   TaskInstanceInput{Enabled: false},
	})
	if err != nil {
		t.Fatalf("create manual task: %v", err)
	}
	if _, err := service.AdminBoardAction(ctx, admin, teamBoard.ID, BoardActionPublish); err != nil {
		t.Fatalf("publish team board: %v", err)
	}
	publicRead, err := service.GetBoard(ctx, pp1, teamBoard.ID)
	if err != nil {
		t.Fatalf("public unjoined metadata read: %v", err)
	}
	if publicRead.Joined {
		t.Fatal("public unjoined board marked joined")
	}
	publicList, err := service.ListBoards(ctx, pp1, ListBoardsQuery{ListQuery: ListQuery{Limit: 50}})
	if err != nil {
		t.Fatalf("public unjoined list: %v", err)
	}
	foundPublic := false
	for _, item := range publicList.Items {
		if item.ID == teamBoard.ID {
			foundPublic = true
		}
	}
	if !foundPublic {
		t.Fatal("public published board missing from unjoined list")
	}
	for _, player := range []authorization.Principal{pp1, pp2, pp3} {
		if _, err := service.JoinBoard(ctx, player, teamBoard.ID, testJoinCode(t, db, teamBoard.ID, "P1R-TEAM-CODE-0001")); err != nil {
			t.Fatalf("join public team board: %v", err)
		}
	}
	team, err := service.CreateTeam(ctx, pp1, teamBoard.ID, "Red Team")
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	invite, err := service.RotateTeamInviteKey(ctx, pp1, teamBoard.ID)
	if err != nil {
		t.Fatalf("rotate invite: %v", err)
	}
	if _, err := service.JoinTeam(ctx, pp2, teamBoard.ID, invite.InviteKey); err != nil {
		t.Fatalf("join team: %v", err)
	}
	members, err := service.MyTeamMembers(ctx, pp1, teamBoard.ID)
	if err != nil {
		t.Fatalf("own team members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("own team members = %d, want 2", len(members))
	}
	for _, member := range members {
		if member.Username == "" {
			t.Fatal("own team member is missing a username")
		}
	}
	// A participant outside the team must not read this roster.
	if _, err := service.MyTeamMembers(ctx, pp3, teamBoard.ID); err == nil {
		t.Fatal("non-member read another team roster")
	}
	if _, err := service.JoinTeam(ctx, pp3, teamBoard.ID, invite.InviteKey); !errors.Is(err, ErrTeamFull) {
		t.Fatalf("team full error = %v", err)
	}
	rotated, err := service.RotateTeamInviteKey(ctx, pp1, teamBoard.ID)
	if err != nil {
		t.Fatalf("rotate invite again: %v", err)
	}
	if _, err := service.JoinTeam(ctx, pp3, teamBoard.ID, invite.InviteKey); !errors.Is(err, ErrInvalidTeamInviteKey) {
		t.Fatalf("old invite error = %v, want invalid", err)
	}
	_ = rotated

	var first *Submission
	var submitWG sync.WaitGroup
	submitResults := make(chan struct {
		submission *Submission
		err        error
	}, 2)
	for index, player := range []authorization.Principal{pp1, pp2} {
		submitWG.Add(1)
		go func(index int, player authorization.Principal) {
			defer submitWG.Done()
			submission, submitErr := service.SubmitTask(ctx, player,
				TaskPath{BoardID: teamBoard.ID, ModuleID: module.ID, TaskID: flagTask.ID},
				"p1r-team-key-000"+string(rune('1'+index)), SubmitInput{Text: "flag{team}"})
			submitResults <- struct {
				submission *Submission
				err        error
			}{submission, submitErr}
		}(index, player)
	}
	submitWG.Wait()
	close(submitResults)
	teamAccepted, teamSolved := 0, 0
	for result := range submitResults {
		switch {
		case result.err == nil:
			teamAccepted++
			first = result.submission
		case errors.Is(result.err, ErrTaskAlreadySolved):
			teamSolved++
		default:
			t.Fatalf("parallel team submit error = %v", result.err)
		}
	}
	if teamAccepted != 1 || teamSolved != 1 || first == nil {
		t.Fatalf("parallel team accepted=%d solved=%d, want 1/1", teamAccepted, teamSolved)
	}
	if first.Subject.Type != SubjectTypeTeam || first.Subject.ID != team.ID {
		t.Fatalf("submission subject = %+v, want team %s", first.Subject, team.ID)
	}
	if first.SubmittedText != nil {
		t.Fatalf("FLAG submission stored submitted text: %q", *first.SubmittedText)
	}
	scoreboard, err := service.GetScoreboard(ctx, pp1, teamBoard.ID)
	if err != nil {
		t.Fatalf("team scoreboard: %v", err)
	}
	if len(scoreboard.Items) != 1 || scoreboard.Items[0].Subject.Type != SubjectTypeTeam || scoreboard.Items[0].Score != 100 {
		t.Fatalf("team scoreboard = %+v", scoreboard)
	}

	// Team leave/hop must not rescore the same human/task for the submitter.
	acceptedUser := first.SubmittedByUserID
	acceptedPrincipal := pp1
	if acceptedUser == p2 {
		acceptedPrincipal = pp2
	}
	if acceptedUser == p1 {
		if err := service.LeaveTeam(ctx, pp2, teamBoard.ID); err != nil {
			t.Fatalf("leave second member: %v", err)
		}
		if _, err := service.JoinTeam(ctx, pp3, teamBoard.ID, rotated.InviteKey); err != nil {
			t.Fatalf("join team after slot opens: %v", err)
		}
		if _, err := service.TransferCaptain(ctx, pp1, teamBoard.ID, p3); err != nil {
			t.Fatalf("transfer captain: %v", err)
		}
		if err := service.LeaveTeam(ctx, pp1, teamBoard.ID); err != nil {
			t.Fatalf("captain leave after transfer: %v", err)
		}
	} else {
		if err := service.LeaveTeam(ctx, pp2, teamBoard.ID); err != nil {
			t.Fatalf("accepted member leave: %v", err)
		}
	}
	blueTeam, err := service.CreateTeam(ctx, acceptedPrincipal, teamBoard.ID, "Blue Team")
	if err != nil {
		t.Fatalf("create new team after leave: %v", err)
	}
	if _, err := service.SubmitTask(ctx, acceptedPrincipal, TaskPath{BoardID: teamBoard.ID, ModuleID: module.ID, TaskID: flagTask.ID},
		"p1r-team-key-0003", SubmitInput{Text: "flag{team}"}); !errors.Is(err, ErrTaskAlreadySolved) {
		t.Fatalf("team hop submit error = %v, want already solved", err)
	}

	// MANUAL: PENDING_REVIEW no score, concurrent reviewers award once.
	pending, err := service.SubmitTask(ctx, pp1, TaskPath{BoardID: teamBoard.ID, ModuleID: module.ID, TaskID: manualTask.ID},
		"p1r-manual-key-0001", SubmitInput{Text: "manual answer"})
	if err != nil {
		t.Fatalf("manual submit: %v", err)
	}
	if pending.Status != SubmissionStatusPendingRev || pending.ScoreAwarded != nil {
		t.Fatalf("manual pending = %+v", pending)
	}
	if pending.SubmittedText == nil || *pending.SubmittedText != "manual answer" {
		t.Fatalf("manual submitted text not persisted: %+v", pending.SubmittedText)
	}
	var storedText *string
	var storedTaskType string
	if err := db.Pool.QueryRow(ctx, `SELECT submitted_text, task_type FROM submissions WHERE id=$1`, pending.ID).Scan(&storedText, &storedTaskType); err != nil {
		t.Fatalf("read manual submitted_text: %v", err)
	}
	if storedText == nil || *storedText != "manual answer" || storedTaskType != TaskTypeManual {
		t.Fatalf("stored manual text = %v taskType=%s", storedText, storedTaskType)
	}
	// Migration 013's trigger derives task_type and makes it immutable. FLAG rows
	// must keep submitted_text NULL.
	if _, err := db.Pool.Exec(ctx, `UPDATE submissions SET task_type='FLAG' WHERE id=$1`, pending.ID); err == nil {
		t.Fatal("migration 013 allowed task_type mutation")
	}
	var flagText *string
	var flagType string
	if err := db.Pool.QueryRow(ctx, `
		SELECT submitted_text, task_type FROM submissions
		WHERE task_id=$1 AND status='ACCEPTED'
	`, flagTask.ID).Scan(&flagText, &flagType); err != nil {
		t.Fatalf("read FLAG submission: %v", err)
	}
	if flagText != nil || flagType != TaskTypeFlag {
		t.Fatalf("FLAG submission text=%v taskType=%s", flagText, flagType)
	}
	grantBinding(t, ctx, db, teamBoard.ID, &module.ID, r1, adminID, "reviewer", nil)
	grantBinding(t, ctx, db, teamBoard.ID, &module.ID, r2, adminID, "reviewer", nil)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for index, reviewer := range []authorization.Principal{pr1, pr2} {
		wg.Add(1)
		go func(index int, reviewer authorization.Principal) {
			defer wg.Done()
			_, reviewErr := service.AdminReviewSubmission(ctx, reviewer, teamBoard.ID, pending.ID,
				ReviewInput{Decision: ReviewDecisionAccept}, "p1r-review-race-000"+string(rune('1'+index)))
			results <- reviewErr
		}(index, reviewer)
	}
	wg.Wait()
	close(results)
	success, reviewed := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrSubmissionAlreadyReviewed):
			reviewed++
		default:
			t.Fatalf("manual review race error = %v", err)
		}
	}
	if success != 1 || reviewed != 1 {
		t.Fatalf("manual review success=%d already=%d, want 1/1", success, reviewed)
	}
	var accepted, judged int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM submissions WHERE id=$1 AND status='ACCEPTED' AND score_awarded=40`, pending.ID).Scan(&accepted); err != nil {
		t.Fatalf("manual accepted count: %v", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE type='submission.judged' AND payload->>'submissionUuid'=$1`, pending.ID.String()).Scan(&judged); err != nil {
		t.Fatalf("manual judged event count: %v", err)
	}
	if accepted != 1 || judged != 1 {
		t.Fatalf("manual accepted=%d judgedEvents=%d, want 1/1", accepted, judged)
	}

	// Score adjustment changes the TEAM subject score once, without a new submission.
	adjustment, err := service.AdminCreateScoreAdjustment(ctx, admin, teamBoard.ID, ScoreAdjustmentInput{
		Subject: ScoreSubject{Type: SubjectTypeTeam, ID: team.ID}, Delta: 5, Reason: "manual bonus",
	})
	if err != nil {
		t.Fatalf("score adjustment: %v", err)
	}
	scoreboard, err = service.GetScoreboard(ctx, pp1, teamBoard.ID)
	if err != nil {
		t.Fatalf("scoreboard after adjustment: %v", err)
	}
	redScore, blueScore := -1, -1
	for _, item := range scoreboard.Items {
		if item.Subject.Type == SubjectTypeTeam && item.Subject.ID == team.ID {
			redScore = item.Score
		}
		if item.Subject.Type == SubjectTypeTeam && item.Subject.ID == blueTeam.ID {
			blueScore = item.Score
		}
	}
	if first.SubmittedByUserID == p1 {
		if redScore != 105 || blueScore != 40 {
			t.Fatalf("team scores red=%d blue=%d, want 105/40", redScore, blueScore)
		}
	} else if redScore != 145 || blueScore != 0 {
		t.Fatalf("team scores red=%d blue=%d, want 145/0", redScore, blueScore)
	}
	adjustments, err := service.AdminListScoreAdjustments(ctx, admin, teamBoard.ID, nil, ListQuery{Limit: 10})
	if err != nil || adjustments.Total != 1 || adjustments.Items[0].ID != adjustment.ID {
		t.Fatalf("list adjustments = %+v err=%v", adjustments, err)
	}

	// Audience gating: TEAM subject in audience can access the module; removal denies immediately.
	audiencePrincipal := pp1
	if first.SubmittedByUserID == p1 {
		audiencePrincipal = pp3
	}
	audience, err := accessSvc.CreateAudience(ctx, admin, teamBoard.ID, access.AudienceInput{Name: "Finalists"})
	if err != nil {
		t.Fatalf("create audience: %v", err)
	}
	if _, err := accessSvc.AddAudienceMember(ctx, admin, teamBoard.ID, audience.ID, access.MemberInput{SubjectType: SubjectTypeTeam, SubjectID: team.ID}); err != nil {
		t.Fatalf("add audience member: %v", err)
	}
	gatedModule, err := service.AdminCreateModule(ctx, admin, teamBoard.ID, CreateModuleInput{
		Name: "Gated", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag},
		AudienceUUIDs: []uuid.UUID{audience.ID}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create gated module: %v", err)
	}
	if _, err := service.GetModule(ctx, audiencePrincipal, teamBoard.ID, gatedModule.ID); err != nil {
		t.Fatalf("audience member module access: %v", err)
	}
	if err := accessSvc.RemoveAudienceMember(ctx, admin, teamBoard.ID, audience.ID, access.Subject{Type: SubjectTypeTeam, ID: team.ID}); err != nil {
		t.Fatalf("remove audience member: %v", err)
	}
	if _, err := service.GetModule(ctx, audiencePrincipal, teamBoard.ID, gatedModule.ID); !errors.Is(err, ErrModuleNotAvailable) {
		t.Fatalf("withdrawn audience module access = %v, want unavailable", err)
	}

	// Access-group revocation denies a joined PRIVATE participant immediately.
	group, err := accessSvc.CreateAccessGroup(ctx, admin, "P1R Group")
	if err != nil {
		t.Fatalf("create access group: %v", err)
	}
	groupBoard, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "p1r-group", Name: "P1R Group Board", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end, AccessGroupUUIDs: []uuid.UUID{group.ID},
	})
	if err != nil {
		t.Fatalf("create group board: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE boards SET status='ACTIVE' WHERE id=$1`, groupBoard.ID); err != nil {
		t.Fatalf("activate group board: %v", err)
	}
	if _, err := service.GetBoard(ctx, pp3, groupBoard.ID); !errors.Is(err, ErrBoardAccessDenied) {
		t.Fatalf("non-member private read error = %v, want access denied", err)
	}
	groupCode := testJoinCode(t, db, groupBoard.ID, "P1R-GROUP-CODE-001")
	if _, err := service.JoinBoard(ctx, pp3, groupBoard.ID, groupCode); !errors.Is(err, ErrBoardAccessDenied) {
		t.Fatalf("non-member join error = %v, want access denied", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO access_group_members (group_id,user_id) VALUES ($1,$2)`, group.ID, p3); err != nil {
		t.Fatalf("add group member: %v", err)
	}
	if _, err := service.JoinBoard(ctx, pp3, groupBoard.ID, groupCode); err != nil {
		t.Fatalf("group member join: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM access_group_members WHERE group_id=$1 AND user_id=$2`, group.ID, p3); err != nil {
		t.Fatalf("revoke group member: %v", err)
	}
	if _, err := service.GetBoard(ctx, pp3, groupBoard.ID); !errors.Is(err, ErrBoardAccessDenied) {
		t.Fatalf("revoked group read = %v, want access denied", err)
	}

	// Foreign draft is rejected by the authoritative store transaction.
	draftTask, err := service.AdminCreateTask(ctx, admin, teamBoard.ID, module.ID, CreateTaskInput{
		Title: "Draft task", Type: TaskTypeManual, Category: "web", Points: 5, Visible: true,
		Submission: SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerManual},
		Instance:   TaskInstanceInput{Enabled: false},
	})
	if err != nil {
		t.Fatalf("create draft task: %v", err)
	}
	otherDraft := uuid.New()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO submission_drafts (id,board_id,module_id,resource_type,task_id,created_by_user_id,status,expires_at)
		VALUES ($1,$2,$3,'TASK',$4,$5,'OPEN',clock_timestamp()+interval '15 minutes')`,
		otherDraft, teamBoard.ID, module.ID, draftTask.ID, p2); err != nil {
		t.Fatalf("insert foreign draft: %v", err)
	}
	if _, err := NewPostgresStore(db).CreateSubmission(ctx, SubmissionParams{
		BoardID: teamBoard.ID, ModuleID: module.ID, TaskID: draftTask.ID, UserID: p1,
		SubmittedByUserID: p1, DraftID: &otherDraft, Status: SubmissionStatusPendingRev,
		IdempotencyKey: "p1r-foreign-draft-1", RequestHash: []byte("hash"),
	}, AuditEventParams{}); !errors.Is(err, ErrDraftForbidden) {
		t.Fatalf("foreign draft error = %v, want ErrDraftForbidden", err)
	}

	// A READY caller-owned draft is attached/consumed in the same transaction.
	ownDraft := uuid.New()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO submission_drafts (id,board_id,module_id,resource_type,task_id,created_by_user_id,status,expires_at)
		VALUES ($1,$2,$3,'TASK',$4,$5,'OPEN',clock_timestamp()+interval '15 minutes')`,
		ownDraft, teamBoard.ID, module.ID, draftTask.ID, p1); err != nil {
		t.Fatalf("insert own draft: %v", err)
	}
	fileID := uuid.New()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO file_objects (id,board_id,module_id,task_id,draft_id,uploader_user_id,purpose,
			filename,content_type,size_bytes,sha256,quarantine_bucket,quarantine_key,status,validated_at)
		VALUES ($1,$2,$3,$4,$5,$6,'SUBMISSION_DRAFT','answer.txt','text/plain',7,
			repeat('a',64),'quarantine','q/'||$7,'READY',clock_timestamp())`,
		fileID, teamBoard.ID, module.ID, draftTask.ID, ownDraft, p1, fileID.String()); err != nil {
		t.Fatalf("insert ready file: %v", err)
	}
	result, err := NewPostgresStore(db).CreateSubmission(ctx, SubmissionParams{
		BoardID: teamBoard.ID, ModuleID: module.ID, TaskID: draftTask.ID, UserID: p1,
		SubmittedByUserID: p1, DraftID: &ownDraft, Status: SubmissionStatusPendingRev,
		IdempotencyKey: "p1r-own-draft-0001", RequestHash: []byte("hash2"),
	}, AuditEventParams{})
	if err != nil {
		t.Fatalf("finalize own draft: %v", err)
	}
	if len(result.Submission.Files) != 1 || result.Submission.Files[0].UUID != fileID {
		t.Fatalf("finalized files = %+v", result.Submission.Files)
	}
	var draftStatus, fileStatus string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM submission_drafts WHERE id=$1`, ownDraft).Scan(&draftStatus); err != nil {
		t.Fatalf("read draft status: %v", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM file_objects WHERE id=$1`, fileID).Scan(&fileStatus); err != nil {
		t.Fatalf("read file status: %v", err)
	}
	if draftStatus != "CONSUMED" || fileStatus != "ATTACHED" {
		t.Fatalf("draft/file status = %s/%s, want CONSUMED/ATTACHED", draftStatus, fileStatus)
	}
	var linked int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM submission_files WHERE submission_id=$1 AND file_id=$2`, result.Submission.ID, fileID).Scan(&linked); err != nil || linked != 1 {
		t.Fatalf("submission_files linked=%d err=%v", linked, err)
	}
}

type fakeExternalScoreSource struct {
	subjectType string
	subjectID   uuid.UUID
	points      int
}

func (f fakeExternalScoreSource) ListBoardScores(context.Context, uuid.UUID) ([]ExternalSubjectScore, error) {
	return []ExternalSubjectScore{{SubjectType: f.subjectType, SubjectID: f.subjectID, Name: "external", Points: f.points}}, nil
}

func (f fakeExternalScoreSource) SubjectScore(context.Context, uuid.UUID, string, uuid.UUID) (int, error) {
	return f.points, nil
}

type fakeExternalSubmissionSource struct {
	submission Submission
}

func (f fakeExternalSubmissionSource) ListSubmissions(_ context.Context, _ authorization.Principal, _ uuid.UUID, filter SubmissionFilter) (Page[Submission], error) {
	return Page[Submission]{Items: []Submission{f.submission}, Limit: filter.Limit, Offset: filter.Offset, Total: 1}, nil
}

func (f fakeExternalSubmissionSource) GetSubmission(_ context.Context, _ authorization.Principal, _ uuid.UUID, _ uuid.UUID) (*Submission, error) {
	return &f.submission, nil
}

func (f fakeExternalSubmissionSource) ReviewSubmission(_ context.Context, _ authorization.Principal, _ uuid.UUID, _ uuid.UUID, input ReviewInput, _ string) (*Submission, error) {
	value := f.submission
	if input.Decision == ReviewDecisionAccept {
		score := 77
		value.Status = SubmissionStatusAccepted
		value.ScoreAwarded = &score
	}
	return &value, nil
}

// TestMyTeamMembersRevokedGroupAccessStopsRoster is the privacy guard: a
// participant whose live access-group membership is revoked can no longer read
// the team roster, even though the participant row still exists.
func TestMyTeamMembersRevokedGroupAccessStopsRoster(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the team roster revocation test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()
	authz := authorization.NewAuthorizer(db)
	accessSvc := access.NewService(db, authz)
	service := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(64),
		WithAccessPolicy(accessSvc), WithAccessService(accessSvc))

	adminID := createBoardUser(t, ctx, db, "roster_admin", []string{"admin"})
	playerID := createBoardUser(t, ctx, db, "roster_player", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	player := authorization.Principal{UserID: playerID, GlobalRoles: []string{"user"}}

	group, err := accessSvc.CreateAccessGroup(ctx, admin, "roster-gate")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2)`, group.ID, playerID); err != nil {
		t.Fatalf("add group member: %v", err)
	}
	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "roster-team", Name: "Roster Team", ParticipationMode: ParticipationTeam,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, AccessMode: AccessModeAudience,
		AccessGroupUUIDs: []uuid.UUID{group.ID}, RatingVisibility: RatingPublic,
		TeamSettings: &TeamSettings{MinMembers: 1, MaxMembers: 4, AllowSelfCreate: true, AllowLeaveAfterStart: true},
	})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	if _, err := service.store.UpdateBoardStatus(ctx, board.ID, BoardStatusActive,
		AuditEventParams{ActorUserID: adminID, Action: "test.activate", ResourceType: "board"}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := service.JoinBoard(ctx, player, board.ID, testJoinCode(t, db, board.ID, "P1R-ROSTER-CODE-01")); err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, err := service.CreateTeam(ctx, player, board.ID, "Roster Team One"); err != nil {
		t.Fatalf("create team: %v", err)
	}
	if _, err := service.MyTeamMembers(ctx, player, board.ID); err != nil {
		t.Fatalf("roster before revocation: %v", err)
	}

	// Revoke the live gate: the roster must stop being readable.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM access_group_members WHERE group_id=$1 AND user_id=$2`, group.ID, playerID); err != nil {
		t.Fatalf("revoke membership: %v", err)
	}
	if _, err := service.MyTeamMembers(ctx, player, board.ID); !errors.Is(err, ErrBoardAccessDenied) {
		t.Fatalf("roster after revocation err = %v, want board access denied", err)
	}
}
