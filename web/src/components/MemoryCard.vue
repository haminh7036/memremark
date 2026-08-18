<script setup>
import { computed } from 'vue'
import { useClipboard } from '@vueuse/core'
import {
  Copy,
  Check,
  Info,
  Lightbulb,
  Heart,
  Compass,
  Activity,
  Terminal,
  Clock,
  Folder,
  Calendar,
  Hash,
  FileCode
} from 'lucide-vue-next'

const props = defineProps({
  item: {
    type: Object,
    required: true
  },
  wing: {
    type: Object,
    default: null
  }
})

const { copy, copied } = useClipboard({ copiedDuring: 2000 })

const hallConfig = computed(() => {
  switch (props.item.hall) {
    case 'fact':
      return {
        label: 'Fact',
        icon: Info,
        dotClass: 'bg-blue-500 ring-4 ring-blue-500/20',
        badgeClass: 'bg-blue-500/10 text-blue-700 dark:text-blue-400 border-blue-500/20'
      }
    case 'discovery':
      return {
        label: 'Discovery',
        icon: Lightbulb,
        dotClass: 'bg-purple-500 ring-4 ring-purple-500/20',
        badgeClass: 'bg-purple-500/10 text-purple-700 dark:text-purple-400 border-purple-500/20'
      }
    case 'preference':
      return {
        label: 'Preference',
        icon: Heart,
        dotClass: 'bg-amber-500 ring-4 ring-amber-500/20',
        badgeClass: 'bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20'
      }
    case 'advice':
      return {
        label: 'Advice',
        icon: Compass,
        dotClass: 'bg-emerald-500 ring-4 ring-emerald-500/20',
        badgeClass: 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400 border-emerald-500/20'
      }
    case 'event':
    default:
      return {
        label: props.item.tool_name ? `Tool: ${props.item.tool_name}` : 'Event',
        icon: props.item.tool_name ? Terminal : Activity,
        dotClass: 'bg-zinc-400 dark:bg-zinc-600 ring-4 ring-zinc-500/20',
        badgeClass: 'bg-zinc-500/10 text-zinc-700 dark:text-zinc-400 border-zinc-500/20'
      }
  }
})

function formatRelativeTime(dateStr) {
  if (!dateStr) return ''
  const date = new Date(dateStr)
  const now = new Date()
  const diffSec = Math.floor((now - date) / 1000)
  if (diffSec < 10) return 'just now'
  if (diffSec < 60) return `${diffSec}s ago`
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHour = Math.floor(diffMin / 60)
  if (diffHour < 24) return `${diffHour}h ago`
  const diffDays = Math.floor(diffHour / 24)
  if (diffDays === 1) return 'yesterday'
  if (diffDays < 7) return `${diffDays}d ago`
  return date.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    year: date.getFullYear() !== now.getFullYear() ? 'numeric' : undefined
  })
}

function formatAbsoluteTime(dateStr) {
  if (!dateStr) return ''
  return new Date(dateStr).toLocaleString(undefined, {
    dateStyle: 'medium',
    timeStyle: 'medium'
  })
}

