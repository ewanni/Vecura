<template>
  <div class="app-backdrop"></div>

  <div class="app-shell">
    <!-- Title bar with native-style window controls (Win64) -->
    <header class="titlebar">
      <div class="titlebar-title">
        <span class="app-glyph"><svg viewBox="0 0 24 24"><path d="M4 7l8-4 8 4-8 4-8-4zM4 12l8 4 8-4M4 17l8 4 8-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/></svg></span>
        Vecura
      </div>
      <div class="traffic">
        <button class="light-dot light-red" title="Close" @click="winClose"><X /></button>
        <button class="light-dot light-yellow" title="Minimize" @click="winMin"><Minus /></button>
        <button class="light-dot light-green" title="Maximize" @click="winMax"><Square /></button>
      </div>
    </header>

    <div class="app-body">
      <!-- Floating sidebar card -->
      <aside class="sidebar">
        <nav class="sidebar-nav">
          <div class="nav-item" :class="{ active: activeRoute === '/settings' }" @click="onMenu('/settings')">
            <el-icon><SettingsIcon /></el-icon><span>Settings</span>
          </div>
          <div class="nav-item" :class="{ active: activeRoute === '/help' }" @click="onMenu('/help')">
            <el-icon><CircleHelp /></el-icon><span>Help</span>
          </div>
          <div class="nav-item" :class="{ active: logsOpen }" @click="logsOpen = true">
            <el-icon><Document /></el-icon><span>Logs</span>
          </div>
        </nav>

        <div class="sidebar-scroll">
          <router-view
            :models="models"
            :active-key="activeModel"
            :folder-path="configFolder"
            @models-changed="loadModels"
            @use-model="onUseModel"
            @scan-folder="onScanFolder"
          />
        </div>

        <div class="sidebar-foot">
          <button class="theme-toggle" @click="toggleTheme" :title="theme === 'dark' ? 'Switch to light' : 'Switch to dark'" :aria-label="theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'">
            <Transition name="theme-spin" mode="out-in">
              <Sun v-if="theme === 'dark'" :key="'sun'" />
              <Moon v-else :key="'moon'" />
            </Transition>
          </button>
        </div>
      </aside>

      <!-- Main content card -->
      <section class="main">
        <div class="main-search">
          <SearchBar :recent="recent" :active-model="activeModel" @search="onSearch" />
        </div>
        <div class="main-scroll">
          <ImageGrid :hits="hits" :loading="searching" @open="openPreview" />
        </div>
      </section>
    </div>

    <PreviewModal
      v-model="previewVisible"
      :hits="hits"
      :index="previewIndex"
      @update:index="previewIndex = $event"
    />

    <el-dialog
      v-model="logsOpen"
      title="Application logs"
      width="760px"
      class="logs-dialog"
      :close-on-click-modal="false"
    >
      <LogsView />
    </el-dialog>

    <div v-if="scanActive" class="scan-chip">
      <el-progress type="circle" :percentage="scanPercent" :width="58" :stroke-width="4" />
      <span class="scan-text">{{ scanText }}</span>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import SearchBar from './components/SearchBar.vue'
import ImageGrid from './components/ImageGrid.vue'
import PreviewModal from './components/PreviewModal.vue'
import LogsView from './views/LogsView.vue'
import { call, eventsOn } from './api.js'
import { pushLog } from './logger.js'
import { Settings as SettingsIcon, CircleHelp, X, Minus, Square, Moon, Sun } from '@lucide/vue'
import { WindowMinimise, WindowToggleMaximise, Quit } from '../wailsjs/runtime/runtime.js'

const route = useRoute()
const router = useRouter()

const activeRoute = computed(() => route.path)
const theme = ref('dark')
const models = ref([])
const recent = ref([])
const configFolder = ref('')
const hits = ref([])
const searching = ref(false)
const activeModel = ref('')
const previewVisible = ref(false)
const previewIndex = ref(0)

const scanActive = ref(false)
const scanPercent = ref(0)
const scanText = ref('')

const logsOpen = ref(false)

