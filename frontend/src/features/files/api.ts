import { apiDelete, apiPost, apiRequest } from '@/shared/api/client'
import type {
  DownloadUrlResponse,
  PendingFile,
  ScopedFilePresignRequest,
  ScopedFilePresignResponse,
  SubmissionDraft,
} from '@/shared/types/api'

function taskDraftPath(boardUuid: string, moduleUuid: string, taskUuid: string): string {
  return `/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}/submission-drafts`
}

function pentestDraftPath(boardUuid: string, moduleUuid: string): string {
  return `/boards/${boardUuid}/modules/${moduleUuid}/pentest/submission-drafts`
}

export const filesApi = {
  createTaskDraft(boardUuid: string, moduleUuid: string, taskUuid: string): Promise<SubmissionDraft> {
    return apiPost<SubmissionDraft>(taskDraftPath(boardUuid, moduleUuid, taskUuid))
  },

  getTaskDraft(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    draftUuid: string,
  ): Promise<SubmissionDraft> {
    return apiRequest<SubmissionDraft>(`${taskDraftPath(boardUuid, moduleUuid, taskUuid)}/${draftUuid}`)
  },

  discardTaskDraft(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    draftUuid: string,
  ): Promise<void> {
    return apiDelete(`${taskDraftPath(boardUuid, moduleUuid, taskUuid)}/${draftUuid}`)
  },

  presignTaskFile(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    draftUuid: string,
    body: ScopedFilePresignRequest,
  ): Promise<ScopedFilePresignResponse> {
    return apiPost<ScopedFilePresignResponse>(
      `${taskDraftPath(boardUuid, moduleUuid, taskUuid)}/${draftUuid}/files/presign`,
      body,
    )
  },

  completeTaskFile(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    draftUuid: string,
    fileUuid: string,
  ): Promise<PendingFile> {
    return apiPost<PendingFile>(
      `${taskDraftPath(boardUuid, moduleUuid, taskUuid)}/${draftUuid}/files/${fileUuid}/complete`,
    )
  },

  createPentestDraft(boardUuid: string, moduleUuid: string): Promise<SubmissionDraft> {
    return apiPost<SubmissionDraft>(pentestDraftPath(boardUuid, moduleUuid))
  },

  getPentestDraft(boardUuid: string, moduleUuid: string, draftUuid: string): Promise<SubmissionDraft> {
    return apiRequest<SubmissionDraft>(`${pentestDraftPath(boardUuid, moduleUuid)}/${draftUuid}`)
  },

  discardPentestDraft(boardUuid: string, moduleUuid: string, draftUuid: string): Promise<void> {
    return apiDelete(`${pentestDraftPath(boardUuid, moduleUuid)}/${draftUuid}`)
  },

  presignPentestFile(
    boardUuid: string,
    moduleUuid: string,
    draftUuid: string,
    body: ScopedFilePresignRequest,
  ): Promise<ScopedFilePresignResponse> {
    return apiPost<ScopedFilePresignResponse>(
      `${pentestDraftPath(boardUuid, moduleUuid)}/${draftUuid}/files/presign`,
      body,
    )
  },

  completePentestFile(
    boardUuid: string,
    moduleUuid: string,
    draftUuid: string,
    fileUuid: string,
  ): Promise<PendingFile> {
    return apiPost<PendingFile>(
      `${pentestDraftPath(boardUuid, moduleUuid)}/${draftUuid}/files/${fileUuid}/complete`,
    )
  },

  downloadTaskAttachment(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    fileUuid: string,
  ): Promise<DownloadUrlResponse> {
    return apiPost<DownloadUrlResponse>(
      `/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}/attachments/${fileUuid}/download-url`,
    )
  },

  downloadOwnTaskSubmissionFile(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    submissionUuid: string,
    fileUuid: string,
  ): Promise<DownloadUrlResponse> {
    return apiPost<DownloadUrlResponse>(
      `/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}/submissions/${submissionUuid}/files/${fileUuid}/download-url`,
    )
  },

  adminPresignTaskAttachment(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    body: ScopedFilePresignRequest,
  ): Promise<ScopedFilePresignResponse> {
    return apiPost<ScopedFilePresignResponse>(
      `/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}/attachments/presign`,
      body,
    )
  },

  adminCompleteTaskAttachment(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    fileUuid: string,
  ): Promise<PendingFile> {
    return apiPost<PendingFile>(
      `/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}/attachments/${fileUuid}/complete`,
    )
  },

  adminDownloadTaskAttachment(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    fileUuid: string,
  ): Promise<DownloadUrlResponse> {
    return apiPost<DownloadUrlResponse>(
      `/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}/attachments/${fileUuid}/download-url`,
    )
  },

  adminDeleteTaskAttachment(
    boardUuid: string,
    moduleUuid: string,
    taskUuid: string,
    fileUuid: string,
  ): Promise<void> {
    return apiDelete(
      `/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}/attachments/${fileUuid}`,
    )
  },

  downloadPentestAttachment(
    boardUuid: string,
    moduleUuid: string,
    fileUuid: string,
  ): Promise<DownloadUrlResponse> {
    return apiPost<DownloadUrlResponse>(
      `/boards/${boardUuid}/modules/${moduleUuid}/pentest/attachments/${fileUuid}/download-url`,
    )
  },

  downloadOwnPentestSubmissionFile(
    boardUuid: string,
    moduleUuid: string,
    submissionUuid: string,
    fileUuid: string,
  ): Promise<DownloadUrlResponse> {
    return apiPost<DownloadUrlResponse>(
      `/boards/${boardUuid}/modules/${moduleUuid}/pentest/submissions/${submissionUuid}/files/${fileUuid}/download-url`,
    )
  },

  adminDownloadSubmissionFile(
    boardUuid: string,
    submissionUuid: string,
    fileUuid: string,
  ): Promise<DownloadUrlResponse> {
    return apiPost<DownloadUrlResponse>(
      `/admin/boards/${boardUuid}/submissions/${submissionUuid}/files/${fileUuid}/download-url`,
    )
  },
}