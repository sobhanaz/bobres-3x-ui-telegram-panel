<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
</script>

<template>
  <h1 class="app-page-title">{{ t('app.loading') }}</h1>
</template>
