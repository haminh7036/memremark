<script setup>
import { useDark, useToggle } from '@vueuse/core'
import {
  Sparkles,
  Sun,
  Moon,
  RefreshCw,
  Database,
  Activity,
  Layers,
  Info,
  Lightbulb,
  Heart,
  Compass
} from 'lucide-vue-next'

const isDark = useDark({
  selector: 'html',
  attribute: 'class',
  valueDark: 'dark',
  valueLight: ''
})
const toggleDark = useToggle(isDark)

const props = defineProps({
  stats: {
    type: Object,
    default: () => ({
      total_wings: 0,
      total_summaries: 0,
      total_verbatim: 0,
      halls: {
        fact: 0,
        discovery: 0,
        preference: 0,
        advice: 0
      }
    })
  },
  isLoading: {
    type: Boolean,
    default: false
  },
  autoPollInterval: {
    type: Number,
    default: 0
  },
  lastUpdated: {
    type: Date,
    default: null
  }
})

const emit = defineEmits(['refresh', 'update:autoPollInterval'])

const pollOptions = [
  { label: 'Off', value: 0 },
  { label: '5s', value: 5000 },
  { label: '10s', value: 10000 },
  { label: '30s', value: 30000 }
]

function formatLastUpdated(date) {
  if (!date) return ''
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}
</script>

<template>
  <header class="border-b border-zinc-200 dark:border-zinc-800 bg-white/80 dark:bg-zinc-950/80 backdrop-blur sticky top-0 z-30 transition-colors duration-200">
    <div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
      <div class="flex items-center justify-between h-16">
        <!-- Brand Logo & Status -->
        <div class="flex items-center gap-4">
          <div class="flex items-center gap-2.5">
            <div class="w-9 h-9 rounded-xl bg-indigo-600 text-white flex items-center justify-center shadow-sm shadow-indigo-500/20">
              <Sparkles class="w-5 h-5" />
            </div>
            <div>
              <div class="flex items-center gap-2">
                <h1 class="text-base font-bold text-zinc-900 dark:text-zinc-50 tracking-tight">MemRemark</h1>
                <span class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-semibold bg-indigo-500/10 text-indigo-700 dark:text-indigo-300 border border-indigo-500/20">
                  v1.1
                </span>
              </div>
              <p class="text-xs text-zinc-500 dark:text-zinc-400 font-medium">Memory Dashboard & Timeline</p>
            </div>
          </div>

          <!-- Connection Badge -->
          <div class="hidden sm:flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium bg-emerald-500/10 text-emerald-700 dark:text-emerald-400 border border-emerald-500/20">
            <span class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse"></span>
            <span>Connected to SQLite</span>
          </div>
        </div>

        <!-- Right Side Controls: Stats, Poll, Refresh, Theme -->
        <div class="flex items-center gap-3">
          <!-- Stats Summary Bar (Desktop) -->
          <div class="hidden md:flex items-center gap-2 px-3 py-1.5 rounded-lg bg-zinc-100 dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 text-xs">
            <div class="flex items-center gap-1.5 text-zinc-600 dark:text-zinc-300" title="Total Distilled Summaries">
              <Layers class="w-3.5 h-3.5 text-indigo-500" />
              <span class="font-semibold text-zinc-900 dark:text-zinc-100">{{ stats?.total_summaries ?? 0 }}</span>
              <span class="text-zinc-400">summaries</span>
            </div>
            <span class="text-zinc-300 dark:text-zinc-700">•</span>
            <div class="flex items-center gap-1.5 text-zinc-600 dark:text-zinc-300" title="Total Raw Observations">
              <Activity class="w-3.5 h-3.5 text-emerald-500" />
              <span class="font-semibold text-zinc-900 dark:text-zinc-100">{{ stats?.total_verbatim ?? 0 }}</span>
              <span class="text-zinc-400">verbatim</span>
            </div>
            <span class="text-zinc-300 dark:text-zinc-700">•</span>
            <div class="flex items-center gap-1.5 text-zinc-600 dark:text-zinc-300" title="Tracked Workspaces">
              <Database class="w-3.5 h-3.5 text-amber-500" />
              <span class="font-semibold text-zinc-900 dark:text-zinc-100">{{ stats?.total_wings ?? 0 }}</span>
              <span class="text-zinc-400">workspaces</span>
            </div>
          </div>

          <!-- Auto Refresh Dropdown -->
          <div class="flex items-center bg-zinc-100 dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-lg p-0.5 text-xs">
            <span class="px-2 text-zinc-500 dark:text-zinc-400 font-medium hidden sm:inline">Poll:</span>
            <button
              v-for="opt in pollOptions"
              :key="opt.value"
              @click="emit('update:autoPollInterval', opt.value)"
              :class="[
                'px-2 py-1 rounded font-medium transition-all',
                autoPollInterval === opt.value
                  ? 'bg-white dark:bg-zinc-800 text-zinc-900 dark:text-zinc-100 shadow-xs'
                  : 'text-zinc-500 dark:text-zinc-400 hover:text-zinc-800 dark:hover:text-zinc-200'
              ]"
            >
              {{ opt.label }}
            </button>
          </div>

          <!-- Manual Refresh Button -->
          <button
            @click="emit('refresh')"
            :disabled="isLoading"
            title="Refresh memory timeline"
            class="p-2 rounded-lg bg-zinc-100 dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 text-zinc-600 dark:text-zinc-300 hover:bg-zinc-200 dark:hover:bg-zinc-800 transition-colors focus:outline-none focus:ring-2 focus:ring-indigo-500/20"
          >
            <RefreshCw :class="['w-4 h-4', isLoading ? 'animate-spin text-indigo-500' : '']" />
          </button>

          <!-- Theme Toggle Button -->
          <button
            @click="toggleDark()"
            title="Toggle theme"
            class="p-2 rounded-lg bg-zinc-100 dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 text-zinc-600 dark:text-zinc-300 hover:bg-zinc-200 dark:hover:bg-zinc-800 transition-colors focus:outline-none focus:ring-2 focus:ring-indigo-500/20"
          >
            <Sun v-if="isDark" class="w-4 h-4 text-amber-400" />
            <Moon v-else class="w-4 h-4 text-zinc-600" />
          </button>
        </div>
      </div>
    </div>
  </header>
</template>
