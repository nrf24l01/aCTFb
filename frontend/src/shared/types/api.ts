/**
 * JSON shapes copied from docs/swagger.yaml (definitions) plus the additive
 * scoped-RBAC definitions approved in docs/p1-core-contracts.md.
 *
 * Do not invent fields. If the backend contract changes, change this file
 * from Swagger, not from a component's assumptions.
 */

export type ParticipationMode = 'INDIVIDUAL' | 'TEAM'
export type RegistrationMode = 'OPEN' | 'INVITE_ONLY' | 'CLOSED'
export type BoardStatus = 'DRAFT' | 'SCHEDULED' | 'ACTIVE' | 'FINISHED' | 'ARCHIVED'
export type BoardVisibility = 'PUBLIC' | 'PRIVATE'
export type BoardAccessMode = 'PUBLIC' | 'AUDIENCE' | 'INVITE' | 'LEGACY'
export type RatingVisibility = 'PUBLIC' | 'HIDDEN'
export type ModuleType = 'TASKS' | 'ATTACK_DEFENSE' | 'PENTEST' | 'EXTENSION'
export type ModuleStatus = 'DISABLED' | 'UPCOMING' | 'ACTIVE' | 'FINISHED'
export type TaskType = 'FLAG' | 'MANUAL' | 'INSTANCE' | 'EXPLOIT' | 'PATCH' | 'PENTEST_REPORT' | 'EXTENSION'
export type TaskStatus = 'LOCKED' | 'AVAILABLE' | 'SOLVED'
export type SubmissionMode = 'NONE' | 'TEXT' | 'FILES' | 'TEXT_AND_FILES' | 'TEXT_OR_FILES'
export type SubmissionChecker = 'AUTOMATIC' | 'MANUAL' | 'PLUGIN'
export type SubmissionStatus = 'PENDING' | 'CHECKING' | 'PENDING_REVIEW' | 'ACCEPTED' | 'REJECTED' | 'ERROR'
export type SubjectType = 'USER' | 'TEAM'
export type ReviewDecision = 'ACCEPT' | 'REJECT'
export type GlobalRole = 'user' | 'admin'
export type BoardRole = 'board_owner' | 'board_editor' | 'reviewer'
export type PermissionCode =
  | 'board.create'
  | 'board.read'
  | 'board.update'
  | 'board.publish'
  | 'board.delete'
  | 'board.staff.manage'
  | 'board.participants.manage'
  | 'board.rating.manage'
  | 'board.audit.read'
  | 'module.create'
  | 'module.update'
  | 'module.delete'
  | 'module.tasks.manage'
  | 'module.submissions.read'
  | 'module.submissions.review'
  | 'submission.create_own'
  | 'submission.read_own'
  | 'platform.users.manage'
  | 'platform.access_groups.manage'
  | 'platform.integrations.manage'
  | 'platform.audit.read'
  | 'board.audiences.manage'
  | 'deployment_templates.use'
  | 'platform.deployment_templates.publish'
  | 'module.instances.admin'
  | 'instance.start_own'
  | 'instance.manage_own'
  | 'team.manage_own'
  | 'plugins.manage'
  // Plugin registry permissions are namespaced strings; the core union above
  // remains the documented core catalog.
  | (string & {})

export interface Page<T> {
  items: T[]
  limit: number
  offset: number
  total: number
}

export interface PageQuery {
  limit?: number
  offset?: number
  query?: string
}

export interface UserSummary {
  uuid: string
  username: string
}

export interface Me {
  uuid: string
  email: string
  username: string
  emailVerified: boolean
  roles: GlobalRole[]
  createdAt: string
}

export interface LoginRequest {
  username: string
  password: string
}

export interface RegisterRequest {
  email: string
  username: string
  password: string
}

export interface TokenResponse {
  accessToken: string
  tokenType: 'Bearer'
  expiresIn: number
}

export interface LoginResponse extends TokenResponse {
  user: Me
}

export interface RegisterResponse extends TokenResponse {
  emailVerified: boolean
  user: Me
}

