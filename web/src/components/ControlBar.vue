<script setup>
import { ref } from 'vue'
import { onKeyStroke } from '@vueuse/core'
import {
  Search,
  FolderGit2,
  X,
  Info,
  Lightbulb,
  Heart,
  Compass,
  Activity,
  Layers,
  FileText
} from 'lucide-vue-next'

const searchInputRef = ref(null)

const props = defineProps({
  wings: {
    type: Array,
    default: () => []
  },
  selectedWing: {
    type: [Number, null],
    default: null
  },
  selectedHall: {
    type: String,
    default: ''
  },
  selectedType: {
    type: String,
    default: ''
  },
  searchQuery: {
    type: String,
    default: ''
  },
  resultCount: {
    type: Number,
    default: 0
  },
  stats: {
    type: Object,
    default: () => ({})
  }
})

const emit = defineEmits([
  'update:selectedWing',
  'update:selectedHall',
  'update:selectedType',
  'update:searchQuery',
  'clearFilters'
])

// Keyboard shortcut '/' to focus search bar
onKeyStroke('/', (e) => {
  if (
    document.activeElement?.tagName === 'INPUT' ||
    document.activeElement?.tagName === 'TEXTAREA'
  ) {
    return
  }
  e.preventDefault()
  searchInputRef.value?.focus()
})

