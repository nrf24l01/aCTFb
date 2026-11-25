// Package http maps the existing /api/v2 generated OpenAPI models onto the
// boards domain service. It never defines a parallel API or a new envelope.
package http

import (
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/users"
)

// Handler owns the first-slice participant and owner/admin board routes.
type Handler struct {
	service    *boards.Service
	auth       *users.UserService
	fileRoutes bool
}

// HandlerOption customizes additive route ownership without changing existing
// call sites.
type HandlerOption func(*Handler)

// WithFileRoutes controls the legacy board-owned task file routes. Tests and
// text-only deployments keep them (fail closed without a files service). Boot
// disables them when the real files HTTP adapter is installed, so every
// method/path has exactly one handler.
func WithFileRoutes(enabled bool) HandlerOption {
	return func(h *Handler) { h.fileRoutes = enabled }
}

// NewHandler constructs the board HTTP adapter.
func NewHandler(service *boards.Service, auth *users.UserService, opts ...HandlerOption) *Handler {
	handler := &Handler{service: service, auth: auth, fileRoutes: true}
	for _, opt := range opts {
		if opt != nil {
			opt(handler)
		}
	}
	return handler
}

// RegisterRoutes installs exactly the 33 existing first-slice operations.
// Each scoped route runs the shared bearer middleware and an HTTP authorization
// preflight; the service re-checks before every write.
func (h *Handler) RegisterRoutes(e *echo.Echo) {
	if e == nil {
		return
	}
	requireUser := httpapi.RequireUser(h.auth)
	optionalUser := httpapi.OptionalUser(h.auth)
	boardRead := func(admin bool) echo.MiddlewareFunc { return h.requireBoardRead("boardUuid", admin) }
	boardPermission := func(permission authorization.Permission, admin bool) echo.MiddlewareFunc {
		return h.requirePermission(permission, "boardUuid", "", admin)
	}
	modulePermission := func(permission authorization.Permission, admin bool) echo.MiddlewareFunc {
		return h.requirePermission(permission, "boardUuid", "moduleUuid", admin)
	}

	e.GET("/api/v2/boards", h.ListBoards, optionalUser)
	e.GET("/api/v2/boards/:boardUuid", h.GetBoard, optionalUser)
	e.POST("/api/v2/boards/join", h.JoinBoardByCode, requireUser)
	e.POST("/api/v2/boards/:boardUuid/join", h.JoinBoard, requireUser)
	e.POST("/api/v2/boards/:boardUuid/leave", h.LeaveBoard, requireUser)
	e.GET("/api/v2/boards/:boardUuid/me", h.GetBoardMe, requireUser)
	e.GET("/api/v2/boards/:boardUuid/scoreboard", h.GetScoreboard, requireUser, boardRead(false))
	e.GET("/api/v2/boards/:boardUuid/scoreboard/me", h.GetMyScoreboardEntry, requireUser, boardRead(false))
	e.GET("/api/v2/boards/:boardUuid/modules", h.ListModules, requireUser, boardRead(false))
	e.GET("/api/v2/boards/:boardUuid/modules/:moduleUuid", h.GetModule, requireUser, boardRead(false))
	e.GET("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks", h.ListTasks, requireUser, boardRead(false))
	e.GET("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid", h.GetTask, requireUser, boardRead(false))
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submissions", h.SubmitTask,
		requireUser, modulePermission(authorization.PermissionSubmissionCreateOwn, false))
	e.GET("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submissions/me", h.ListMyTaskSubmissions,
		requireUser, modulePermission(authorization.PermissionSubmissionReadOwn, false))

	e.GET("/api/v2/admin/boards", h.AdminListBoards, requireUser,
		h.requirePermission(authorization.PermissionBoardCreate, "", "", true))
	e.POST("/api/v2/admin/boards", h.AdminCreateBoard, requireUser,
		h.requirePermission(authorization.PermissionBoardCreate, "", "", true))
	e.GET("/api/v2/admin/boards/:boardUuid", h.AdminGetBoard, requireUser, boardRead(true))
	e.PATCH("/api/v2/admin/boards/:boardUuid", h.AdminUpdateBoard, requireUser,
		boardPermission(authorization.PermissionBoardUpdate, true))
	e.DELETE("/api/v2/admin/boards/:boardUuid", h.AdminDeleteBoard, requireUser,
		boardPermission(authorization.PermissionBoardDelete, true))
	e.POST("/api/v2/admin/boards/:boardUuid/actions", h.AdminBoardAction, requireUser)
	e.GET("/api/v2/admin/boards/:boardUuid/join-code", h.GetBoardJoinCodeStatus, requireUser)
	e.POST("/api/v2/admin/boards/:boardUuid/join-code", h.SetBoardJoinCode, requireUser)
	e.GET("/api/v2/admin/boards/:boardUuid/modules", h.AdminListModules, requireUser, boardRead(true))
	e.POST("/api/v2/admin/boards/:boardUuid/modules", h.AdminCreateModule, requireUser,
		boardPermission(authorization.PermissionModuleCreate, true))
	e.PATCH("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid", h.AdminUpdateModule, requireUser,
		modulePermission(authorization.PermissionModuleUpdate, true))
	e.DELETE("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid", h.AdminDeleteModule, requireUser,
		modulePermission(authorization.PermissionModuleDelete, true))
	e.GET("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid", h.AdminGetModule, requireUser, boardRead(true))
	e.GET("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks", h.AdminListTasks, requireUser,
		modulePermission(authorization.PermissionModuleTasksManage, true))
	e.POST("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks", h.AdminCreateTask, requireUser,
		modulePermission(authorization.PermissionModuleTasksManage, true))
	e.POST("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/batch-visibility", h.AdminBatchTaskVisibility, requireUser,
		modulePermission(authorization.PermissionModuleTasksManage, true))
	e.GET("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid", h.AdminGetTask, requireUser,
		modulePermission(authorization.PermissionModuleTasksManage, true))
	e.GET("/api/v2/admin/boards/:boardUuid/scoreboard", h.AdminGetScoreboard, requireUser,
		boardPermission(authorization.PermissionBoardRatingManage, true))
	e.PATCH("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid", h.AdminUpdateTask, requireUser,
		modulePermission(authorization.PermissionModuleTasksManage, true))
	e.DELETE("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid", h.AdminDeleteTask, requireUser,
		modulePermission(authorization.PermissionModuleTasksManage, true))
	e.GET("/api/v2/admin/boards/:boardUuid/submissions", h.AdminListSubmissions, requireUser)
	e.GET("/api/v2/admin/boards/:boardUuid/submissions/:submissionUuid", h.AdminGetSubmission, requireUser)
	e.POST("/api/v2/admin/boards/:boardUuid/submissions/:submissionUuid/review", h.AdminReviewSubmission, requireUser)
	e.GET("/api/v2/admin/boards/:boardUuid/participants", h.AdminListParticipants, requireUser,
		boardPermission(authorization.PermissionBoardParticipantsManage, true))
	e.POST("/api/v2/admin/boards/:boardUuid/participants", h.AdminAddParticipant, requireUser,
		boardPermission(authorization.PermissionBoardParticipantsManage, true))
	e.DELETE("/api/v2/admin/boards/:boardUuid/participants/:userUuid", h.AdminRemoveParticipant, requireUser,
		boardPermission(authorization.PermissionBoardParticipantsManage, true))

	// P1R teams
	e.GET("/api/v2/boards/:boardUuid/team", h.GetMyTeam, requireUser)
	e.GET("/api/v2/boards/:boardUuid/team/members", h.MyTeamMembers, requireUser)
	e.PATCH("/api/v2/boards/:boardUuid/team", h.UpdateMyTeam, requireUser)
	e.POST("/api/v2/boards/:boardUuid/teams", h.CreateTeam, requireUser)
	e.POST("/api/v2/boards/:boardUuid/teams/join", h.JoinTeam, requireUser)
	e.GET("/api/v2/boards/:boardUuid/team/invite-key", h.GetTeamInviteKey, requireUser)
	e.POST("/api/v2/boards/:boardUuid/team/invite-key/rotate", h.RotateTeamInviteKey, requireUser)
	e.POST("/api/v2/boards/:boardUuid/team/leave", h.LeaveTeam, requireUser)
	e.DELETE("/api/v2/boards/:boardUuid/team/members/:userUuid", h.RemoveTeamMember, requireUser)
	e.POST("/api/v2/boards/:boardUuid/team/captain", h.TransferTeamCaptain, requireUser)

	e.GET("/api/v2/boards/:boardUuid/audiences/me", h.ListMyAudiences, requireUser, boardRead(false))

	e.GET("/api/v2/admin/boards/:boardUuid/teams", h.AdminListTeams, requireUser, boardPermission(authorization.PermissionBoardParticipantsManage, true))
	e.POST("/api/v2/admin/boards/:boardUuid/teams", h.AdminCreateTeam, requireUser, boardPermission(authorization.PermissionBoardParticipantsManage, true))
	e.GET("/api/v2/admin/boards/:boardUuid/teams/:teamUuid", h.AdminGetTeam, requireUser, boardPermission(authorization.PermissionBoardParticipantsManage, true))
	e.PATCH("/api/v2/admin/boards/:boardUuid/teams/:teamUuid", h.AdminUpdateTeam, requireUser, boardPermission(authorization.PermissionBoardParticipantsManage, true))
	e.DELETE("/api/v2/admin/boards/:boardUuid/teams/:teamUuid", h.AdminDeleteTeam, requireUser, boardPermission(authorization.PermissionBoardParticipantsManage, true))
	e.POST("/api/v2/admin/boards/:boardUuid/teams/:teamUuid/members", h.AdminAddTeamMember, requireUser, boardPermission(authorization.PermissionBoardParticipantsManage, true))
	e.DELETE("/api/v2/admin/boards/:boardUuid/teams/:teamUuid/members/:userUuid", h.AdminRemoveTeamMember, requireUser, boardPermission(authorization.PermissionBoardParticipantsManage, true))

	e.GET("/api/v2/admin/boards/:boardUuid/audiences", h.AdminListAudiences, requireUser, boardPermission(authorization.PermissionBoardAudiencesManage, true))
	e.POST("/api/v2/admin/boards/:boardUuid/audiences", h.AdminCreateAudience, requireUser, boardPermission(authorization.PermissionBoardAudiencesManage, true))
	e.GET("/api/v2/admin/boards/:boardUuid/audiences/:audienceUuid", h.AdminGetAudience, requireUser, boardPermission(authorization.PermissionBoardAudiencesManage, true))
	e.PATCH("/api/v2/admin/boards/:boardUuid/audiences/:audienceUuid", h.AdminUpdateAudience, requireUser, boardPermission(authorization.PermissionBoardAudiencesManage, true))
	e.DELETE("/api/v2/admin/boards/:boardUuid/audiences/:audienceUuid", h.AdminDeleteAudience, requireUser, boardPermission(authorization.PermissionBoardAudiencesManage, true))
	e.GET("/api/v2/admin/boards/:boardUuid/audiences/:audienceUuid/members", h.AdminListAudienceMembers, requireUser, boardPermission(authorization.PermissionBoardAudiencesManage, true))
	e.POST("/api/v2/admin/boards/:boardUuid/audiences/:audienceUuid/members", h.AdminAddAudienceMember, requireUser, boardPermission(authorization.PermissionBoardAudiencesManage, true))
	e.DELETE("/api/v2/admin/boards/:boardUuid/audiences/:audienceUuid/members/:subjectType/:subjectUuid", h.AdminRemoveAudienceMember, requireUser, boardPermission(authorization.PermissionBoardAudiencesManage, true))

	e.GET("/api/v2/admin/boards/:boardUuid/invite-code", h.GetBoardInviteCode, requireUser, boardPermission(authorization.PermissionBoardUpdate, true))
	e.POST("/api/v2/admin/boards/:boardUuid/invite-code", h.RotateBoardInviteCode, requireUser, boardPermission(authorization.PermissionBoardUpdate, true))

	e.GET("/api/v2/admin/boards/:boardUuid/score-adjustments", h.AdminListScoreAdjustments, requireUser, boardPermission(authorization.PermissionBoardRatingManage, true))
	e.POST("/api/v2/admin/boards/:boardUuid/score-adjustments", h.AdminCreateScoreAdjustment, requireUser, boardPermission(authorization.PermissionBoardRatingManage, true))

	// P1R task submission drafts/files. When the real files HTTP adapter is
	// installed, boot disables these legacy handlers to avoid duplicate routes.
	if h.fileRoutes {
		e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts", h.CreateTaskSubmissionDraft, requireUser)
		e.GET("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts/:draftUuid", h.GetTaskSubmissionDraft, requireUser)
		e.DELETE("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts/:draftUuid", h.DiscardTaskSubmissionDraft, requireUser)
		e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts/:draftUuid/files/presign", h.PresignTaskSubmissionFile, requireUser)
		e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts/:draftUuid/files/:fileUuid/complete", h.CompleteTaskSubmissionFile, requireUser)
		e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/:fileUuid/download-url", h.DownloadTaskAttachment, requireUser)
		e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submissions/:submissionUuid/files/:fileUuid/download-url", h.DownloadOwnTaskSubmissionFile, requireUser)
		e.POST("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/presign", h.AdminPresignTaskAttachment, requireUser, modulePermission(authorization.PermissionModuleTasksManage, true))
		e.POST("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/:fileUuid/complete", h.AdminCompleteTaskAttachment, requireUser, modulePermission(authorization.PermissionModuleTasksManage, true))
		e.POST("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/:fileUuid/download-url", h.AdminDownloadTaskAttachment, requireUser, modulePermission(authorization.PermissionModuleTasksManage, true))
		e.DELETE("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/:fileUuid", h.AdminDeleteTaskAttachment, requireUser, modulePermission(authorization.PermissionModuleTasksManage, true))
		e.POST("/api/v2/admin/boards/:boardUuid/submissions/:submissionUuid/files/:fileUuid/download-url", h.AdminDownloadSubmissionFile, requireUser)
	}
}
