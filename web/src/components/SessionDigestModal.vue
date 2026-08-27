<script setup>
import { ref, watch } from 'vue'
import { onKeyStroke, useClipboard } from '@vueuse/core'
import {
  X,
  Sparkles,
  HelpCircle,
  Search,
  Lightbulb,
  CheckCircle2,
  ArrowRightCircle,
  StickyNote,
  Clock,
  Copy,
  Check,
  FileQuestion
} from 'lucide-vue-next'

const props = defineProps({
  sessionId: {
    type: String,
    default: null
  },
  isOpen: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['close'])

const digest = ref(null)
const isLoading = ref(false)
const error = ref(null)

const { copy, copied } = useClipboard({ copiedDuring: 2000 })

async function fetchDigest(sessionId) {
  if (!sessionId) {
    digest.value = null
    return
  }
  isLoading.value = true
  error.value = null
  try {
    const res = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/digest`)
    if (res.status === 404) {
      digest.value = null
      error.value = 'No synthesized digest found for this session yet.'
      return
    }
    if (!res.ok) {
      throw new Error(`Failed to load session digest: ${res.statusText}`)
    }
    const data = await res.json()
    digest.value = data
  } catch (err) {
    console.error('Error fetching session digest:', err)
    error.value = err.message
  } finally {
    isLoading.value = false
  }
}

watch(
  () => [props.isOpen, props.sessionId],
  ([isOpen, sessionId]) => {
    if (isOpen && sessionId) {
      fetchDigest(sessionId)
    } else {
      digest.value = null
      error.value = null
    }
  },
  { immediate: true }
)

onKeyStroke('Escape', () => {
  if (props.isOpen) {
    emit('close')
  }
})

function formatTimestamp(dateStr) {
  if (!dateStr) return ''
  return new Date(dateStr).toLocaleString(undefined, {
    dateStyle: 'medium',
    timeStyle: 'medium'
  })
}

function handleCopySummary() {
  if (!digest.value) return
  const lines = [
    `Session: ${digest.value.session_id}`,
    digest.value.request ? `Request: ${digest.value.request}` : '',
    digest.value.investigated ? `Investigated: ${digest.value.investigated}` : '',
    digest.value.learned ? `Learned: ${digest.value.learned}` : '',
    digest.value.completed ? `Completed: ${digest.value.completed}` : '',
    digest.value.next_steps ? `Next Steps: ${digest.value.next_steps}` : '',
    digest.value.notes ? `Notes: ${digest.value.notes}` : ''
  ].filter(Boolean)
  copy(lines.join('\n\n'))
}
</script>

<template>
  <teleport to="body">
    <transition
      enter-active-class="transition duration-200 ease-out"
      enter-from-class="opacity-0"
      enter-to-class="opacity-100"
      leave-active-class="transition duration-150 ease-in"
      leave-from-class="opacity-100"
      leave-to-class="opacity-0"
    >
      <div
        v-if="isOpen"
        class="fixed inset-0 z-50 overflow-y-auto bg-zinc-950/70 backdrop-blur-xs flex items-center justify-center p-4 sm:p-6"
        @click.self="emit('close')"
      >
        <div
          class="relative w-full max-w-2xl bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-2xl shadow-2xl overflow-hidden animate-in zoom-in-95 duration-150"
        >
          <!-- Header -->
          <div class="px-6 py-4 border-b border-zinc-200 dark:border-zinc-800 flex items-center justify-between bg-zinc-50/50 dark:bg-zinc-950/50">
            <div class="flex items-center gap-2.5">
              <div class="w-8 h-8 rounded-lg bg-indigo-500/10 text-indigo-600 dark:text-indigo-400 flex items-center justify-center border border-indigo-500/20">
                <Sparkles class="w-4 h-4" />
              </div>
              <div>
                <h3 class="text-sm font-bold text-zinc-900 dark:text-zinc-100 flex items-center gap-2">
                  <span>Session Digest</span>
                  <span
                    v-if="sessionId"
                    class="font-mono text-xs font-normal text-zinc-500 dark:text-zinc-400 bg-zinc-100 dark:bg-zinc-800 px-2 py-0.5 rounded border border-zinc-200 dark:border-zinc-700"
                  >
                    {{ sessionId }}
                  </span>
                </h3>
                <p class="text-[11px] text-zinc-500 dark:text-zinc-400">High-level synthesized knowledge distillation</p>
              </div>
            </div>

            <div class="flex items-center gap-1.5">
              <button
                v-if="digest"
                @click="handleCopySummary"
                :title="copied ? 'Copied!' : 'Copy digest'"
                class="p-2 rounded-lg text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors cursor-pointer"
              >
                <Check v-if="copied" class="w-4 h-4 text-emerald-500" />
                <Copy v-else class="w-4 h-4" />
              </button>
              <button
                @click="emit('close')"
                title="Close (Esc)"
                class="p-2 rounded-lg text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors cursor-pointer"
              >
                <X class="w-4 h-4" />
              </button>
            </div>
          </div>

          <!-- Body -->
          <div class="p-6 max-h-[75vh] overflow-y-auto space-y-4">
            <!-- Loading State -->
            <div v-if="isLoading" class="py-12 flex flex-col items-center justify-center space-y-3">
              <div class="w-6 h-6 border-2 border-indigo-500 border-t-transparent rounded-full animate-spin"></div>
              <p class="text-xs text-zinc-500 dark:text-zinc-400 font-medium">Synthesizing session knowledge...</p>
            </div>

            <!-- Error / Empty State -->
            <div v-else-if="error || !digest" class="py-10 text-center space-y-3">
              <div class="w-12 h-12 rounded-xl bg-zinc-100 dark:bg-zinc-800 text-zinc-400 flex items-center justify-center mx-auto">
                <FileQuestion class="w-6 h-6" />
              </div>
              <h4 class="text-sm font-semibold text-zinc-800 dark:text-zinc-200">No Digest Available</h4>
              <p class="text-xs text-zinc-500 dark:text-zinc-400 max-w-sm mx-auto">
                {{ error || 'A session digest has not been generated for this session yet. It will automatically synthesize after batch distillation.' }}
              </p>
            </div>

            <!-- Digest Content Sections -->
            <div v-else class="space-y-3.5">
              <!-- Request -->
              <div v-if="digest.request" class="p-3.5 rounded-xl bg-blue-50/50 dark:bg-blue-950/20 border border-blue-200/60 dark:border-blue-800/40">
                <div class="flex items-center gap-1.5 text-xs font-semibold text-blue-700 dark:text-blue-300 mb-1.5">
                  <HelpCircle class="w-3.5 h-3.5" />
                  <span>Target Request</span>
                </div>
                <p class="text-xs text-zinc-800 dark:text-zinc-200 leading-relaxed whitespace-pre-wrap">{{ digest.request }}</p>
              </div>

              <!-- Investigated -->
              <div v-if="digest.investigated" class="p-3.5 rounded-xl bg-purple-50/50 dark:bg-purple-950/20 border border-purple-200/60 dark:border-purple-800/40">
                <div class="flex items-center gap-1.5 text-xs font-semibold text-purple-700 dark:text-purple-300 mb-1.5">
                  <Search class="w-3.5 h-3.5" />
                  <span>Investigated & Explored</span>
                </div>
                <p class="text-xs text-zinc-800 dark:text-zinc-200 leading-relaxed whitespace-pre-wrap">{{ digest.investigated }}</p>
              </div>

              <!-- Learned -->
              <div v-if="digest.learned" class="p-3.5 rounded-xl bg-amber-50/50 dark:bg-amber-950/20 border border-amber-200/60 dark:border-amber-800/40">
                <div class="flex items-center gap-1.5 text-xs font-semibold text-amber-700 dark:text-amber-300 mb-1.5">
                  <Lightbulb class="w-3.5 h-3.5" />
                  <span>Learned Insights</span>
                </div>
                <p class="text-xs text-zinc-800 dark:text-zinc-200 leading-relaxed whitespace-pre-wrap">{{ digest.learned }}</p>
              </div>

              <!-- Completed -->
              <div v-if="digest.completed" class="p-3.5 rounded-xl bg-emerald-50/50 dark:bg-emerald-950/20 border border-emerald-200/60 dark:border-emerald-800/40">
                <div class="flex items-center gap-1.5 text-xs font-semibold text-emerald-700 dark:text-emerald-300 mb-1.5">
                  <CheckCircle2 class="w-3.5 h-3.5" />
                  <span>Completed Work</span>
                </div>
                <p class="text-xs text-zinc-800 dark:text-zinc-200 leading-relaxed whitespace-pre-wrap">{{ digest.completed }}</p>
              </div>

              <!-- Next Steps -->
              <div v-if="digest.next_steps" class="p-3.5 rounded-xl bg-indigo-50/50 dark:bg-indigo-950/20 border border-indigo-200/60 dark:border-indigo-800/40">
                <div class="flex items-center gap-1.5 text-xs font-semibold text-indigo-700 dark:text-indigo-300 mb-1.5">
                  <ArrowRightCircle class="w-3.5 h-3.5" />
                  <span>Next Steps</span>
                </div>
                <p class="text-xs text-zinc-800 dark:text-zinc-200 leading-relaxed whitespace-pre-wrap">{{ digest.next_steps }}</p>
              </div>

              <!-- Notes -->
              <div v-if="digest.notes" class="p-3.5 rounded-xl bg-zinc-50 dark:bg-zinc-950 border border-zinc-200/80 dark:border-zinc-800">
                <div class="flex items-center gap-1.5 text-xs font-semibold text-zinc-600 dark:text-zinc-400 mb-1.5">
                  <StickyNote class="w-3.5 h-3.5" />
                  <span>Notes</span>
                </div>
                <p class="text-xs text-zinc-800 dark:text-zinc-200 leading-relaxed whitespace-pre-wrap">{{ digest.notes }}</p>
              </div>
            </div>
          </div>

          <!-- Footer -->
          <div class="px-6 py-3 border-t border-zinc-200 dark:border-zinc-800 bg-zinc-50/50 dark:bg-zinc-950/50 flex items-center justify-between text-xs text-zinc-500 dark:text-zinc-400">
            <div v-if="digest?.created_at" class="flex items-center gap-1.5">
              <Clock class="w-3.5 h-3.5 text-zinc-400" />
              <span>Created: {{ formatTimestamp(digest.created_at) }}</span>
            </div>
            <div v-else></div>
            <button
              @click="emit('close')"
              class="px-4 py-1.5 rounded-lg bg-zinc-200 dark:bg-zinc-800 hover:bg-zinc-300 dark:hover:bg-zinc-700 text-zinc-800 dark:text-zinc-200 font-medium transition-colors cursor-pointer text-xs"
            >
              Close
            </button>
          </div>
        </div>
      </div>
    </transition>
  </teleport>
</template>
