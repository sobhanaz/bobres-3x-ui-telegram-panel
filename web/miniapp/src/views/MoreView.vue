<script setup lang="ts">
import { computed } from 'vue'
import { tr } from '../i18n'
import { sectionsFor, type Section } from '../sections'
import { state } from '../store'

const emit = defineEmits<{ open: [section: Section] }>()
const list = computed(() => sectionsFor(state.me))
</script>

<template>
  <section>
    <h1>{{ tr('more.title') }}</h1>
    <ul class="list">
      <li v-for="s in list" :key="s.id">
        <button class="card row between" type="button" @click="emit('open', s)">
          <span>
            <strong>{{ tr(s.title) }}</strong>
            <span v-if="s.hint" class="muted small block">{{ tr(s.hint) }}</span>
          </span>
          <span class="chev" aria-hidden="true">›</span>
        </button>
      </li>
    </ul>
  </section>
</template>
