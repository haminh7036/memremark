<script setup>
import { computed } from 'vue'
import { Calendar, Layers } from 'lucide-vue-next'
import MemoryCard from './MemoryCard.vue'
import EmptyState from './EmptyState.vue'

const props = defineProps({
  items: {
    type: Array,
    default: () => []
  },
  wings: {
    type: Array,
    default: () => []
  },
  isLoading: {
    type: Boolean,
    default: false
  },
  hasFilters: {
    type: Boolean,
    default: false
  },
  searchQuery: {
    type: String,
    default: ''
  }
})

defineEmits(['clearFilters'])

const wingsMap = computed(() => {
  const map = {}
  for (const w of props.wings) {
    map[w.id] = w
  }
  return map
})

// Group items chronologically by Day
const groupedTimeline = computed(() => {
  const groups = []
  const groupMap = new Map()

  for (const item of props.items) {
    const d = new Date(item.created_at)
    const key = isNaN(d.getTime())
      ? 'Unknown Date'
      : `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`

    if (!groupMap.has(key)) {
      const groupObj = {
        key,
        dateLabel: formatGroupDate(item.created_at),
        items: []
      }
      groupMap.set(key, groupObj)
      groups.push(groupObj)
    }

    groupMap.get(key).items.push(item)
  }

  return groups
})

function formatGroupDate(dateStr) {
  if (!dateStr) return 'Recent'
  const d = new Date(dateStr)
  if (isNaN(d.getTime())) return 'Recent'

  const today = new Date()
  const yesterday = new Date()
  yesterday.setDate(today.getDate() - 1)

  const isToday = d.getFullYear() === today.getFullYear() &&
                  d.getMonth() === today.getMonth() &&
                  d.getDate() === today.getDate()

  const isYesterday = d.getFullYear() === yesterday.getFullYear() &&
                      d.getMonth() === yesterday.getMonth() &&
                      d.getDate() === yesterday.getDate()

  if (isToday) return 'Today'
  if (isYesterday) return 'Yesterday'

  return d.toLocaleDateString(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    year: d.getFullYear() !== today.getFullYear() ? 'numeric' : undefined
  })
}
</script>

<template>
  <div class="max-w-4xl mx-auto px-4 sm:px-6 lg:px-8 py-6">
    <!-- Loading Skeleton State when initial fetch is running -->
    <div v-if="isLoading && items.length === 0" class="space-y-6 animate-pulse">
      <div class="h-6 w-32 bg-zinc-200 dark:bg-zinc-800 rounded-md mb-4"></div>
      <div v-for="i in 3" :key="i" class="relative pl-6 sm:pl-8">
        <div class="absolute left-[-5px] top-4 w-2.5 h-2.5 rounded-full bg-zinc-300 dark:bg-zinc-700"></div>
        <div class="bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl p-5 space-y-3">
          <div class="flex justify-between">
            <div class="h-4 w-24 bg-zinc-200 dark:bg-zinc-800 rounded"></div>
            <div class="h-4 w-16 bg-zinc-200 dark:bg-zinc-800 rounded"></div>
          </div>
          <div class="h-12 bg-zinc-100 dark:bg-zinc-850 rounded"></div>
          <div class="h-3 w-40 bg-zinc-200 dark:bg-zinc-800 rounded"></div>
        </div>
      </div>
    </div>

    <!-- Empty State -->
    <EmptyState
      v-else-if="items.length === 0"
      :has-filters="hasFilters"
      :search-query="searchQuery"
      @clear-filters="$emit('clearFilters')"
    />

    <!-- Timeline Content -->
    <div v-else class="space-y-8">
      <section
        v-for="group in groupedTimeline"
        :key="group.key"
        class="relative"
      >
        <!-- Date Header Sticky Label -->
        <div class="sticky top-16 z-20 bg-zinc-50/95 dark:bg-zinc-950/95 backdrop-blur py-2.5 mb-4 flex items-center gap-2.5">
          <div class="flex items-center gap-2 px-3 py-1 rounded-lg bg-zinc-200/80 dark:bg-zinc-800/80 border border-zinc-300 dark:border-zinc-700 text-xs font-semibold text-zinc-900 dark:text-zinc-100 shadow-xs">
            <Calendar class="w-3.5 h-3.5 text-indigo-500" />
            <span>{{ group.dateLabel }}</span>
          </div>
          <span class="text-xs text-zinc-400 dark:text-zinc-500 font-medium">
            {{ group.items.length }} {{ group.items.length === 1 ? 'memory' : 'memories' }}
          </span>
        </div>

        <!-- Vertical Timeline Line and Cards -->
        <div class="relative border-l border-zinc-200 dark:border-zinc-800 ml-2 sm:ml-3 space-y-4">
          <MemoryCard
            v-for="item in group.items"
            :key="item.id"
            :item="item"
            :wing="wingsMap[item.wing_id]"
          />
        </div>
      </section>
    </div>
  </div>
</template>
