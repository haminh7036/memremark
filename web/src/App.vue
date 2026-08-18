<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { refDebounced, useWindowScroll } from '@vueuse/core'
import { ArrowUp } from 'lucide-vue-next'
import Header from './components/Header.vue'
import ControlBar from './components/ControlBar.vue'
import TimelineView from './components/TimelineView.vue'

// State
const wings = ref([])
const stats = ref({
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
const timeline = ref([])

const selectedWing = ref(null)
const selectedHall = ref('')
const selectedType = ref('summary') // Default to clean Distilled Memories
const searchQuery = ref('')
const debouncedSearch = refDebounced(searchQuery, 250)

const isLoading = ref(false)
const autoPollInterval = ref(0)
const lastUpdated = ref(null)
const fetchError = ref(null)

const { y } = useWindowScroll({ behavior: 'smooth' })

let pollTimer = null

const hasFilters = computed(() => {
  return Boolean(
    selectedWing.value !== null ||
    selectedHall.value !== '' ||
    (selectedType.value !== '' && selectedType.value !== 'summary') ||
    searchQuery.value.trim() !== ''
  )
})

function scrollToTop() {
  window.scrollTo({ top: 0, behavior: 'smooth' })
}

async function fetchWings() {
  try {
    const res = await fetch('/api/wings')
    if (!res.ok) throw new Error(`Failed to fetch wings: ${res.statusText}`)
    const data = await res.json()
    wings.value = Array.isArray(data) ? data : []
  } catch (err) {
    console.error('Error fetching wings:', err)
  }
}

async function fetchStats() {
  try {
    const res = await fetch('/api/stats')
    if (!res.ok) throw new Error(`Failed to fetch stats: ${res.statusText}`)
    const data = await res.json()
    stats.value = data || {
      total_wings: 0,
      total_summaries: 0,
      total_verbatim: 0,
      halls: {}
    }
  } catch (err) {
    console.error('Error fetching stats:', err)
  }
}

async function fetchTimeline() {
  isLoading.value = true
  fetchError.value = null
  try {
    const params = new URLSearchParams()
    if (selectedWing.value) params.append('wing_id', selectedWing.value)
    if (selectedHall.value) params.append('hall', selectedHall.value)
    if (selectedType.value) params.append('type', selectedType.value)
    if (debouncedSearch.value.trim()) params.append('q', debouncedSearch.value.trim())
    params.append('limit', '100')

    const res = await fetch(`/api/timeline?${params.toString()}`)
    if (!res.ok) throw new Error(`Failed to fetch timeline: ${res.statusText}`)
    const data = await res.json()
    timeline.value = Array.isArray(data) ? data : []
    lastUpdated.value = new Date()
  } catch (err) {
    console.error('Error fetching timeline:', err)
    fetchError.value = err.message
  } finally {
    isLoading.value = false
  }
}

async function refreshAll() {
  await Promise.all([fetchWings(), fetchStats(), fetchTimeline()])
}

function clearFilters() {
  selectedWing.value = null
  selectedHall.value = ''
  selectedType.value = 'summary'
  searchQuery.value = ''
}

// Watch filters to refetch timeline
watch([selectedWing, selectedHall, selectedType, debouncedSearch], () => {
  fetchTimeline()
})

// Setup auto-polling timer
function updatePolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
  if (autoPollInterval.value > 0) {
    pollTimer = setInterval(() => {
      refreshAll()
    }, autoPollInterval.value)
  }
}

watch(autoPollInterval, () => {
  updatePolling()
})

onMounted(async () => {
  await refreshAll()
  updatePolling()
})

onUnmounted(() => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
})
</script>

<template>
  <div class="min-h-screen flex flex-col bg-zinc-50 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 transition-colors duration-200">
    <!-- Header with Stats and Controls -->
    <Header
      :stats="stats"
      :is-loading="isLoading"
      :auto-poll-interval="autoPollInterval"
      :last-updated="lastUpdated"
      @refresh="refreshAll"
      @update:auto-poll-interval="autoPollInterval = $event"
    />

    <!-- Filter and Search Control Bar -->
    <ControlBar
      :wings="wings"
      :selected-wing="selectedWing"
      :selected-hall="selectedHall"
      :selected-type="selectedType"
      :search-query="searchQuery"
      :result-count="timeline.length"
      :stats="stats"
      @update:selected-wing="selectedWing = $event"
      @update:selected-hall="selectedHall = $event"
      @update:selected-type="selectedType = $event"
      @update:search-query="searchQuery = $event"
      @clear-filters="clearFilters"
    />

    <!-- Error Banner (if any) -->
    <div v-if="fetchError" class="bg-red-500/10 border-b border-red-500/20 px-4 py-2 text-center text-xs text-red-600 dark:text-red-400 font-medium">
      Failed to synchronize with server: {{ fetchError }}. Retrying on next poll...
    </div>

    <!-- Main Timeline Stream -->
    <main class="flex-1">
      <TimelineView
        :items="timeline"
        :wings="wings"
        :is-loading="isLoading"
        :has-filters="hasFilters"
        :search-query="searchQuery"
        @clear-filters="clearFilters"
      />
    </main>

    <!-- Floating Scroll To Top Button -->
    <transition
      enter-active-class="transition duration-200 ease-out"
      enter-from-class="opacity-0 translate-y-4 scale-75"
      enter-to-class="opacity-100 translate-y-0 scale-100"
      leave-active-class="transition duration-150 ease-in"
      leave-from-class="opacity-100 translate-y-0 scale-100"
      leave-to-class="opacity-0 translate-y-4 scale-75"
    >
      <button
        v-if="y > 250"
        @click="scrollToTop"
        title="Scroll to top"
        class="fixed bottom-6 right-6 z-40 p-3 rounded-full bg-zinc-900/90 dark:bg-zinc-800/90 text-white dark:text-zinc-100 shadow-lg shadow-black/20 border border-zinc-700/60 hover:bg-zinc-800 dark:hover:bg-zinc-700 hover:scale-110 active:scale-95 transition-all cursor-pointer backdrop-blur flex items-center justify-center group"
      >
        <ArrowUp class="w-4 h-4 transition-transform group-hover:-translate-y-0.5" />
      </button>
    </transition>

    <!-- Footer -->
    <footer class="border-t border-zinc-200 dark:border-zinc-800/80 py-4 text-center text-xs text-zinc-400 dark:text-zinc-600 bg-white/50 dark:bg-zinc-950/50">
      <p>MemRemark • AI Distilled Memory Architecture</p>
    </footer>
  </div>
</template>