export interface StatusResponse {
  status: string
  message?: string
}

export interface MePatchRequest {
  username?: string
}

export interface ChangePasswordRequest {
  currentPassword: string
  newPassword: string
}

export interface PasswordResetRequest {
  email: string
}

export interface PasswordResetConfirmRequest {
  token: string
  newPassword: string
}

export interface EmailVerificationConfirmRequest {
  token: string
}

export interface RatingConfig {
  visibility: RatingVisibility
}

export interface BoardListItem {
  uuid: string
  slug: string
  name: string
  moduleTypes: ModuleType[]
  modulesCount: number
  participationMode: ParticipationMode
  registrationMode: RegistrationMode
  status: BoardStatus
  rating: RatingConfig
  startsAt?: string | null
  endsAt?: string | null
  joined: boolean
  accessMode?: BoardAccessMode
}

export interface Board extends BoardListItem {
  description: string
  visibility: BoardVisibility
}

export interface BoardJoinRequest {
  inviteCode?: string
}

export interface BoardMe {
  joined: boolean
  joinedAt?: string | null
  participationMode: ParticipationMode
  team?: Team | null
  score?: number | null
  place?: number | null
  audiences: AudienceSummary[]
}

export interface ModuleListItem {
  uuid: string
  boardUuid: string
  name: string
  type: ModuleType
  allowedTaskTypes: TaskType[]
  status: ModuleStatus
  audienceUuids: string[]
  order: number
  extensionKey?: string | null
  startsAt?: string | null
  endsAt?: string | null
}

export interface Module extends ModuleListItem {
  description: string
}

export interface TaskListItem {
  uuid: string
  moduleUuid: string
  type: TaskType
  title: string
  category: string
  points: number
  status: TaskStatus
  solved: boolean
  solvesCount?: number
  instanceRequired: boolean
}

export interface FileRef {
  uuid: string
  filename: string
  contentType: string
  size: number
  sha256: string
}

export interface SubmissionTextPolicy {
  required: boolean
  minLength: number
  maxLength: number
}

export interface SubmissionFilesPolicy {
  required: boolean
  minCount: number
  maxCount: number
  maxSizeBytes: number
  allowedContentTypes: string[]
}

export interface SubmissionPolicy {
  mode: SubmissionMode
  checker: SubmissionChecker
  text?: SubmissionTextPolicy | null
  files?: SubmissionFilesPolicy | null
  checkerKey?: string | null
}

export interface TaskInstanceSummary {
  required: boolean
  status:
    | 'NOT_CREATED'
    | 'PROVISIONING'
    | 'RUNNING'
    | 'RESETTING'
    | 'DESTROYING'
    | 'ERROR'
    | 'EXPIRED'
  ttlSeconds?: number | null
  canStart: boolean
  cannotStartReason?: string | null
}

export interface Task extends TaskListItem {
  description: string
  attachments: FileRef[]
  submission: SubmissionPolicy
  instance: TaskInstanceSummary
  extensionKey?: string | null
}

export interface Submission {
  uuid: string
  status: SubmissionStatus
  text?: string | null
  files: FileRef[]
  scoreAwarded?: number | null
  createdAt: string
  reviewedAt?: string | null
  message?: string | null
}

export interface ScoreSubject {
  type: SubjectType
  uuid: string
  name: string
}

export interface ScoreboardItem {
  place: number
  subject: ScoreSubject
  score: number
}

export interface ScoreboardResponse {
  items: ScoreboardItem[]
  updatedAt: string
}

export interface FinalizeSubmissionRequest {
  text?: string
  draftUuid?: string
}

export interface AdminBoardCreateRequest {
  slug: string
  name: string
  description?: string
  participationMode: ParticipationMode
  registrationMode: RegistrationMode
  visibility: BoardVisibility
  accessMode?: BoardAccessMode
  startsAt?: string | null
  endsAt?: string | null
  rating: RatingConfig
  accessGroupUuids?: string[]
  teamSettings?: TeamSettings | null
}