const halls = [
  { id: '', name: 'All Halls', icon: Layers, activeClass: 'bg-zinc-800 text-white dark:bg-zinc-100 dark:text-zinc-900 border-zinc-700 dark:border-zinc-300' },
  { id: 'fact', name: 'Fact', icon: Info, activeClass: 'bg-blue-500/15 text-blue-700 dark:text-blue-300 border-blue-500/40 ring-1 ring-blue-500/20' },
  { id: 'discovery', name: 'Discovery', icon: Lightbulb, activeClass: 'bg-purple-500/15 text-purple-700 dark:text-purple-300 border-purple-500/40 ring-1 ring-purple-500/20' },
  { id: 'preference', name: 'Preference', icon: Heart, activeClass: 'bg-amber-500/15 text-amber-700 dark:text-amber-300 border-amber-500/40 ring-1 ring-amber-500/20' },
  { id: 'advice', name: 'Advice', icon: Compass, activeClass: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/40 ring-1 ring-emerald-500/20' },
  { id: 'event', name: 'Event', icon: Activity, activeClass: 'bg-zinc-500/15 text-zinc-700 dark:text-zinc-300 border-zinc-500/40 ring-1 ring-zinc-500/20' }
]

function getHallCount(hallId) {
  if (!props.stats?.halls) return null
  if (!hallId) return null
  return props.stats.halls[hallId] ?? 0
}

function handleWingChange(e) {
  const val = e.target.value
  emit('update:selectedWing', val ? Number(val) : null)
}
</script>

<template>
  <div class="bg-white dark:bg-zinc-950 border-b border-zinc-200 dark:border-zinc-800 py-4 transition-colors duration-200">
    <div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 space-y-3.5">
      <!-- Top Row: Workspace Selector & Search Input & Type Switcher -->
      <div class="flex flex-col lg:flex-row items-stretch lg:items-center gap-3">
        <!-- Workspace Dropdown -->
        <div class="relative min-w-[240px] max-w-full lg:max-w-xs">
          <div class="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-zinc-400 dark:text-zinc-500">
            <FolderGit2 class="w-4 h-4" />
          </div>
          <select
            :value="selectedWing ?? ''"
            @change="handleWingChange"
            class="w-full pl-9 pr-8 py-2 text-sm bg-zinc-50 dark:bg-zinc-900 border border-zinc-300 dark:border-zinc-750 rounded-lg text-zinc-800 dark:text-zinc-200 focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-500 font-medium transition-colors"
          >
            <option value="">All Workspaces ({{ wings.length }})</option>
            <option
              v-for="w in wings"
              :key="w.id"
              :value="w.id"
            >
              {{ w.name }} ({{ w.summary_count }} summaries)
            </option>
          </select>
        </div>

        <!-- Search Input with live keyboard shortcut & clear button -->
        <div class="relative flex-1">
          <div class="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-zinc-400 dark:text-zinc-500">
            <Search class="w-4 h-4" />
          </div>
          <input
            ref="searchInputRef"
            type="text"
            :value="searchQuery"
            @input="emit('update:searchQuery', $event.target.value)"
            placeholder="Search distilled memory and logs... (Press '/' to focus)"
            class="w-full pl-9 pr-14 py-2 text-sm bg-zinc-50 dark:bg-zinc-900 border border-zinc-300 dark:border-zinc-750 rounded-lg text-zinc-900 dark:text-zinc-100 placeholder-zinc-400 dark:placeholder-zinc-500 focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-500 transition-colors"
          />
          <div class="absolute inset-y-0 right-0 pr-2.5 flex items-center gap-1">
            <button
              v-if="searchQuery"
              @click="emit('update:searchQuery', '')"
              class="p-1 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200 rounded"
              title="Clear search"
            >
              <X class="w-3.5 h-3.5" />
            </button>
            <kbd
              v-else
              class="hidden sm:inline-block px-1.5 py-0.5 text-[10px] font-semibold text-zinc-400 dark:text-zinc-500 bg-zinc-200 dark:bg-zinc-800 border border-zinc-300 dark:border-zinc-700 rounded"
            >
              /
            </kbd>
          </div>
        </div>

        <!-- Type Switcher (Segmented Control) -->
        <div class="flex items-center bg-zinc-100 dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-lg p-1 text-xs self-start lg:self-auto">
          <button
            @click="emit('update:selectedType', '')"
            :class="[
              'px-3 py-1.5 rounded-md font-medium transition-all flex items-center gap-1.5',
              selectedType === ''
                ? 'bg-white dark:bg-zinc-800 text-zinc-900 dark:text-zinc-100 shadow-xs'
                : 'text-zinc-500 dark:text-zinc-400 hover:text-zinc-800 dark:hover:text-zinc-200'
            ]"
          >
            <Layers class="w-3.5 h-3.5" />
            All Types
          </button>
          <button
            @click="emit('update:selectedType', 'summary')"
            :class="[
              'px-3 py-1.5 rounded-md font-medium transition-all flex items-center gap-1.5',
              selectedType === 'summary'
                ? 'bg-white dark:bg-zinc-800 text-indigo-600 dark:text-indigo-400 shadow-xs'
                : 'text-zinc-500 dark:text-zinc-400 hover:text-zinc-800 dark:hover:text-zinc-200'
            ]"
          >
            <FileText class="w-3.5 h-3.5" />
            Distilled
          </button>
          <button
            @click="emit('update:selectedType', 'verbatim')"
            :class="[
              'px-3 py-1.5 rounded-md font-medium transition-all flex items-center gap-1.5',
              selectedType === 'verbatim'
                ? 'bg-white dark:bg-zinc-800 text-emerald-600 dark:text-emerald-400 shadow-xs'
                : 'text-zinc-500 dark:text-zinc-400 hover:text-zinc-800 dark:hover:text-zinc-200'
            ]"
          >
            <Activity class="w-3.5 h-3.5" />
            Verbatim Logs
          </button>
        </div>
      </div>

      <!-- Bottom Row: Hall Filter Pills & Match Count -->
      <div class="flex flex-wrap items-center justify-between gap-2.5 pt-1">
        <!-- Hall Filter Pills -->
        <div class="flex flex-wrap items-center gap-1.5">
          <span class="text-xs font-medium text-zinc-500 dark:text-zinc-400 mr-1">Halls:</span>
          <button
            v-for="h in halls"
            :key="h.id"
            @click="emit('update:selectedHall', h.id)"
            :class="[
              'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium border transition-all duration-150',
              selectedHall === h.id
                ? h.activeClass
                : 'bg-zinc-50 dark:bg-zinc-900 text-zinc-600 dark:text-zinc-400 border-zinc-200 dark:border-zinc-800 hover:border-zinc-300 dark:hover:border-zinc-700'
            ]"
          >
            <component :is="h.icon" class="w-3 h-3" />
            <span>{{ h.name }}</span>
            <span
              v-if="getHallCount(h.id) !== null"
              class="text-[10px] px-1 py-0.2 rounded-full bg-black/5 dark:bg-white/10 font-semibold"
            >
              {{ getHallCount(h.id) }}
            </span>
          </button>
        </div>

        <!-- Result count & reset button -->
        <div class="flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400 font-medium">
          <span>Showing <strong class="text-zinc-900 dark:text-zinc-100">{{ resultCount }}</strong> entries</span>
          <button
            v-if="selectedWing || selectedHall || selectedType || searchQuery"
            @click="emit('clearFilters')"
            class="text-indigo-600 dark:text-indigo-400 hover:underline inline-flex items-center gap-1 ml-1"
          >
            <X class="w-3 h-3" />
            Clear
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
