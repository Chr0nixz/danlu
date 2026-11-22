import type { Catalog } from '@danlu/core'

// 命名空间 jobs 的英文文案。键集必须与 zh-CN/jobs.ts 完全一致。
export const jobs = {
  // Jobs page: title, filters, search, compare selection
  'jobs.title': 'Jobs',
  'jobs.filter_all': 'All',
  'jobs.filter_queued': 'Queued',
  'jobs.filter_by_status': 'Filter by status',
  'jobs.starred_only': 'Starred only',
  'jobs.selection_and': ' and ',
  'jobs.select_two_hint': 'Select two jobs',
  'jobs.view_compare': 'View comparison',
  'jobs.exit_selection': 'Exit selection',
  'jobs.compare': 'Compare',
  'jobs.search_label': 'Search jobs',
  'jobs.search_placeholder': 'Search jobs, or paste an output path',
  'jobs.search_clear': 'Clear search',
  'jobs.recent_24h': "Finished in the last 24h",
  'jobs.recent_remove_label': 'Remove filter: finished in the last 24 hours',
  'jobs.batch_finished': 'Finished in this batch',
  'jobs.batch_show_all_label': 'View all batches',

  // Jobs page: path-trace sources, empty states, pagination
  'jobs.trace_source': 'Source: {source}',
  'jobs.trace_source_output_dir': 'output directory',
  'jobs.trace_source_run_code': 'snapshot rerun directory',
  'jobs.trace_source_guess': 'possible match',
  'jobs.trace_empty': 'No matching jobs',
  'jobs.filter_empty': 'No jobs match the filters',
  'jobs.empty': 'No jobs yet — the furnace is cold',
  'jobs.new_job': 'New job',
  'jobs.pagination_range': '{from}–{to} of {total}',
  'jobs.prev_page': 'Previous page',
  'jobs.next_page': 'Next page',

  // Job row: cancel confirmation dialog
  'jobs.cancel_dialog_title': 'Cancel job #{id}?',
  'jobs.keep_running': 'Keep running',
  'jobs.keep_queued': 'Keep queued',
  'jobs.cancel_job': 'Cancel job',
  'jobs.cancel_job_label': 'Cancel job #{id}',
  'jobs.cancel_running_body': "Running for {duration}. Confirming terminates the process; it cannot be resumed. The log and code snapshot are kept.",
  'jobs.cancel_queue_pos': 'Queued at position {pos}. ',
  'jobs.cancel_queued_body': 'Cancelling removes it from the queue.',

  // Job row: status announcement and detail line
  'jobs.status_announce': 'Job #{id} {name}: {status}',
  'jobs.queue_pos': '#{pos} in queue',
  'jobs.wait_pinned': 'waiting for GPU {list}',
  'jobs.wait_gpus': { one: 'waiting for {n} GPU', other: 'waiting for {n} GPUs' },
  'jobs.cancelling_detail': 'Ending the process…',
  'jobs.pruned_detail': "Logs and code snapshot pruned",
  'jobs.not_started': 'Not started',
  'jobs.waiting_output': 'Waiting for output…',
  'jobs.no_output': 'No output',

  // Job row: time column, markers and actions
  'jobs.waited': 'waited {duration}',
  'jobs.time_waited': 'Wait time',
  'jobs.time_running': 'Run time',
  'jobs.duration': 'Duration',
  'jobs.select_job_label': 'Select job #{id} {name}',
  'jobs.starred_label': 'Starred',
  'jobs.silent_for': 'silent {duration}',
  'jobs.remaining_title': "tqdm's estimate of the time left on the current progress bar; for epoch-based bars, the time left in the current epoch",
  'jobs.remaining': '{remaining} left',
  'jobs.ended_at': 'Ended',
  'jobs.copy_job': 'Copy job',
  'jobs.copy_job_label': 'Copy job #{id}',

  // Job row: desktop column headers
  'jobs.col_job': 'Job',
  'jobs.col_output': 'GPUs · latest output',
  'jobs.col_code': 'Code',

  // Queue controls: pause banner, pause/resume buttons. reason is server text, interpolated as-is
  'jobs.queue_paused': 'Queue paused: {reason}',
  'jobs.queue_paused_fallback': 'Not starting queued jobs',
  'jobs.resume_queue': 'Resume queue',
  'jobs.pause_queue': 'Pause queue',
  'jobs.pause_queue_title': 'Pause the queue; running jobs continue',
  'jobs.pause_failed': 'Failed to pause the queue: {message}. Use the pause button above to retry.',
} satisfies Catalog