export interface AdminBoardPatchRequest {
  slug?: string
  name?: string
  description?: string
  participationMode?: ParticipationMode
  registrationMode?: RegistrationMode
  visibility?: BoardVisibility
  accessMode?: BoardAccessMode
  startsAt?: string | null
  endsAt?: string | null
  rating?: RatingConfig
  accessGroupUuids?: string[]
  teamSettings?: TeamSettings | null
}

export interface BoardActionRequest {
  action: 'PUBLISH' | 'UNPUBLISH' | 'ARCHIVE'
}

export interface AdminModuleCreateRequest {
  name: string
  description?: string
  type: ModuleType
  allowedTaskTypes: TaskType[]
  audienceUuids?: string[]
  enabled?: boolean
  order?: number
  startsAt?: string | null
  endsAt?: string | null
  taskInstanceSettings?: TaskInstanceModuleSettings | null
  extensionKey?: string | null
}

export interface AdminModulePatchRequest {
  name?: string
  description?: string
  allowedTaskTypes?: TaskType[]
  audienceUuids?: string[]
  enabled?: boolean
  order?: number
  startsAt?: string | null
  endsAt?: string | null
  taskInstanceSettings?: TaskInstanceModuleSettings | null
  extensionKey?: string | null
}

export interface AdminModule extends Module {
  enabled: boolean
  taskInstanceSettings?: TaskInstanceModuleSettings | null
  legacyMixed?: boolean
}

export interface TaskInstanceExtensionPolicy {
  enabled: boolean
  seconds: number
  maxCount: number
}

export interface TaskInstanceConfig {
  enabled: boolean
  providerIntegrationUuid?: string | null
  deploymentTemplateUuid?: string | null
  templateVersion?: number | null
  parameters?: Record<string, string>
  secretParameters?: Record<string, string>
  /** Initial lifetime override. null/omitted inherits the module default. */
  ttlSeconds?: number | null
  /** Maximum total lifetime override. null/omitted inherits the module maximum. */
  maxTtlSeconds?: number | null
  /** Student extension override. null/omitted inherits the module policy. */
  extension?: TaskInstanceExtensionPolicy | null
  nodes?: InstanceNodeConfig[]
}

export interface AdminTaskCreateRequest {
  title: string
  type: TaskType
  category: string
  description: string
  points: number
  visible: boolean
  submission: SubmissionPolicy
  acceptedAnswers?: string[]
  instance: TaskInstanceConfig
  extensionKey?: string | null
}

export interface AdminTaskPatchRequest {
  title?: string
  category?: string
  description?: string
  points?: number
  visible?: boolean
  submission?: SubmissionPolicy
  acceptedAnswers?: string[]
  instance?: TaskInstanceConfig
  extensionKey?: string | null
}

export interface AdminTask extends Task {
  visible: boolean
  answerConfiguration: {
    configured: boolean
    acceptedAnswersCount: number
  }
  instanceConfig: TaskInstanceConfig
}

export interface AdminSubmissionReviewRequest {
  decision: ReviewDecision
  message?: string | null
}

export interface AdminSubmission extends Submission {
  resourceType: 'TASK' | 'PENTEST'
  boardUuid: string
  moduleUuid: string
  taskUuid?: string | null
  subject: ScoreSubject
  submittedByUserUuid?: string
}

export interface AdminParticipantAddRequest {
  userUuid: string
}

export interface BoardPermissionsResponse {
  boardUuid: string
  roles: BoardRole[]
  permissions: PermissionCode[]
  modules: {
    moduleUuid: string
    roles: BoardRole[]
    permissions: PermissionCode[]
  }[]
}

export interface BoardRoleBinding {
  uuid: string
  boardUuid: string
  moduleUuid?: string | null
  user: UserSummary
  role: BoardRole
  grantedBy: UserSummary
  expiresAt?: string | null
  revokedAt?: string | null
  createdAt: string
}

export interface BoardRoleBindingCreateRequest {
  userUuid: string
  role: BoardRole
  moduleUuid?: string | null
  expiresAt?: string | null
}

export interface BoardRoleBindingPatchRequest {
  expiresAt: string | null
}

