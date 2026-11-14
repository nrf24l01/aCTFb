import { describe, expect, it } from 'vitest'
import { canManageTeam, isTeamCaptain } from '@/features/teams/permissions'
import {
  canReviewSubmissions,
  hasBoardPermission,
  hasModulePermission,
  isBoardJoinOffered,
} from '@/features/boards/permissions'
import type { BoardPermissionsResponse, Team } from '@/shared/types/api'

const team: Team = {
  uuid: '11111111-1111-4111-8111-111111111111',
  boardUuid: '22222222-2222-4222-8222-222222222222',
  name: 'Blue',
  captainUuid: '33333333-3333-4333-8333-333333333333',
  membersCount: 2,
  maxMembers: 4,
}

const reviewerPermissions: BoardPermissionsResponse = {
  boardUuid: team.boardUuid,
  roles: ['reviewer'],
  permissions: ['board.read'],
  modules: [
    {
      moduleUuid: '44444444-4444-4444-8444-444444444444',
      roles: ['reviewer'],
      permissions: ['module.submissions.read', 'module.submissions.review'],
    },
  ],
}

describe('team captain authority', () => {
  it('only the returned captainUuid is treated as captain', () => {
    expect(isTeamCaptain(team, team.captainUuid)).toBe(true)
    expect(canManageTeam(team, '55555555-5555-4555-8555-555555555555')).toBe(false)
    expect(canManageTeam(null, team.captainUuid)).toBe(false)
  })
})

describe('board join affordance for visible unjoined boards', () => {
  const openBoard = { status: 'ACTIVE', registrationMode: 'OPEN' }

  it('offers self-join before the participant row exists', () => {
    expect(isBoardJoinOffered(openBoard, { joined: false })).toBe(true)
    expect(isBoardJoinOffered(openBoard, null)).toBe(true)
  })

  it('does not offer join after joining, after the window, or when registration is closed', () => {
    expect(isBoardJoinOffered(openBoard, { joined: true })).toBe(false)
    expect(isBoardJoinOffered({ status: 'FINISHED', registrationMode: 'OPEN' }, { joined: false })).toBe(false)
    expect(isBoardJoinOffered({ status: 'ACTIVE', registrationMode: 'CLOSED' }, { joined: false })).toBe(false)
  })
})

describe('reviewer scoping', () => {
  it('module reviewer can read/review only its module', () => {
    const moduleUuid = '44444444-4444-4444-8444-444444444444'
    expect(hasModulePermission(reviewerPermissions, moduleUuid, 'module.submissions.read')).toBe(true)
    expect(hasModulePermission(reviewerPermissions, moduleUuid, 'module.submissions.review')).toBe(true)
    expect(hasModulePermission(reviewerPermissions, '66666666-6666-4666-8666-666666666666', 'module.submissions.review')).toBe(false)
    expect(hasBoardPermission(reviewerPermissions, 'board.update')).toBe(false)
    expect(canReviewSubmissions(reviewerPermissions)).toBe(false)
  })
})