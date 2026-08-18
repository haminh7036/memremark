<script setup>
import { Sparkles, RefreshCcw, SearchX } from 'lucide-vue-next'

defineProps({
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
</script>

<template>
  <div class="flex flex-col items-center justify-center py-16 px-4 text-center">
    <div class="w-14 h-14 rounded-2xl bg-zinc-100 dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 flex items-center justify-center mb-4 text-zinc-400 dark:text-zinc-500 shadow-sm">
      <SearchX v-if="hasFilters" class="w-7 h-7" />
      <Sparkles v-else class="w-7 h-7" />
    </div>

    <h3 class="text-base font-semibold text-zinc-900 dark:text-zinc-100 mb-1">
      <template v-if="hasFilters">No matching memories found</template>
      <template v-else>No memories recorded yet</template>
    </h3>

    <p class="text-sm text-zinc-500 dark:text-zinc-400 max-w-sm mb-6">
      <template v-if="searchQuery">
        No memories match your query <span class="font-medium text-zinc-800 dark:text-zinc-200">"{{ searchQuery }}"</span>.
      </template>
      <template v-else-if="hasFilters">
        Try adjusting your workspace, hall, or entry type filters to find what you're looking for.
      </template>
      <template v-else>
        Memories will automatically appear here as your AI agent interacts with your projects.
      </template>
    </p>

    <button
      v-if="hasFilters"
      @click="$emit('clearFilters')"
      class="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium text-zinc-700 dark:text-zinc-200 bg-white dark:bg-zinc-800 border border-zinc-300 dark:border-zinc-700 rounded-lg hover:bg-zinc-50 dark:hover:bg-zinc-750 transition-colors shadow-sm focus:outline-none focus:ring-2 focus:ring-indigo-500/20"
    >
      <RefreshCcw class="w-4 h-4" />
      Reset all filters
    </button>
  </div>
</template>