export interface BoardAuditEvent {
  uuid: string
  action: string
  actor: UserSummary
  boardUuid: string
  moduleUuid?: string | null
  resourceType: string
  resourceId?: string | null
  details?: Record<string, unknown>
  createdAt: string
}

export interface BoardAuditResponse extends Page<BoardAuditEvent> {}

export interface FieldError {
  field: string
  issue: string
  rejectedValue?: unknown
  code?: string
}

export interface ApiErrorBody {
  code: string
  message: string
  traceId: string
  timestamp: string
  path: string
  details?: Record<string, unknown>
  fieldErrors?: FieldError[]
}

export type BoardListResponse = Page<BoardListItem>
export type ModuleListResponse = Page<ModuleListItem>
export type AdminModulesResponse = Page<AdminModule>
export type TaskListResponse = Page<TaskListItem>
export type AdminTasksResponse = Page<AdminTask>
export type SubmissionListResponse = Page<Submission>
export type AdminSubmissionListResponse = Page<AdminSubmission>
export type AdminBoardsResponse = Page<Board>
export type BoardRoleBindingListResponse = Page<BoardRoleBinding>
/* ------------------------------------------------------------------ */
/* Remaining P1R–P5 models (docs/swagger.yaml definitions)             */
/* ------------------------------------------------------------------ */

export interface Team {
  uuid: string
  boardUuid: string
  name: string
  captainUuid: string
  membersCount: number
  maxMembers: number
}

export interface TeamCreateRequest {
  name: string
}

export interface TeamPatchRequest {
  name?: string
}

export interface TeamJoinRequest {
  inviteKey: string
}

export interface TeamInviteResponse {
  inviteKey: string
  expiresAt?: string | null
}

export interface TeamSettings {
  minMembers: number
  maxMembers: number
  allowSelfCreate: boolean
  allowLeaveAfterStart: boolean
}

export interface TransferCaptainRequest {
  userUuid: string
}

export interface AdminTeamMemberAddRequest {
  userUuid: string
}

export interface AdminBoard extends Board {
  teamSettings?: TeamSettings | null
  accessGroupUuids: string[]
}

export interface AccessGroup {
  uuid: string
  name: string
  usersCount: number
}

export interface AccessGroupCreateRequest {
  name: string
}

export interface AccessGroupPatchRequest {
  name?: string
}

export interface AdminUser {
  uuid: string
  email: string
  username: string
  roles: GlobalRole[]
  accessGroups: { uuid: string; name: string }[]
  emailVerified: boolean
  disabled: boolean
  createdAt: string
  updatedAt: string
}

export interface AdminUserCreateRequest {
  email: string
  username: string
  password: string
  emailVerified?: boolean
  disabled?: boolean
  roles?: GlobalRole[]
  accessGroupUuids?: string[]
}

export interface AdminUserPatchRequest {
  email?: string
  username?: string
  emailVerified?: boolean
  disabled?: boolean
  roles?: GlobalRole[]
  accessGroupUuids?: string[]
}

export interface Audience {
  uuid: string
  boardUuid: string
  name: string
  description: string
  membersCount: number
}

export interface AudienceSummary {
  uuid: string
  name: string
}

export interface AudienceCreateRequest {
  name: string
  description?: string
}

export interface AudiencePatchRequest {
  name?: string
  description?: string
}

export interface AudienceMemberCreateRequest {
  subjectType: SubjectType
  subjectUuid: string
}

export interface AudienceMember {
  audienceUuid: string
  subject: ScoreSubject
  assignedAt: string
}

export interface BoardInviteResponse {
  inviteCode: string
  expiresAt?: string | null
}

export interface SubmissionDraft {
  uuid: string
  resourceType: 'TASK' | 'PENTEST'
  moduleUuid: string
  boardUuid: string
  taskUuid?: string | null
  status: 'OPEN' | 'CONSUMED' | 'EXPIRED'
  createdAt: string
  expiresAt: string
  files: PendingFile[]
}

