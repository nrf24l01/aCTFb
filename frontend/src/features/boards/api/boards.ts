import { apiDelete, apiPatch, apiPost, apiRequest } from '@/shared/api/client'
import type {
  AdminBoard,
  BoardInviteResponse,
  AdminBoardCreateRequest,
  AdminBoardPatchRequest,
  AdminBoardsResponse,
  AdminModule,
  AdminModuleCreateRequest,
  AdminModulePatchRequest,
  AdminModulesResponse,
  AdminParticipantAddRequest,
  AdminSubmission,
  AdminSubmissionListResponse,
  AdminSubmissionReviewRequest,
  AdminTask,
  AdminTaskCreateRequest,
  AdminTaskPatchRequest,
  AdminTasksResponse,
  Board,
  BoardActionRequest,
  BoardAuditResponse,
  BoardListItem,
  BoardListResponse,
  BoardMe,
  BoardPermissionsResponse,
  BoardRoleBinding,
  BoardRoleBindingCreateRequest,
  BoardRoleBindingListResponse,
  BoardRoleBindingPatchRequest,
  FinalizeSubmissionRequest,
  Module,
  ModuleListResponse,
  Page,
  PageQuery,
  ScoreboardItem,
  ScoreboardResponse,
  Submission,
  SubmissionListResponse,
  SubmissionStatus,
  Task,
  TaskListResponse,
  UserSummary,
} from '@/shared/types/api'

function withQuery(path: string, query: Record<string, string | number | undefined>): string {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== '') params.set(key, String(value))
  }
  const suffix = params.toString()
  return suffix ? `${path}?${suffix}` : path
}

function pageQuery(query: PageQuery = {}): Record<string, string | number | undefined> {
  return { limit: query.limit, offset: query.offset, query: query.query }
}

export interface SubmissionListQuery extends PageQuery {
  moduleUuid?: string
  resourceType?: 'TASK' | 'PENTEST'
  status?: SubmissionStatus
}

function taskPath(boardUuid: string, moduleUuid: string): string {
  return `/boards/${boardUuid}/modules/${moduleUuid}`
}