function formatCoversDate(epochSec) {
  if (!epochSec) return ''
  return new Date(epochSec * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

// Split content into code blocks and text segments for rich display
const parsedContent = computed(() => {
  const content = props.item.content || ''
  const codeBlockRegex = /```([a-zA-Z0-9_-]*)\n([\s\S]*?)```/g
  const segments = []
  let lastIndex = 0
  let match

  while ((match = codeBlockRegex.exec(content)) !== null) {
    if (match.index > lastIndex) {
      segments.push({
        type: 'text',
        text: content.slice(lastIndex, match.index)
      })
    }
    segments.push({
      type: 'code',
      lang: match[1] || 'text',
      code: match[2].trimEnd()
    })
    lastIndex = match.index + match[0].length
  }

  if (lastIndex < content.length) {
    segments.push({
      type: 'text',
      text: content.slice(lastIndex)
    })
  }

  return segments
})

function handleCopy() {
  copy(props.item.content)
}
</script>

<template>
  <div class="relative pl-6 sm:pl-8 group">
    <!-- Timeline node dot -->
    <div
      :class="[
        'absolute left-[-5px] top-4 w-2.5 h-2.5 rounded-full transition-transform group-hover:scale-125 z-10',
        hallConfig.dotClass
      ]"
    ></div>

    <!-- Main Card Container -->
    <div class="bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl p-4 sm:p-5 shadow-xs hover:shadow-md hover:border-zinc-300 dark:hover:border-zinc-700 transition-all duration-200">
      <!-- Card Header -->
      <div class="flex flex-wrap items-center justify-between gap-2 mb-3 pb-2.5 border-b border-zinc-100 dark:border-zinc-800/80">
        <!-- Left Badges: Hall, Type, Tool, Session -->
        <div class="flex flex-wrap items-center gap-2">
          <!-- Hall Badge -->
          <span
            :class="[
              'inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-semibold border',
              hallConfig.badgeClass
            ]"
          >
            <component :is="hallConfig.icon" class="w-3 h-3" />
            <span>{{ hallConfig.label }}</span>
          </span>

          <!-- Type Badge (Summary vs Verbatim) -->
          <span
            v-if="item.type === 'summary'"
            class="inline-flex items-center px-2 py-0.5 rounded text-[11px] font-medium bg-indigo-50 dark:bg-indigo-950/50 text-indigo-700 dark:text-indigo-300 border border-indigo-200 dark:border-indigo-800/50"
          >
            Distilled
          </span>
          <span
            v-else-if="item.type === 'verbatim'"
            class="inline-flex items-center px-2 py-0.5 rounded text-[11px] font-medium bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-400 border border-zinc-200 dark:border-zinc-700"
          >
            Verbatim Log
          </span>

          <!-- Tool Name Badge (if present) -->
          <span
            v-if="item.tool_name"
            class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] font-mono bg-zinc-100 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-300 border border-zinc-200 dark:border-zinc-700"
          >
            <Terminal class="w-3 h-3 text-zinc-400" />
            {{ item.tool_name }}
          </span>

          <!-- Session Badge (if present) -->
          <span
            v-if="item.session_id"
            :title="`Session ID: ${item.session_id}`"
            class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-mono bg-zinc-50 dark:bg-zinc-850 text-zinc-500 dark:text-zinc-400 border border-zinc-200 dark:border-zinc-800"
          >
            <Hash class="w-2.5 h-2.5 text-zinc-400" />
            {{ item.session_id.length > 8 ? item.session_id.slice(0, 8) + '…' : item.session_id }}
          </span>
        </div>

        <!-- Right: Timestamp & Copy Button -->
        <div class="flex items-center gap-2 text-xs text-zinc-400 dark:text-zinc-500">
          <div
            class="flex items-center gap-1 cursor-default"
            :title="formatAbsoluteTime(item.created_at)"
          >
            <Clock class="w-3.5 h-3.5" />
            <span class="font-medium text-zinc-600 dark:text-zinc-400">{{ formatRelativeTime(item.created_at) }}</span>
          </div>

          <!-- 1-Click Copy Button -->
          <button
            @click="handleCopy"
            :title="copied ? 'Copied to clipboard!' : 'Copy content'"
            class="p-1.5 rounded-md hover:bg-zinc-100 dark:hover:bg-zinc-800 text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors focus:outline-none focus:ring-1 focus:ring-indigo-500/20"
          >
            <Check v-if="copied" class="w-3.5 h-3.5 text-emerald-500 animate-in zoom-in-50" />
            <Copy v-else class="w-3.5 h-3.5" />
          </button>
        </div>
      </div>

      <!-- Card Body: Structured Content -->
      <div class="text-sm text-zinc-800 dark:text-zinc-200 leading-relaxed space-y-2">
        <template v-for="(seg, idx) in parsedContent" :key="idx">
          <!-- Text Segment -->
          <div
            v-if="seg.type === 'text'"
            class="whitespace-pre-wrap break-words"
          >{{ seg.text }}</div>

          <!-- Code Block Segment -->
          <div
            v-else-if="seg.type === 'code'"
            class="rounded-lg overflow-hidden border border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-950 my-2"
          >
            <div
              v-if="seg.lang && seg.lang !== 'text'"
              class="px-3 py-1 bg-zinc-100 dark:bg-zinc-900 border-b border-zinc-200 dark:border-zinc-800 text-[10px] font-mono text-zinc-500 uppercase tracking-wider flex items-center justify-between"
            >
              <span>{{ seg.lang }}</span>
              <FileCode class="w-3 h-3 text-zinc-400" />
            </div>
            <pre class="p-3 text-xs font-mono overflow-x-auto text-zinc-800 dark:text-zinc-200"><code>{{ seg.code }}</code></pre>
          </div>
        </template>
      </div>

      <!-- Card Footer: Workspace & Coverage Range (if present) -->
      <div
        v-if="wing || item.wing_path || (item.covers_from && item.covers_to)"
        class="mt-3.5 pt-2.5 border-t border-zinc-100 dark:border-zinc-800/60 flex flex-wrap items-center justify-between gap-2 text-xs text-zinc-500 dark:text-zinc-400"
      >
        <!-- Workspace Name/Path -->
        <div v-if="wing || item.wing_path" class="flex items-center gap-1.5 truncate max-w-md">
          <Folder class="w-3.5 h-3.5 text-zinc-400 shrink-0" />
          <span class="font-medium text-zinc-700 dark:text-zinc-300 truncate">
            {{ wing ? wing.name : item.wing_name || item.wing_path }}
          </span>
          <span v-if="wing?.path || item.wing_path" class="text-zinc-400 dark:text-zinc-600 text-[11px] truncate hidden sm:inline">
            ({{ wing ? wing.path : item.wing_path }})
          </span>
        </div>

        <!-- Coverage Period for summaries -->
        <div v-if="item.covers_from && item.covers_to" class="flex items-center gap-1 text-[11px] text-zinc-400 dark:text-zinc-500 ml-auto">
          <Calendar class="w-3 h-3 text-zinc-400" />
          <span>Covers: {{ formatCoversDate(item.covers_from) }} - {{ formatCoversDate(item.covers_to) }}</span>
        </div>
      </div>
    </div>
  </div>
</template>