export type FileValidationStatus =
  | 'AWAITING_UPLOAD'
  | 'VALIDATING'
  | 'READY'
  | 'REJECTED'
  | 'ATTACHED'
  | 'EXPIRED'

export interface PendingFile {
  uuid: string
  filename: string
  contentType: string
  size: number
  sha256: string
  status: FileValidationStatus
  rejectionCode?: string | null
}

export interface ScopedFilePresignRequest {
  filename: string
  contentType: string
  size: number
  sha256: string
}

export interface ScopedFilePresignResponse {
  fileUuid: string
  method: 'PUT'
  uploadUrl: string
  requiredHeaders: Record<string, string>
  expiresAt: string
}

export interface DownloadUrlResponse {
  url: string
  expiresAt: string
}

export interface TaskInstanceModuleSettings {
  maxConcurrentInstancesPerSubject: number
  defaultTtlSeconds: number
  maxTtlSeconds: number
  resetEnabled: boolean
  extension?: TaskInstanceExtensionPolicy
}

export interface InstanceNodeConfig {
  key: string
  name: string
  kind: 'CONTAINER' | 'VM'
  source?: {
    type: 'OCI_IMAGE' | 'VM_IMAGE' | 'VM_TEMPLATE'
    ref: string
  }
  resources?: {
    cpuMillicores?: number
    memoryMiB?: number
    diskMiB?: number
  }
  driverOptions?: Record<string, unknown>
  image?: string
  templateVmid?: number
  cpuCores?: number
  memoryMiB?: number
  endpoints: Array<{
    key: string
    protocol: string
    targetPort: number
    exposure: 'PUBLIC' | 'PRIVATE'
    display?: { label?: string; commandTemplate?: string }
  }>
  credentials?: {
    username?: { type: 'NONE' | 'STATIC' | 'GENERATED'; value?: string; length?: number }
    password?: { type: 'NONE' | 'STATIC' | 'GENERATED'; value?: string; length?: number }
    visibleToParticipant?: boolean
  }
}

export interface AccessEndpoint {
  key: string
  label?: string
  type: string
  host: string
  port: number
  url?: string | null
  exposure: 'PUBLIC' | 'PRIVATE'
  command?: string | null
  credentials?: unknown
}

export interface InstanceNode {
  uuid: string
  key: string
  name: string
  kind: 'CONTAINER' | 'VM'
  status: 'PROVISIONING' | 'RUNNING' | 'ERROR'
  endpoints: AccessEndpoint[]
}

export interface Instance {
  uuid: string
  boardUuid: string
  moduleUuid: string
  scope: 'TASK' | 'PENTEST'
  scopeUuid?: string | null
  owner: ScoreSubject
  status: 'PROVISIONING' | 'RUNNING' | 'RESETTING' | 'DESTROYING' | 'ERROR' | 'EXPIRED'
  createdAt: string
  startedAt?: string | null
  expiresAt?: string | null
  remainingSeconds?: number | null
  extensionCount?: number
  /** Server-computed; never recompute authorization in the client. */
  canExtend?: boolean | null
  canReset?: boolean | null
  extensionSeconds?: number | null
  extensionsRemaining?: number | null
  nodes: InstanceNode[]
}

export interface InstanceActionRequest {
  action: 'RESET' | 'DESTROY' | 'EXTEND'
}

export interface IntegrationSummary {
  uuid: string
  type: string
  kind?: 'VM' | 'CONTAINER' | null
  driver?: 'proxmox' | 'kubevirt' | 'docker' | 'podman' | 'kubernetes' | null
  name: string
  baseUrl?: string | null
  status: 'CONNECTED' | 'ERROR' | 'UNKNOWN'
  credentialsConfigured: boolean
  publicationPolicyConfigured?: boolean
}

export interface IntegrationCreateRequest {
  type: string
  kind?: 'VM' | 'CONTAINER' | null
  driver?: 'proxmox' | 'kubevirt' | 'docker' | 'podman' | 'kubernetes' | null
  name: string
  baseUrl?: string
  credentials?: Record<string, string>
  settings?: Record<string, unknown>
  instancePublicationPolicy?: {
    publicTcpHost?: string
    publicPortFrom?: number
    publicPortTo?: number
    httpBaseDomain?: string
    forceHttps?: boolean
  } | null
}

