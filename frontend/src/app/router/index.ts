import { createRouter, createWebHistory } from 'vue-router'
import AppShell from '@/components/layout/AppShell.vue'
import { initSession, useSession } from '@/features/auth/session'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      component: AppShell,
      children: [
        { path: '', name: 'landing', component: () => import('@/features/site/pages/LandingPage.vue'), meta: { public: true } },
        {
          path: 'sign-in',
          name: 'sign-in',
          component: () => import('@/features/auth/pages/SignInPage.vue'),
          meta: { public: true, bare: true },
        },
        {
          path: 'register',
          name: 'register',
          component: () => import('@/features/auth/pages/RegisterPage.vue'),
          meta: { public: true, bare: true },
        },
        {
          path: 'confirm-email',
          name: 'confirm-email',
          component: () => import('@/features/auth/pages/ConfirmEmailPage.vue'),
          meta: { public: true, bare: true },
        },
        {
          path: 'reset-password',
          name: 'reset-password-request',
          component: () => import('@/features/auth/pages/ResetPasswordRequestPage.vue'),
          meta: { public: true, bare: true },
        },
        {
          path: 'reset-password/confirm',
          name: 'reset-password-confirm',
          component: () => import('@/features/auth/pages/ResetPasswordPage.vue'),
          meta: { public: true, bare: true },
        },
        {
          path: 'boards',
          name: 'boards',
          component: () => import('@/features/boards/pages/BoardsPage.vue'),
        },
        {
          path: 'boards/:boardUuid',
          name: 'board',
          component: () => import('@/features/boards/pages/BoardPage.vue'),
        },
        {
          path: 'boards/:boardUuid/scoreboard',
          name: 'scoreboard',
          component: () => import('@/features/boards/pages/ScoreboardPage.vue'),
        },
        {
          path: 'boards/:boardUuid/modules/:moduleUuid',
          name: 'module',
          component: () => import('@/features/boards/pages/ModulePage.vue'),
        },
        {
          path: 'boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid',
          name: 'task',
          component: () => import('@/features/boards/pages/TaskPage.vue'),
        },
        {
          path: 'boards/:boardUuid/modules/:moduleUuid/pentest',
          name: 'pentest',
          component: () => import('@/features/pentest/pages/PentestPage.vue'),
        },
        {
          path: 'boards/:boardUuid/modules/:moduleUuid/attack-defense',
          name: 'attack-defense',
          component: () => import('@/features/attack-defense/pages/ADModulePage.vue'),
        },
        {
          path: 'boards/:boardUuid/staff',
          name: 'board-staff',
          component: () => import('@/features/boards/pages/BoardStaffPage.vue'),
        },
        {
          path: 'profile',
          name: 'profile',
          component: () => import('@/features/auth/pages/ProfilePage.vue'),
        },
        {
          path: 'admin',
          name: 'admin',
          component: () => import('@/features/admin/pages/AdminHubPage.vue'),
        },
        {
          path: 'admin/boards',
          name: 'admin-boards',
          component: () => import('@/features/admin/pages/AdminBoardsPage.vue'),
        },
        {
          path: 'admin/users',
          name: 'admin-users',
          component: () => import('@/features/admin/pages/AdminUsersPage.vue'),
        },
        {
          path: 'admin/access-groups',
          name: 'admin-access-groups',
          component: () => import('@/features/admin/pages/AdminAccessGroupsPage.vue'),
        },
        {
          path: 'admin/site',
          name: 'admin-site',
          component: () => import('@/features/site/pages/AdminSitePage.vue'),
        },
        {
          path: 'admin/integrations',
          name: 'admin-integrations',
          component: () => import('@/features/admin/pages/AdminIntegrationsPage.vue'),
        },
        {
          path: 'admin/templates',
          name: 'admin-templates',
          component: () => import('@/features/admin/pages/AdminTemplatesPage.vue'),
        },
        {
          path: 'admin/templates/:templateUuid',
          name: 'admin-template-editor',
          component: () => import('@/features/admin/pages/AdminTemplateEditorPage.vue'),
        },
        {
          path: 'admin/instances',
          name: 'admin-instances',
          component: () => import('@/features/admin/pages/AdminInstancesPage.vue'),
        },
        {
          path: 'admin/plugins',
          name: 'admin-plugins',
          component: () => import('@/features/admin/pages/AdminPluginsPage.vue'),
        },
        {
          path: 'admin/plugins/:pluginId',
          name: 'admin-plugin-detail',
          component: () => import('@/features/admin/pages/AdminPluginsPage.vue'),
        },
        {
          path: 'plugins/:pluginId/:pagePath(.*)',
          name: 'plugin-page',
          component: () => import('@/features/plugins/pages/PluginPage.vue'),
        },
        {
          path: 'admin/boards/:boardUuid',
          name: 'admin-board',
          component: () => import('@/features/admin/pages/AdminBoardPage.vue'),
        },
        {
          path: 'forbidden',
          name: 'forbidden',
          component: () => import('@/features/auth/pages/ForbiddenPage.vue'),
        },
        {
          path: ':pathMatch(.*)*',
          name: 'not-found',
          component: () => import('@/features/auth/pages/NotFoundPage.vue'),
        },
      ],
    },
  ],
  scrollBehavior: () => ({ top: 0 }),
})

let bootstrapped = false

router.beforeEach(async (to) => {
  const { state } = useSession()

  if (!bootstrapped) {
    bootstrapped = true
    await initSession()
  }

  const isPublic = to.meta.public === true

  if (isPublic) {
    if ((to.name === 'sign-in' || to.name === 'register') && state.status === 'authenticated') {
      return { name: 'boards' }
    }
    return true
  }

  if (state.status !== 'authenticated') {
    return { name: 'sign-in', query: { redirect: to.fullPath } }
  }

  return true
})

export default router