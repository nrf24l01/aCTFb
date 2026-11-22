<script setup lang="ts">
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import EmptyState from '@/components/layout/EmptyState.vue'
import { useSession } from '@/features/auth/session'
import { computed } from 'vue'

const { state } = useSession()
const isAdmin = computed(() => state.user?.roles.includes('admin') ?? false)

const links = [
  { to: '/admin/site', title: 'Site', text: 'Edit the public landing title and Markdown introduction with write/preview.' },
  { to: '/admin/boards', title: 'Boards', text: 'Create boards, modules, tasks, teams, audiences and review manual submissions.' },
  { to: '/admin/users', title: 'Users', text: 'Global roles, disabled/verified state and access-group membership.' },
  { to: '/admin/access-groups', title: 'Access groups', text: 'User sets that gate PRIVATE board joining.' },
  { to: '/admin/integrations', title: 'Integrations', text: 'Git/VM/container providers; credentials are write-only and never displayed.' },
  { to: '/admin/templates', title: 'Deployment templates', text: 'YAML manifest and bounded package files, validation, preview, immutable versions and allowlists.' },
  { to: '/admin/instances', title: 'Instances', text: 'Read-only runtime inventory plus backend-allowed reset/destroy/extend actions.' },
  { to: '/admin/plugins', title: 'Plugins', text: 'Validate/install server-side packages, explicit capabilities and grants, enable/disable/upgrade/remove.' },
]
</script>

<template>
  <PageContainer>
    <PageHeader title="Administration" description="Global-admin console. Scoped board work stays in the board's own Manage view." />
    <EmptyState v-if="!isAdmin" title="Administrator access required" description="Only a global admin can open this console." />
    <div v-else class="grid gap-3 sm:grid-cols-2">
      <RouterLink v-for="link in links" :key="link.to" :to="link.to" class="border border-border bg-black p-4 hover:border-muted-foreground">
        <h2 class="font-semibold">{{ link.title }}</h2>
        <p class="mt-1 text-sm text-muted-foreground">{{ link.text }}</p>
      </RouterLink>
    </div>
  </PageContainer>
</template>