export interface IntegrationPatchRequest {
  kind?: 'VM' | 'CONTAINER' | null
  driver?: 'proxmox' | 'kubevirt' | 'docker' | 'podman' | 'kubernetes' | null
  name?: string
  baseUrl?: string
  credentials?: Record<string, string>
  settings?: Record<string, unknown>
  instancePublicationPolicy?: IntegrationCreateRequest['instancePublicationPolicy']
}

export interface IntegrationTestResponse {
  ok: boolean
  message?: string
}

export interface ValidationFinding {
  path: string
  code: string
  message: string
  severity: 'error' | 'warning'
}

export interface DeploymentTemplate {
  uuid: string
  slug: string
  name: string
  description: string
  createdAt: string
  updatedAt: string
}

export interface DeploymentTemplateFile {
  path: string
  contentBase64: string
}

export interface DeploymentTemplateDraft {
  templateUuid: string
  manifestYaml: string
  files: DeploymentTemplateFile[]
  updatedAt: string
}

export interface DeploymentTemplateDraftRequest {
  manifestYaml: string
  files: DeploymentTemplateFile[]
}

export interface DeploymentTemplateValidation {
  valid: boolean
  findings: ValidationFinding[]
}

export interface DeploymentTemplatePreview {
  nodes: Array<{
    key: string
    name: string
    kind: 'VM' | 'CONTAINER'
    action: 'CREATE' | 'UPDATE' | 'RECREATE' | 'DELETE' | 'NONE'
    findings?: ValidationFinding[]
  }>
  endpoints: Array<{
    key: string
    nodeKey: string
    protocol: string
    exposure: 'PUBLIC' | 'PRIVATE'
  }>
  warnings: string[]
}

export interface DeploymentTemplateVersion {
  uuid: string
  templateUuid: string
  version: number
  digest: string
  publishedAt: string
  publishedBy: string
}

export interface DeploymentTemplateDiffEntry {
  kind: 'NODE' | 'PARAMETER' | 'FILE' | 'ENDPOINT' | 'CHECK' | 'LIFECYCLE'
  path: string
  change: 'ADDED' | 'REMOVED' | 'CHANGED'
  before?: string | null
  after?: string | null
}

export interface DeploymentTemplateDiff {
  fromVersion: number
  toVersion: number
  changes: DeploymentTemplateDiffEntry[]
}

export interface DeploymentTemplatePublishRequest {
  versionNote: string
}

export interface DeploymentTemplateAllowlistTarget {
  kind: 'VM' | 'CONTAINER'
  integrationUuid: string
}

export interface DeploymentTemplateAllowlist {
  uuid: string
  templateVersionUuid: string
  boardUuid?: string | null
  moduleUuid?: string | null
  targets: DeploymentTemplateAllowlistTarget[]
  parametersPolicy?: Record<string, unknown> | null
  approvedByUuid: string
  createdAt: string
  expiresAt?: string | null
  revokedAt?: string | null
}

export interface DeploymentTemplateAllowlistCreateRequest {
  templateVersionUuid: string
  boardUuid?: string | null
  moduleUuid?: string | null
  targets: DeploymentTemplateAllowlistTarget[]
  parametersPolicy?: Record<string, unknown> | null
  expiresAt?: string | null
}

export interface PentestModuleInfo {
  attachments: FileRef[]
  submission: SubmissionPolicy
}

export interface ADVulnerabilityProgress {
  uuid: string
  key: string
  title: string
  status: 'FOUND' | 'PATCHED'
  currentPatchValid?: boolean | null
  foundAt: string
  patchedAt?: string | null
}

export interface ADProgress {
  totalVulnerabilities: number
  foundCount: number
  patchedCount: number
  score: number
  vulnerabilities: ADVulnerabilityProgress[]
}

