<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import UserLink from '../UserLink.vue'
import { useAuth } from '../../stores/auth'
import type { OrderSeller } from '../../types'

// Who sold an order: the reseller (linking to their reseller page for those who manage resellers, else to the
// user page) and how ("bought for a customer" or "brought the buyer"); the store's own sale says so.
defineProps<{ seller: OrderSeller | null | undefined; inline?: boolean }>()
const { t } = useI18n()
const auth = useAuth()
</script>

<template>
  <span v-if="!seller || !seller.user" class="app-muted seller-store">{{ t('order.seller_store') }}</span>
  <span v-else class="seller" :class="{ inline }">
    <RouterLink v-if="auth.can('resellers.write')" :to="{ name: 'reseller', params: { id: seller.user.id } }" class="seller-link" @click.stop>
      <UserLink :user="seller.user" :link="false" />
    </RouterLink>
    <UserLink v-else :user="seller.user" :link="auth.can('users.read')" />
    <span class="app-muted kind">{{ t('order.seller_kind.' + seller.kind) }}</span>
  </span>
</template>

<style scoped>
.seller {
  display: inline-flex;
  flex-direction: column;
  gap: 0.1rem;
}
.seller.inline {
  flex-direction: row;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.25rem 0.5rem;
}
.seller-link {
  color: inherit;
  text-decoration: none;
}
.seller-link:hover :deep(bdi) {
  text-decoration: underline;
}
.kind {
  font-size: 0.8rem;
}
</style>