function applyTheme() {
  document.documentElement.setAttribute('data-theme', theme.value)
}
function toggleTheme() {
  theme.value = theme.value === 'dark' ? 'light' : 'dark'
  applyTheme()
}
function onMenu(index) {
  router.push(index)
}

// Win64 window controls (frameless app has no OS chrome)
function winClose() { Quit() }
function winMin() { WindowMinimise() }
function winMax() { WindowToggleMaximise() }

async function loadModels() {
  try {
    models.value = await call('ListModels')
  } catch (e) {
    console.error(e)
  }
}
async function loadRecent() {
  try {
    recent.value = await call('RecentSearches')
  } catch (e) {
    console.error(e)
  }
}

function onUseModel(key) {
  activeModel.value = key
  call('SetActiveModel', key).catch(() => {})
}

async function onScanFolder(path) {
  if (!activeModel.value) {
    ElMessage.warning('Select a model before scanning')
    return
  }
  scanActive.value = true
  scanPercent.value = 0
  pushLog('info', ['Scan started', path, 'model=' + activeModel.value])
  try {
    await call('ScanFolder', path, activeModel.value)
    pushLog('info', ['Scan finished', path])
  } catch (e) {
    pushLog('error', ['Scan failed', e])
    ElMessage.error('Scan failed: ' + e)
  }
}

async function onSearch(query) {
  // Keyword search works without an embedding model; a registered model only
  // adds semantic results on top. So we no longer require one up front.
  let provider = ''
  let modelId = ''
  if (activeModel.value && activeModel.value.includes('/')) {
    ;[provider, modelId] = activeModel.value.split('/')
  }
  searching.value = true
  try {
    const res = await call('Search', query, provider, modelId, 1000, '')
    hits.value = res || []
    await loadRecent()
  } catch (e) {
    ElMessage.error('Search failed: ' + e)
  } finally {
    searching.value = false
  }
}

function openPreview(idx) {
  previewIndex.value = idx
  previewVisible.value = true
}

onMounted(async () => {
  applyTheme()
  await loadModels()
  try {
    const cfg = await call('GetConfig')
    if (cfg) {
      if (cfg.activeModel) activeModel.value = cfg.activeModel
      if (cfg.folderPath) configFolder.value = cfg.folderPath
      // Auto-reconnect on relaunch: validate the last provider's connection
      // (make the request immediately) and re-establish the last selected
      // model so search works without manual intervention.
      const pc = cfg.providers && cfg.providers[cfg.provider]
      if (pc && pc.baseUrl) {
        autoReconnect(pc.baseUrl, pc.apiKey, cfg.activeModel)
      }
    }
  } catch (e) {
    console.error(e)
  }
  await loadRecent()
  eventsOn('provider:reconnected', (info) => {
    if (info && info.activeModel) {
      activeModel.value = info.activeModel
      call('SetActiveModel', info.activeModel).catch(() => {})
    }
  })
  eventsOn('scan:progress', (p) => {
    if (p.Total > 0) scanPercent.value = Math.round((p.Done / p.Total) * 100)
    scanText.value = `Scanning ${p.Done}/${p.Total}`
    scanActive.value = !p.Finished
    if (p.Finished) pushLog('info', ['Scan progress: finished', p.Done + '/' + p.Total])
    else if (p.Total > 0 && p.Done === p.Total) pushLog('info', ['Scan progress', p.Done + '/' + p.Total])
  })
})

// Validates the saved provider connection and refreshes the model list, then
// connects to the last active model if it is still registered.
async function autoReconnect(baseUrl, apiKey, lastModel) {
  try {
    await call('CheckProvider', baseUrl, apiKey)
    pushLog('info', ['Auto-reconnected provider', baseUrl])
    await loadModels()
    if (lastModel && models.value.some((m) => m.key === lastModel)) {
      activeModel.value = lastModel
      call('SetActiveModel', lastModel).catch(() => {})
    } else if (lastModel) {
      pushLog('warn', ['Last model not available after reconnect', lastModel])
    }
  } catch (e) {
    pushLog('error', ['Auto-reconnect failed', e])
  }
}
</script>