export interface ADRunSummary {
  uuid: string
  moduleUuid: string
  kind: 'EXPLOIT' | 'PATCH'
  commitSha: string
  status: 'QUEUED' | 'RUNNING' | 'PASSED' | 'FAILED' | 'ERROR' | 'CANCELED'
  createdAt: string
  startedAt?: string | null
  finishedAt?: string | null
  summary?: Record<string, number>
}

export interface ADRun extends ADRunSummary {
  results?: Array<Record<string, unknown>>
  regressionTests?: Record<string, number> | null
  securityTests?: Record<string, number> | null
}

export interface ADRunCreateRequest {
  kind: 'EXPLOIT' | 'PATCH'
  commitSha?: string
}

export interface ADRunAccepted {
  runUuid: string
  status: 'QUEUED'
}

export interface ADConfig {
  defaultSourceBranch: string
  defaultExploitBranch: string
  runnerImage: string
  runTimeoutSeconds: number
  maxConcurrentRunsPerSubject: number
  manualRerunEnabled: boolean
}

export interface ADVulnerabilityAdmin {
  uuid: string
  key: string
  title: string
  description: string
  exploitPoints: number
  patchPoints: number
  exploitTestSelector: string
  patchTestSelector: string
}

export interface ADVulnerabilityCreateRequest {
  key: string
  title: string
  description?: string
  exploitPoints: number
  patchPoints: number
  exploitTestSelector: string
  patchTestSelector: string
}

export interface RepositorySpec {
  integrationUuid: string
  repository: string
  branch: string
}

export interface ADRepositoryAssignmentRequest {
  subjectType: SubjectType
  subjectUuid: string
  sourceRepository: RepositorySpec
  exploitRepository: RepositorySpec
}

export interface ADRepositoryAssignment {
  uuid: string
  subjectType: SubjectType
  subjectUuid: string
  sourceRepository: RepositorySpec
  exploitRepository: RepositorySpec
}

export interface GitRepositoryInfo {
  repositoryUrl: string
  webUrl: string
  branch: string
  lastCommit?: { sha: string; createdAt: string } | null
}

export interface ADRepositoriesResponse {
  source: GitRepositoryInfo
  exploit: GitRepositoryInfo
}

export interface PluginHealth {
  status: 'unknown' | 'healthy' | 'unhealthy'
  message?: string
  checkedAt: string
}

export interface PluginPermission {
  id: string
  scope: 'global' | 'board' | 'module'
}

export interface PluginRoute {
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  path: string
  permission: string
  scope: 'global' | 'board' | 'module'
  ui?: boolean
}

export interface Plugin {
  id: string
  name: string
  version: string
  apiVersion: string
  status: 'discovered' | 'installed' | 'enabled' | 'disabled' | 'incompatible' | 'unhealthy' | 'failed'
  health: PluginHealth
  author: string
  capabilities: string[]
  permissions: PluginPermission[]
  routes: PluginRoute[]
  frontendMode?: string
  dependencies?: PluginDependency[]
  trustedJSApproved?: boolean
  installedAt?: string | null
  updatedAt?: string | null
}

export interface PluginDependency {
  id: string
  version: string
}

export interface PluginValidateRequest {
  packagePath: string
}

export interface PluginValidation {
  valid: boolean
  findings: ValidationFinding[]
  id?: string
  name?: string
  version?: string
  apiVersion?: string
  backendMode?: string
  frontendMode?: string
  dependencies?: PluginDependency[]
  capabilities?: string[]
  permissions?: PluginPermission[]
}

export interface PluginInstallRequest {
  packagePath: string
  approveCapabilities: string[]
  approvePermissions: string[]
  approveTrustedJS?: boolean
  enable?: boolean
}

export interface PluginUpgradeRequest {
  packagePath: string
  approveCapabilities?: string[]
  approvePermissions?: string[]
  approveTrustedJS?: boolean
}

export interface PluginLogsResponse {
  lines: string[]
}

export interface PluginEventDelivery {
  eventId: string
  status: 'pending' | 'delivered' | 'failed' | 'dead'
  attempts?: number
  lastError?: string | null
  nextAttemptAt?: string | null
  deliveredAt?: string | null
}

