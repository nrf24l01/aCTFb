<script setup lang="ts">
import { Button } from '@/components/ui/button'
import PageContainer from '@/components/layout/PageContainer.vue'
import EmptyState from '@/components/layout/EmptyState.vue'
</script>

<template>
  <PageContainer>
    <EmptyState title="Access denied" description="Your account does not have permission to view this page.">
      <Button as-child variant="outline">
        <RouterLink to="/boards">Back to boards</RouterLink>
      </Button>
    </EmptyState>
  </PageContainer>
</template>