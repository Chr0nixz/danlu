import type { Catalog } from '@danlu/core'

// 命名空间 gpus 的英文文案。键集必须与 zh-CN/gpus.ts 完全一致。
export const gpus = {
  // Card state and metrics
  'gpus.idle': 'Idle',
  'gpus.unknown_mem': "Unexplained memory",
  'gpus.unknown_mem_title': 'The processes listed by nvidia-smi cannot explain this memory usage (e.g. processes in another container); this card will not be allocated',
  'gpus.util': 'Util',
  'gpus.mem': 'Memory',

  // Occupant process labels (single process / grouped by job or user)
  'gpus.proc_job': 'Job #{id} process',
  'gpus.proc_job_graphics': 'Job #{id} graphics process',
  'gpus.proc_external': 'External process',
  'gpus.proc_external_graphics': 'External graphics process',
  'gpus.job_proc_count': { one: 'Job #{job}: {n} process', other: 'Job #{job}: {n} processes' },
  'gpus.external_proc_count': { one: '{n} external process', other: '{n} external processes' },
  'gpus.no_procs': 'No processes of this job on the card yet',
  'gpus.no_procs_title': 'This card is allocated to the job, but none of its processes are on it yet: it may still be starting, or the program may not use this card',

  // Process tooltips: head is the first half (pid list, user, graphics count), reused by the full and reserved-card variants
  'gpus.stray_user': ', user {user}',
  'gpus.stray_graphics': {
    one: ', including {n} graphics process (EGL/OpenGL rendering; picks cards by its own device index, ignoring CUDA_VISIBLE_DEVICES)',
    other: ', including {n} graphics processes (EGL/OpenGL rendering; pick cards by their own device index, ignoring CUDA_VISIBLE_DEVICES)',
  },
  'gpus.stray_cvd': ', process CUDA_VISIBLE_DEVICES points to GPU {cvd}',
  'gpus.stray_cvd_unset': ', process has no CUDA_VISIBLE_DEVICES',
  'gpus.stray_job_head': 'Job #{job} processes (pid {pids}{kinds})',
  'gpus.stray_external_head': 'External processes (pid {pids}{user}{kinds})',
  'gpus.stray_job_title': '{head}, but this card was not allocated to it: the program picked the card itself, or these are leftovers of a finished job. This card will not be allocated',
  'gpus.stray_external_title': '{head}: not started by Danlu; this card will not be allocated',
  'gpus.stray_reserved_title': '{head}: running on a reserved card',

  // Reservation line on a card
  'gpus.reservation_label': 'Reservation #{id}',
  'gpus.res_claiming': "Claiming GPUs",
  'gpus.res_until': 'until {time}',
  'gpus.res_manual': 'manual release',
  'gpus.res_line_title': 'Reservation #{id}: Danlu will not allocate this card to jobs',
  'gpus.res_line_title_note': 'Reservation #{id} ({note}): Danlu will not allocate this card to jobs',

  // Card picking (shared by the new-job form and the reserve dialog)
  'gpus.mode_count': 'By count',
  'gpus.mode_pinned': "Pick GPUs",
  'gpus.count_less': "One fewer GPU",
  'gpus.count_more': "One more GPU",
  'gpus.pick_required': "Select at least one GPU",
  'gpus.picker_job': 'In use by #{id}',

  // Load failures and empty state
  'gpus.load_failed': 'Failed to read GPUs: {message}',
  'gpus.sample_failed': 'Failed to read GPU info; allocating new jobs is paused: {error}',
  'gpus.error_since': '(ongoing for {duration})',
  'gpus.error_duration_s': { one: '{n} second', other: '{n} seconds' },
  'gpus.error_duration_m': { one: '{n} minute', other: '{n} minutes' },
  'gpus.error_duration_hm': '{h} h {m} min',
  'gpus.none_detected': 'No GPUs detected. Only jobs that need no GPU can be submitted.',
} satisfies Catalog