export interface PluginVisibleItem {
  path: string
  label: string
  iframeUrl: string
}

export interface PluginTrustedJS {
  entry: string
  url: string
  integrity: string
}

export interface PluginVisible {
  id: string
  name: string
  menu: PluginVisibleItem[]
  pages: PluginVisibleItem[]
  trustedJS?: PluginTrustedJS | null
}

export interface PermissionGrant {
  uuid: string
  userUuid: string
  permission: string
  boardUuid?: string | null
  moduleUuid?: string | null
  grantedByUuid: string
  createdAt: string
  expiresAt?: string | null
  revokedAt?: string | null
}

export interface PermissionGrantCreateRequest {
  userUuid: string
  permission: string
  boardUuid?: string | null
  moduleUuid?: string | null
  expiresAt?: string | null
}

export type AdminUsersResponse = Page<AdminUser>
export type AccessGroupsResponse = Page<AccessGroup>
export type IntegrationsResponse = Page<IntegrationSummary>
export type AdminInstancesResponse = Page<Instance>
export type AdminAudiencesResponse = Page<Audience>
export type AudienceMembersResponse = Page<AudienceMember>
export type AudienceListResponse = { items: AudienceSummary[] }
export type DeploymentTemplateListResponse = Page<DeploymentTemplate>
export type DeploymentTemplateVersionListResponse = Page<DeploymentTemplateVersion>
export type DeploymentTemplateAllowlistListResponse = { items: DeploymentTemplateAllowlist[] }
export type ADRunListResponse = Page<ADRunSummary>
export type ADRepositoriesResponseEnvelope = ADRepositoriesResponse
export type ADProgressEnvelope = ADProgress
export type PluginListResponse = Page<Plugin>
export type PluginEventDeliveryListResponse = Page<PluginEventDelivery>
export type PluginVisibleListResponse = { items: PluginVisible[] }
export type PermissionGrantListResponse = Page<PermissionGrant>

export interface RunLogsResponse {
  lines: string[]
  truncated?: boolean
}

export interface PentestConfig {
  proxmoxIntegrationUuid?: string | null
  providerIntegrationUuid?: string | null
  deploymentTemplateUuid?: string | null
  templateVersion?: number | null
  parameters?: Record<string, string>
  secretParameters?: Record<string, string>
  resetEnabled: boolean
  maxResets: number
  nodes?: InstanceNodeConfig[]
  submission: SubmissionPolicy
}

export type ADRepositoryAssignmentsResponse = Page<ADRepositoryAssignment>
export type ADVulnerabilitiesResponse = Page<ADVulnerabilityAdmin>

export interface ADRunAdminActionRequest {
  action: 'CANCEL' | 'RETRY'
}

export interface ADVulnerabilityPatchRequest {
  key?: string
  title?: string
  description?: string
  exploitPoints?: number
  patchPoints?: number
  exploitTestSelector?: string
  patchTestSelector?: string
}

export interface SitePublic {
  title: string
  introMarkdown: string
  published: boolean
  updatedAt: string
}

export interface AdminSite extends SitePublic {
  updatedByUserUuid?: string | null
}

export interface AdminSitePatchRequest {
  title?: string
  introMarkdown?: string
  published?: boolean
}

export interface UserDirectoryEntry {
  uuid: string
  username: string
  /** Present only when the caller's server-side permission allows disclosure. */
  email?: string
}

export interface TeamDirectoryEntry {
  uuid: string
  name: string
}

export interface TeamDirectoryResponse {
  items: TeamDirectoryEntry[]
  limit: number
  offset: number
  total: number
}

export interface CaptainRosterEntry {
  uuid: string
  username: string
}

export interface CaptainRosterResponse {
  items: CaptainRosterEntry[]
  limit: number
  offset: number
  total: number
}

export interface TeamMember {
  userUuid: string
  username: string
  joinedAt: string
}

export interface UserDirectoryResponse {
  items: UserDirectoryEntry[]
  limit: number
  offset: number
  total: number
}
