<script setup lang="ts">
import Toast from 'primevue/toast'
import ConfirmDialog from 'primevue/confirmdialog'
import { useUi } from './stores/ui'

useUi() // applies the saved theme on start
</script>

<template>
  <Toast position="top-center" />
  <ConfirmDialog />
  <RouterView />
</template>
