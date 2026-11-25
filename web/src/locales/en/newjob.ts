import type { Catalog } from '@danlu/core'

// 命名空间 newjob 的英文文案。键集必须与 zh-CN/newjob.ts 完全一致。
export const newjob = {
  // Title and subtitle
  'newjob.title_new': 'New job',
  'newjob.title_rerun': 'Rerun job #{id}',
  'newjob.title_copy': 'Copy job #{id}',
  'newjob.subtitle_new': 'Submitted jobs queue in order and start automatically once enough cards are free.',
  'newjob.subtitle_rerun': 'Settings copied from "{name}"; adjust parameters before submitting.',
  'newjob.subtitle_copy': 'Settings copied from "{name}"; edit and submit as a separate new job.',
  'newjob.title_edit': 'Edit queued job #{id}',
  'newjob.subtitle_edit': '"{name}" is still queued. Saving updates it in place; its id and queue position stay the same.',
  'newjob.save_edit': 'Save changes',
  'newjob.edit_closed': "Job #{id} is no longer queued and can't be edited.",
  'newjob.edit_view_job': 'View job',
  'newjob.source_failed': 'Source job failed: ',

  // Draft restore
  'newjob.draft_restored': 'Restored your unsubmitted draft.',
  'newjob.draft_reset': 'Start over',

  // Code version (snapshot rerun)
  'newjob.code_version': 'Code version',
  'newjob.code_hint_unavailable': 'The snapshot is no longer available. Select the current working directory before submitting.',
  'newjob.code_hint_snapshot': 'Code is restored into a separate directory. Files not captured in the snapshot may come from the original directory, and absolute paths hardcoded in the command still point there. conda uses the current environment.',
  'newjob.code_hint_pruned': "The source job's snapshot has been pruned.",
  'newjob.code_hint_not_ready': "The source job's snapshot is not available yet.",
  'newjob.code_hint_default': "You can switch to the source job's snapshot and adjust this run's parameters.",
  'newjob.code_current': 'Current working directory',
  'newjob.code_snapshot': 'Snapshot of job #{id}',
  'newjob.code_fill_workdir': 'Fill in the working directory first',
  'newjob.code_reading': 'Reading…',
  'newjob.code_not_git': 'Not a git repository',
  'newjob.snap_pruned': 'Pruned',
  'newjob.snap_not_ready': 'Not available yet',
  'newjob.snap_non_git': 'Not git; snapshot saved',
  'newjob.dirty_mark': ' · uncommitted changes',

  // Code comparison notices on rerun (the {code} link is inserted at the call site)
  'newjob.code_link': "the source job's code snapshot",
  'newjob.code_view_after': ' can be viewed and downloaded.',
  'newjob.not_git_before': 'The working directory is not a git repository, so the code cannot be compared. The rerun will use the files currently in the directory; ',
  'newjob.code_same': "The working directory's code matches the source job: commit {commit}, neither has uncommitted changes.",
  'newjob.diff_intro_before': "The rerun will use the working directory's ",
  'newjob.diff_now': 'current',
  'newjob.diff_intro_after': " code, not the one in the source job's snapshot:",
  'newjob.diff_commit': 'Current commit is {current}; the source job ran {original}',
  'newjob.diff_dirty': 'The working directory has uncommitted changes; cannot tell whether it matches what the source job ran',
  'newjob.diff_source_dirty': 'The source job ran with uncommitted changes; the working directory is now clean',

  // Working directory
  'newjob.workdir': 'Working directory',
  'newjob.workdir_source': 'Original working directory',
  'newjob.workdir_hint': 'Absolute path on the server; type or press ↓ to list subdirectories. A code snapshot is saved automatically at launch.',
  'newjob.workdir_hint_snapshot': 'Data and weights are filled in from here. The command runs in the restored directory.',
  'newjob.workdir_placeholder': 'e.g. /home/you/projects/vit',
  'newjob.subdirs': 'Subdirectories',
  'newjob.recent': 'Recently used',

  // Command
  'newjob.cmd': 'Command',
  'newjob.cmd_hint': 'Runs with bash in the working directory; multiple lines allowed. torchrun needs no --master_port; it is assigned automatically.',
  'newjob.cmd_hint_snapshot': 'Runs with bash in the restored directory; multiple lines allowed. The torchrun port is assigned automatically.',
  'newjob.cmd_picks_gpus': 'The command picks GPUs itself ({what}), overriding the GPUs danlu assigns; the job may land on cards used by other jobs or reservations. Remove it and the program uses the CUDA_VISIBLE_DEVICES danlu sets.',
  'newjob.cmd_picks_gpus_strict': 'The command picks GPUs itself ({what}). Strict GPU isolation is on: the program only sees its assigned GPUs, numbered from 0, so write them that way (e.g. 0,1); physical numbers will find no GPU.',
  'newjob.cmd_placeholder': 'e.g. torchrun --nproc_per_node 2 train.py --lr 3e-4',

  // conda environment
  'newjob.conda': 'conda environment',
  'newjob.conda_hint': 'Activated automatically before the job starts.',
  'newjob.conda_none': 'No conda',

  // GPU selection and held-card warnings
  'newjob.gpu_hint': { one: "{total} GPU total, {free} free now. Whole GPUs only.", other: "{total} GPUs total, {free} free now. Whole GPUs only." },
  'newjob.gpu_mode_label': 'GPU allocation',
  'newjob.gpu_count_label': 'Number of GPUs',
  'newjob.gpu_zero': 'No GPU',
  'newjob.gpu_count_suffix': { one: "GPU, chosen from free ones", other: "GPUs, chosen from free ones" },
  'newjob.reserved_sep': ', ',
  'newjob.pinned_reserved_item': 'GPU {gpu} is held by reservation #{id}',
  'newjob.pinned_reserved_warning': '{list}. Danlu will not allocate reserved cards; the job would stay at the head of the queue, blocking every job behind it, until the reservation is released.',
  'newjob.pinned_external_warning': "External processes or another job's processes hold GPU {gpus}; Danlu will not allocate such cards. The job would stay at the head of the queue, blocking every job behind it, until those processes exit.",
  'newjob.pinned_unknown_warning': 'Unexplained memory usage on GPU {gpus} (not accounted for by the process list); Danlu will not allocate such cards. The job would wait at the head of the queue until the memory is freed or the state recovers.',
  'newjob.will_queue': 'Not enough free cards right now; the job will queue after submission and start automatically when cards free up.',

  // Job name, memo, phone push
  'newjob.name': 'Job name',
  'newjob.name_hint': 'Leave empty to use the directory name plus time.',
  'newjob.name_placeholder': 'e.g. vit-lr3e-4',
  'newjob.memo': 'Memo',
  'newjob.memo_hint': 'Optional, up to 500 characters.',
  'newjob.notify': 'Phone push',
  'newjob.notify_hint': "Failures still notify per the server's push settings.",
  'newjob.notify_done': "Completion alert",
  'newjob.notify_push_off': "Phone push isn't set up on this server, so this alert won't arrive.",
  'newjob.notify_push_settings': 'Push settings',

  // Environment variables
  'newjob.env': 'Environment variables',
  'newjob.env_hint': 'Optional, one KEY=VALUE per line.',
  'newjob.env_placeholder': 'e.g. WANDB_PROJECT=vit\nOMP_NUM_THREADS=8',
  'newjob.env_format': 'Expected KEY=VALUE: {line}',
  'newjob.env_name_invalid': 'Variable names may only use letters, digits and underscores, and cannot start with a digit: {name}',

  // Bash environment
  'newjob.bashrc': 'Bash environment',
  'newjob.bashrc_hint': "If missing or too slow to load, the job fails at startup",
  'newjob.bashrc_load': "Load variables exported by the server's ~/.bashrc",

  // Validation and submit
  'newjob.workdir_required': 'Enter the working directory',
  'newjob.cmd_required': 'Enter the command to run',
  'newjob.memo_max': 'Memo is limited to 500 characters',
  'newjob.back': 'Back',
  'newjob.submit': 'Submit job',
} satisfies Catalog
