<template>
  <el-dialog
    :model-value="modelValue"
    @update:model-value="$emit('update:modelValue', $event)"
    width="80%"
    top="5vh"
    class="preview-dialog"
    :show-close="true"
  >
    <div class="preview-wrap" v-if="current">
      <el-image :src="fullUri" fit="contain" class="preview-img" v-loading="loadingImg">
        <template #error><div class="img-error">failed to load</div></template>
      </el-image>
      <div class="preview-meta">
        <div class="path">{{ current.path }}</div>
        <div class="score">score {{ current.score.toFixed(4) }} • {{ index + 1 }} / {{ hits.length }}</div>
      </div>
      <div class="nav">
        <button class="nav-btn" @click="step(-1)" aria-label="Previous"><IconArrowLeft /></button>
        <button class="nav-btn" @click="step(1)" aria-label="Next"><IconArrowRight /></button>
      </div>
    </div>
  </el-dialog>
</template>

<script setup>
import { ref, watch, computed } from 'vue'
import { ArrowLeft as IconArrowLeft, ArrowRight as IconArrowRight } from '@lucide/vue'
import { call } from '../api.js'

const props = defineProps({
  modelValue: Boolean,
  hits: { type: Array, default: () => [] },
  index: { type: Number, default: 0 },
})
const emit = defineEmits(['update:modelValue', 'update:index'])

const fullUri = ref('')
const loadingImg = ref(false)

const current = computed(() => props.hits[props.index] || null)

watch(
  () => [props.modelValue, props.index],
  async () => {
    if (!props.modelValue || !current.value) return
    loadingImg.value = true
    fullUri.value = ''
    try {
      fullUri.value = await call('ImageDataURI', current.value.path)
    } catch (e) {
      fullUri.value = ''
    } finally {
      loadingImg.value = false
    }
  },
  { immediate: true }
)

function step(dir) {
  const n = props.hits.length
  if (!n) return
  emit('update:index', (props.index + dir + n) % n)
}
</script>

<style scoped>
.preview-wrap { display: flex; flex-direction: column; align-items: center; gap: 12px; }
.preview-img { max-height: 70vh; max-width: 100%; border-radius: 12px; }
.preview-meta { text-align: center; color: var(--fg-3); font-size: 0.82rem; word-break: break-all; }
.nav { display: flex; gap: 12px; }
.nav-btn {
  width: 40px; height: 40px;
  display: grid; place-items: center;
  border-radius: 50%;
  border: 1px solid var(--hairline-strong);
  background: var(--field-bg);
  color: var(--fg);
  cursor: pointer;
  transition: all 0.15s var(--ease);
}
.nav-btn:hover { background: var(--field-bg-focus); transform: scale(1.06); }
.nav-btn:active { transform: scale(0.94); }
.nav-btn svg { width: 18px; height: 18px; }
.img-error { color: var(--fg-3); padding: 20px; }
</style>
