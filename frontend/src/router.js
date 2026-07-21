import { createRouter, createWebHashHistory } from 'vue-router'
import SettingsView from './views/SettingsView.vue'
import HelpView from './views/HelpView.vue'

const routes = [
  { path: '/', redirect: '/settings' },
  { path: '/settings', name: 'settings', component: SettingsView, meta: { title: 'Settings' } },
  { path: '/help', name: 'help', component: HelpView, meta: { title: 'Help' } },
]

export const router = createRouter({
  history: createWebHashHistory(),
  routes,
})
