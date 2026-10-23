<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Textarea from 'primevue/textarea'
import ToggleSwitch from 'primevue/toggleswitch'
import { costText, parsePrice, type SupplierProduct } from '../../supplier'

// One supplier product: what the supplier says, and what the owner writes (price in Toman, names and descriptions per language).
const props = defineProps<{ visible: boolean; product: SupplierProduct | null; saving: boolean }>()
const emit = defineEmits<{ 'update:visible': [boolean]; save: [Record<string, unknown>] }>()
const { t } = useI18n()

const price = ref('')
const enabled = ref(false)
const sort = ref('0')
const titleFa = ref('')
const titleEn = ref('')
const descFa = ref('')
const descEn = ref('')
watch(
  () => [props.visible, props.product] as const,
  () => {
    const p = props.product
    if (!p) return
    price.value = p.sell_price === null ? '' : String(p.sell_price)
    enabled.value = p.enabled
    sort.value = String(p.sort)
    titleFa.value = p.title.fa ?? ''
    titleEn.value = p.title.en ?? ''
    descFa.value = p.desc.fa ?? ''
    descEn.value = p.desc.en ?? ''
  },
  { immediate: true },
)
// The supplier's own name and text as a start for the owner's: copied into the English fields, to be edited (and translated).
function copySupplierText() {
  const p = props.product
  if (!p) return
  if (p.name) titleEn.value = p.name
  if (p.description) descEn.value = p.description.slice(0, 3000)
}
const parsed = computed(() => (price.value.trim() === '' ? 0 : parsePrice(price.value)))
const problem = computed(() => {
  if (parsed.value === null) return 'price'
  if (enabled.value && parsed.value === 0) return 'need_price'
  if (!/^-?\d{1,6}$/.test(sort.value.trim())) return 'sort'
  return ''
})
function save() {
  if (problem.value || !props.product) return
  emit('save', {
    enabled: enabled.value,
    sell_price: parsed.value ?? 0,
    sort: Number(sort.value),
    title: { fa: titleFa.value, en: titleEn.value },
    desc: { fa: descFa.value, en: descEn.value },
  })
}
</script>

<template>
  <Dialog :visible="visible" modal :header="product?.name || product?.id" :style="{ width: '34rem', maxWidth: '96vw' }" @update:visible="(v: boolean) => emit('update:visible', v)">
    <form v-if="product" class="form" @submit.prevent="save">
      <p class="app-muted small app-ltr app-mono">{{ product.id }}</p>
      <p v-if="product.cost_micro !== null || product.stock !== null" class="app-muted small">
        <span v-if="product.cost_micro !== null">{{ t('supplier.cost') }}: <bdi class="app-ltr">{{ costText(product.cost_micro, product.cost_currency) }}</bdi></span>
        <span v-if="product.stock !== null"> · {{ t('supplier.stock') }}: {{ product.stock }}</span>
      </p>
      <p v-if="product.category" class="app-muted small">{{ t('supplier.category') }}: <bdi>{{ product.category }}</bdi></p>
      <div v-if="product.description" class="supplier-text">
        <span class="app-muted small">{{ t('supplier.supplier_text') }}</span>
        <div class="supplier-body" dir="auto" tabindex="0">{{ product.description }}</div>
      </div>
      <Button v-if="product.description || product.name" type="button" outlined size="small" icon="pi pi-copy" :label="t('supplier.copy_text')" @click="copySupplierText" />
      <label class="field">
        <span>{{ t('supplier.price') }}</span>
        <InputText v-model="price" class="app-ltr" inputmode="numeric" :invalid="problem === 'price' || problem === 'need_price'" />
        <small class="app-muted">{{ t('supplier.price_help') }}</small>
      </label>
      <label class="switch">
        <ToggleSwitch v-model="enabled" :aria-label="t('supplier.sell')" />
        <span>{{ t('supplier.sell') }}</span>
      </label>
      <small v-if="problem === 'need_price'" class="app-muted">{{ t('supplier.need_price') }}</small>
      <label class="field"><span>{{ t('supplier.title_fa') }}</span><InputText v-model="titleFa" maxlength="200" /></label>
      <label class="field"><span>{{ t('supplier.title_en') }}</span><InputText v-model="titleEn" class="app-ltr" maxlength="200" /></label>
      <label class="field"><span>{{ t('supplier.desc_fa') }}</span><Textarea v-model="descFa" rows="8" maxlength="3000" /></label>
      <label class="field"><span>{{ t('supplier.desc_en') }}</span><Textarea v-model="descEn" rows="8" class="app-ltr" maxlength="3000" />
        <small class="app-muted">{{ t('supplier.desc_help') }}</small>
      </label>
      <label class="field">
        <span>{{ t('supplier.sort') }}</span>
        <InputText v-model="sort" class="app-ltr" inputmode="numeric" :invalid="problem === 'sort'" />
      </label>
    </form>
    <template #footer>
      <Button text :label="t('app.cancel')" @click="emit('update:visible', false)" />
      <Button :label="t('licenses.save')" :loading="saving" :disabled="problem !== ''" @click="save" />
    </template>
  </Dialog>
</template>

<style scoped>
/* One column that may shrink: a long word or link in the supplier's text must not stretch the form past the dialog. */
.form {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 0.75rem;
}
.field {
  display: grid;
  gap: 0.25rem;
  min-width: 0;
}
.field :deep(input),
.field :deep(textarea) {
  width: 100%;
  min-width: 0;
  box-sizing: border-box;
}
.supplier-text {
  display: grid;
  gap: 0.25rem;
  min-width: 0;
}
/* The supplier's text as it was written: its own line breaks, in its own direction, in a box that scrolls. */
.supplier-body {
  max-height: 9rem;
  overflow: auto;
  padding: 0.5rem 0.75rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.5rem;
  background: var(--p-content-hover-background, transparent);
  font-size: 0.85rem;
  line-height: 1.6;
  text-align: start;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.switch {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.small {
  font-size: 0.85rem;
  margin: 0;
}
</style>