export const boardsApi = {
  listBoards(query: PageQuery = {}): Promise<BoardListResponse> {
    return apiRequest<BoardListResponse>(withQuery('/boards', pageQuery(query)))
  },

  getBoard(boardUuid: string): Promise<Board> {
    return apiRequest<Board>(`/boards/${boardUuid}`)
  },

  joinBoard(boardUuid: string, inviteCode?: string): Promise<BoardMe> {
    return apiPost<BoardMe>(`/boards/${boardUuid}/join`, inviteCode ? { inviteCode } : {})
  },

  leaveBoard(boardUuid: string): Promise<void> {
    return apiPost<void>(`/boards/${boardUuid}/leave`, undefined)
  },

  getBoardMe(boardUuid: string): Promise<BoardMe> {
    return apiRequest<BoardMe>(`/boards/${boardUuid}/me`)
  },

  getScoreboard(boardUuid: string): Promise<ScoreboardResponse> {
    return apiRequest<ScoreboardResponse>(`/boards/${boardUuid}/scoreboard`)
  },

  getMyScoreboardEntry(boardUuid: string): Promise<ScoreboardItem> {
    return apiRequest<ScoreboardItem>(`/boards/${boardUuid}/scoreboard/me`)
  },

  listModules(boardUuid: string, query: PageQuery = {}): Promise<ModuleListResponse> {
    return apiRequest<ModuleListResponse>(withQuery(`/boards/${boardUuid}/modules`, pageQuery(query)))
  },

  getModule(boardUuid: string, moduleUuid: string): Promise<Module> {
    return apiRequest<Module>(`/boards/${boardUuid}/modules/${moduleUuid}`)
  },

  listTasks(boardUuid: string, moduleUuid: string, query: PageQuery = {}): Promise<TaskListResponse> {
    return apiRequest<TaskListResponse>(withQuery(`${taskPath(boardUuid, moduleUuid)}/tasks`, pageQuery(query)))
  },

  getTask(boardUuid: string, moduleUuid: string, taskUuid: string): Promise<Task> {
    return apiRequest<Task>(`${taskPath(boardUuid, moduleUuid)}/tasks/${taskUuid}`)
  },

  submitTask(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    idempotencyKey: string,
    text: string,
  ): Promise<Submission> {
    return boardsApi.submitTaskBody(boardUuid, moduleUuid, taskUuid, idempotencyKey, { text })
  },

  submitTaskBody(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    idempotencyKey: string,
    body: FinalizeSubmissionRequest,
  ): Promise<Submission> {
    return apiRequest<Submission>(`${taskPath(boardUuid, moduleUuid)}/tasks/${taskUuid}/submissions`, {
      method: 'POST',
      body,
      headers: { 'Idempotency-Key': idempotencyKey },
    })
  },

  listMySubmissions(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    query: PageQuery = {},
  ): Promise<SubmissionListResponse> {
    return apiRequest<SubmissionListResponse>(
      withQuery(`${taskPath(boardUuid, moduleUuid)}/tasks/${taskUuid}/submissions/me`, pageQuery(query)),
    )
  },

  getMyPermissions(boardUuid: string): Promise<BoardPermissionsResponse> {
    return apiRequest<BoardPermissionsResponse>(`/boards/${boardUuid}/me/permissions`)
  },

  getBoardInviteCode(boardUuid: string): Promise<BoardInviteResponse> {
    return apiRequest<BoardInviteResponse>(`/admin/boards/${boardUuid}/invite-code`)
  },

  rotateBoardInviteCode(boardUuid: string): Promise<BoardInviteResponse> {
    return apiPost<BoardInviteResponse>(`/admin/boards/${boardUuid}/invite-code`)
  },

  listRoleBindings(boardUuid: string, query: PageQuery = {}): Promise<BoardRoleBindingListResponse> {
    return apiRequest<BoardRoleBindingListResponse>(
      withQuery(`/admin/boards/${boardUuid}/role-bindings`, pageQuery(query)),
    )
  },

  createRoleBinding(boardUuid: string, body: BoardRoleBindingCreateRequest): Promise<BoardRoleBinding> {
    return apiPost<BoardRoleBinding>(`/admin/boards/${boardUuid}/role-bindings`, body)
  },

  updateRoleBinding(
    boardUuid: string,
    bindingUuid: string,
    body: BoardRoleBindingPatchRequest,
  ): Promise<BoardRoleBinding> {
    return apiPatch<BoardRoleBinding>(`/admin/boards/${boardUuid}/role-bindings/${bindingUuid}`, body)
  },

  revokeRoleBinding(boardUuid: string, bindingUuid: string): Promise<void> {
    return apiDelete(`/admin/boards/${boardUuid}/role-bindings/${bindingUuid}`)
  },

  listBoardAudit(boardUuid: string, query: PageQuery = {}): Promise<BoardAuditResponse> {
    return apiRequest<BoardAuditResponse>(withQuery(`/admin/boards/${boardUuid}/audit`, pageQuery(query)))
  },

  listParticipants(boardUuid: string, query: PageQuery = {}): Promise<Page<UserSummary>> {
    return apiRequest<Page<UserSummary>>(
      withQuery(`/admin/boards/${boardUuid}/participants`, pageQuery(query)),
    )
  },

  addParticipant(boardUuid: string, body: AdminParticipantAddRequest): Promise<void> {
    return apiPost<void>(`/admin/boards/${boardUuid}/participants`, body)
  },

  removeParticipant(boardUuid: string, userUuid: string): Promise<void> {
    return apiDelete(`/admin/boards/${boardUuid}/participants/${userUuid}`)
  },

  listAdminBoards(query: PageQuery = {}): Promise<AdminBoardsResponse> {
    return apiRequest<AdminBoardsResponse>(withQuery('/admin/boards', pageQuery(query)))
  },

  createBoard(body: AdminBoardCreateRequest): Promise<AdminBoard> {
    return apiPost<AdminBoard>('/admin/boards', body)
  },

  getAdminBoard(boardUuid: string): Promise<AdminBoard> {
    return apiRequest<AdminBoard>(`/admin/boards/${boardUuid}`)
  },

  updateBoard(boardUuid: string, body: AdminBoardPatchRequest): Promise<AdminBoard> {
    return apiPatch<AdminBoard>(`/admin/boards/${boardUuid}`, body)
  },

  deleteBoard(boardUuid: string): Promise<void> {
    return apiDelete(`/admin/boards/${boardUuid}`)
  },

  boardAction(boardUuid: string, action: BoardActionRequest['action']): Promise<Board> {
    const body: BoardActionRequest = { action }
    return apiPost<Board>(`/admin/boards/${boardUuid}/actions`, body)
  },

  listAdminModules(boardUuid: string, query: PageQuery = {}): Promise<AdminModulesResponse> {
    return apiRequest<AdminModulesResponse>(
      withQuery(`/admin/boards/${boardUuid}/modules`, pageQuery(query)),
    )
  },

  createModule(boardUuid: string, body: AdminModuleCreateRequest): Promise<AdminModule> {
    return apiPost<AdminModule>(`/admin/boards/${boardUuid}/modules`, body)
  },

  updateModule(boardUuid: string, moduleUuid: string, body: AdminModulePatchRequest): Promise<AdminModule> {
    return apiPatch<AdminModule>(`/admin/boards/${boardUuid}/modules/${moduleUuid}`, body)
  },

  deleteModule(boardUuid: string, moduleUuid: string): Promise<void> {
    return apiDelete(`/admin/boards/${boardUuid}/modules/${moduleUuid}`)
  },

  listAdminTasks(boardUuid: string, moduleUuid: string, query: PageQuery = {}): Promise<AdminTasksResponse> {
    return apiRequest<AdminTasksResponse>(
      withQuery(`/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks`, pageQuery(query)),
    )
  },

  createTask(
    boardUuid: string,
    moduleUuid: string,
    body: AdminTaskCreateRequest,
  ): Promise<AdminTask> {
    return apiPost<AdminTask>(`/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks`, body)
  },

  getAdminTask(boardUuid: string, moduleUuid: string, taskUuid: string): Promise<AdminTask> {
    return apiRequest<AdminTask>(`/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`)
  },

  updateTask(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    body: AdminTaskPatchRequest,
  ): Promise<AdminTask> {
    return apiPatch<AdminTask>(
      `/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`,
      body,
    )
  },

  deleteTask(boardUuid: string, moduleUuid: string, taskUuid: string): Promise<void> {
    return apiDelete(`/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`)
  },

  listAdminSubmissions(boardUuid: string, query: SubmissionListQuery = {}): Promise<AdminSubmissionListResponse> {
    return apiRequest<AdminSubmissionListResponse>(
      withQuery(`/admin/boards/${boardUuid}/submissions`, {
        ...pageQuery(query),
        moduleUuid: query.moduleUuid,
        resourceType: query.resourceType,
        status: query.status,
      }),
    )
  },

  getSubmission(boardUuid: string, submissionUuid: string): Promise<AdminSubmission> {
    return apiRequest<AdminSubmission>(`/admin/boards/${boardUuid}/submissions/${submissionUuid}`)
  },

  reviewSubmission(
    boardUuid: string,
    submissionUuid: string,
    idempotencyKey: string,
    body: AdminSubmissionReviewRequest,
  ): Promise<AdminSubmission> {
    return apiRequest<AdminSubmission>(
      `/admin/boards/${boardUuid}/submissions/${submissionUuid}/review`,
      {
        method: 'POST',
        body,
        headers: { 'Idempotency-Key': idempotencyKey },
      },
    )
  },
}

export type { BoardListItem, Module, Task, Submission }