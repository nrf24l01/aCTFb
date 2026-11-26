<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { LogOutIcon, MenuIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { useSession } from '@/features/auth/session'
import { pluginsApi } from '@/features/plugins/api'
import { isSafeVisiblePluginPath } from '@/features/plugins/bridge'

const route = useRoute()
const router = useRouter()
const { state, signOut } = useSession()
const mobileOpen = ref(false)
const warning = ref<string | null>(null)

const isAdmin = computed(() => state.user?.roles.includes('admin') ?? false)

watch(
  () => state.sessionLost,
  (lost) => {
    if (lost && !route.meta.public) {
      void router.replace({ name: 'sign-in', query: { redirect: route.fullPath } })
    }
  },
)
const nav = computed(() => {
  if (state.status !== 'authenticated') {
    return [
      { label: 'Home', to: '/', matches: ['/'] },
      { label: 'Boards', to: '/boards', matches: ['/boards'] },
      { label: 'Sign in', to: '/sign-in', matches: ['/sign-in', '/register'] },
    ]
  }
  return [
    { label: 'Boards', to: '/boards', matches: ['/boards'] },
    ...(isAdmin.value ? [{ label: 'Admin', to: '/admin', matches: ['/admin'] }] : []),
    { label: 'Profile', to: '/profile', matches: ['/profile'] },
  ]
})

const pluginMenu = ref<Array<{ label: string; to: string }>>([])

watch(
  () => state.status,
  async (status) => {
    if (status !== 'authenticated') {
      pluginMenu.value = []
      return
    }
    try {
      const response = await pluginsApi.visible()
      pluginMenu.value = response.items.flatMap((plugin) =>
        plugin.menu
          .filter((item) => isSafeVisiblePluginPath(plugin.id, item.path))
          .map((item) => ({ label: item.label, to: item.path })),
      )
    } catch {
      pluginMenu.value = []
    }
  },
  { immediate: true },
)

function isActive(entry: { matches: string[] }): boolean {
  if (entry.matches.includes('/')) {
    return route.path === '/'
  }
  return entry.matches.some((prefix) => route.path === prefix || route.path.startsWith(`${prefix}/`))
}

async function handleSignOut() {
  warning.value = await signOut()
  mobileOpen.value = false
  await router.push('/sign-in')
}
</script>

<template>
  <div v-if="route.meta.bare" class="min-h-dvh bg-black">
    <header class="flex h-12 items-center border-b border-border px-4">
      <RouterLink to="/sign-in" class="font-bold tracking-wider">aCTFb</RouterLink>
    </header>
    <main class="mx-auto w-full max-w-md px-4 py-10">
      <Alert v-if="warning" class="mb-6 rounded-none border-gold/60 bg-black">
        <AlertTitle>Sign-out warning</AlertTitle>
        <AlertDescription>{{ warning }}</AlertDescription>
      </Alert>
      <RouterView />
    </main>
  </div>

  <div v-else class="min-h-dvh bg-black">
    <header class="sticky top-0 z-30 flex h-12 items-stretch border-b border-border bg-black max-md:h-14">
      <Sheet v-model:open="mobileOpen">
        <SheetTrigger as-child>
          <Button variant="ghost" class="h-full w-14 shrink-0 rounded-none border-r border-border md:hidden" aria-label="Open navigation">
            <MenuIcon class="size-5" />
          </Button>
        </SheetTrigger>
        <SheetContent side="left" class="w-72 rounded-none border-border bg-black p-0">
          <SheetHeader class="border-b border-border px-4 py-3 text-left">
            <SheetTitle>aCTFb</SheetTitle>
          </SheetHeader>
          <nav class="flex flex-col p-2" aria-label="Main navigation">
            <RouterLink
              v-for="entry in nav"
              :key="entry.to"
              :to="entry.to"
              class="flex min-h-11 items-center border-l-2 px-3 text-sm"
              :class="isActive(entry) ? 'border-primary bg-muted text-foreground' : 'border-transparent text-muted-foreground hover:bg-muted hover:text-foreground'"
              @click="mobileOpen = false"
            >
              {{ entry.label }}
            </RouterLink>
            <div v-if="pluginMenu.length > 0" class="mt-2 border-t border-border pt-2">
              <p class="px-3 py-1 text-[11px] uppercase tracking-[0.08em] text-muted-foreground">Plugins</p>
              <RouterLink
                v-for="entry in pluginMenu"
                :key="entry.to"
                :to="entry.to"
                class="flex min-h-11 items-center border-l-2 border-transparent px-3 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
                @click="mobileOpen = false"
              >
                {{ entry.label }}
              </RouterLink>
            </div>
          </nav>
        </SheetContent>
      </Sheet>

      <RouterLink to="/" class="flex w-[212px] shrink-0 items-center border-r border-border px-4 font-bold tracking-wider max-md:w-auto max-md:border-r-0">
        aCTFb
      </RouterLink>

      <div class="flex min-w-0 flex-1 items-center justify-end gap-2 px-3">
        <span class="hidden truncate text-xs text-muted-foreground sm:block">{{ state.user?.username }}</span>
        <Button v-if="state.status === 'authenticated'" variant="ghost" class="h-10 rounded-none max-md:h-11" @click="handleSignOut">
          <LogOutIcon class="size-4" />
          <span class="hidden sm:inline">Sign out</span>
          <span class="sr-only sm:hidden">Sign out</span>
        </Button>
        <Button v-else as-child variant="outline" class="h-10 rounded-none max-md:h-11">
          <RouterLink to="/sign-in">Sign in</RouterLink>
        </Button>
      </div>
    </header>

    <div class="flex">
      <aside class="sticky top-12 hidden h-[calc(100dvh-3rem)] w-[212px] shrink-0 border-r border-border md:block">
        <nav class="flex flex-col p-2" aria-label="Main navigation">
          <RouterLink
            v-for="entry in nav"
            :key="entry.to"
            :to="entry.to"
            class="flex min-h-10 items-center border-l-2 px-3 text-sm"
            :class="isActive(entry) ? 'border-primary bg-muted text-foreground' : 'border-transparent text-muted-foreground hover:bg-muted hover:text-foreground'"
          >
            {{ entry.label }}
          </RouterLink>
          <div v-if="pluginMenu.length > 0" class="mt-2 border-t border-border pt-2">
            <p class="px-3 py-1 text-[11px] uppercase tracking-[0.08em] text-muted-foreground">Plugins</p>
            <RouterLink
              v-for="entry in pluginMenu"
              :key="entry.to"
              :to="entry.to"
              class="flex min-h-10 items-center border-l-2 border-transparent px-3 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              {{ entry.label }}
            </RouterLink>
          </div>
        </nav>
        <p class="mt-auto px-4 py-4 text-xs text-muted-foreground">Challenge board</p>
      </aside>

      <main class="min-w-0 flex-1">
        <div class="mx-auto w-full max-w-[1600px] px-4 pt-4 sm:px-6">
          <Alert v-if="state.sessionLost" class="rounded-none border-love/60 bg-black">
            <AlertTitle>Session ended</AlertTitle>
            <AlertDescription>Your session expired. Sign in again to continue.</AlertDescription>
          </Alert>
          <Alert v-if="warning" class="mt-2 rounded-none border-gold/60 bg-black">
            <AlertTitle>Sign-out warning</AlertTitle>
            <AlertDescription>{{ warning }}</AlertDescription>
          </Alert>
        </div>
        <RouterView />
      </main>
    </div>
  </div>
</template>