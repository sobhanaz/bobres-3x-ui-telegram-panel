<script setup lang="ts">
import Toast from 'primevue/toast'
import ConfirmDialog from 'primevue/confirmdialog'
import { useUi } from './stores/ui'

useUi() // applies the saved theme on start
</script>

<template>
  <Toast position="top-center" />
  <!-- A long question wraps instead of stretching across the screen. -->
  <ConfirmDialog :style="{ width: '32rem' }" :breakpoints="{ '575px': '92vw' }" />
  <RouterView />
</template>
