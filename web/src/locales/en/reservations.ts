import type { Catalog } from '@danlu/core'

// 命名空间 reservations 的英文文案。键集必须与 zh-CN/reservations.ts 完全一致。
export const reservations = {
  // List
  'reservations.heading': 'GPU reservations',
  'reservations.heading_hint': "GPUs claimed by reservations are not allocated to Danlu jobs",
  'reservations.label': 'Reservation #{id}',
  'reservations.extend': 'Extend 1 hour',
  'reservations.release': 'Release',
  'reservations.copy_env': 'Copy env vars',
  'reservations.copy_env_title': 'Copy {text} (limits CUDA programs only; EGL/OpenGL graphics processes pick cards by their own device index)',

  // State line
  'reservations.waiting_cards': 'waiting for GPU {gpus}',
  'reservations.claiming_progress': 'claiming {held}/{want}',
  'reservations.waiting_state': '{what} · waited {duration}',
  'reservations.ready_manual': 'ready · held until released manually',
  'reservations.ready_until': 'ready · until {time} ({remaining} left)',

  // Release confirmation
  'reservations.release_title': 'Release reservation #{id}?',
  'reservations.release_cancel': 'Not now',
  'reservations.release_body_cards': 'GPU {gpus} will be returned to Danlu and may be used by queued jobs right away.',
  'reservations.release_body_empty': 'This reservation has not claimed any cards yet; releasing it stops the wait.',
  'reservations.release_body_note': " GPUs with external processes will still not be allocated by Danlu.",

  // Create dialog
  'reservations.reserve_gpu': 'Reserve GPUs',
  'reservations.submit': 'Reserve',
  'reservations.intro': "Hold GPUs for work that does not run on Danlu. Free GPUs are claimed immediately; if there are not enough, GPUs released by Danlu jobs go to this reservation before queued jobs, and the timer starts once all GPUs are claimed.",
  'reservations.intro_push': " A phone push is sent when all GPUs are claimed and 10 minutes before auto-release.",
  'reservations.mode_label': 'Reservation mode',
  'reservations.cards_summary': { one: "{total} GPU total, {free} free now", other: "{total} GPUs total, {free} free now" },
  'reservations.count_label': "Number of GPUs to reserve",
  'reservations.count_suffix': { one: "GPU, only idle ones with no processes", other: "GPUs, only idle ones with no processes" },
  'reservations.short_warning': "Not enough free GPUs right now; the reservation claims what it can first and completes when Danlu jobs finish.",
  'reservations.auto_release': 'Auto-release',
  'reservations.length_label': 'Auto-release time',
  'reservations.length_1h': '1 hour',
  'reservations.length_3h': '3 hours',
  'reservations.length_8h': '8 hours',
  'reservations.length_custom': 'Custom',
  'reservations.length_none': 'Manual',
  'reservations.custom_hours_suffix': "hours, then release (starting when all GPUs are claimed)",
  'reservations.hours_range': 'Duration must be between 10 minutes and 7 days (168 hours)',
  'reservations.none_hint': "GPUs are held indefinitely; remember to come back and release them.",
  'reservations.note_label': 'Purpose',
  'reservations.note_placeholder': 'Optional, e.g. manual DDP tuning',
} satisfies Catalog
