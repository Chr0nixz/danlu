import type { Catalog } from '@danlu/core'

// 命名空间 ui 的英文文案。键集必须与 zh-CN/ui.ts 完全一致。
export const ui = {
  'ui.commit_dirty': '{sha} (uncommitted changes)',
  'ui.commit_dirty_sr': ', uncommitted changes',
  'ui.commit_view_code': '{title} — view code for this run',
  'ui.announce_separator': '; ',
  'ui.error_chunk_title': 'Page failed to load',
  'ui.error_title': 'This page ran into an error',
  'ui.error_chunk_body': 'Page files failed to load: Danlu may have just been upgraded, the service may be restarting, or the network dropped. Reloading fetches the latest page.',
  'ui.error_body': 'Retries automatically when new data arrives; you can also reload manually. Jobs are unaffected. If it keeps happening, note the error line below.',
  'ui.error_reload': 'Reload',
  'ui.back_home': 'Back to overview',
  'ui.shortcuts_title': 'Keyboard shortcuts',
  'ui.shortcut_search': 'Search jobs, or paste an artifact path to find its job',
  'ui.shortcut_move': 'Move to the next / previous job on Overview and Jobs',
  'ui.shortcut_open': 'Open the selected job',
  'ui.shortcut_submit': 'Submit the job on the New job page',
  'ui.shortcut_esc': 'Clear search; close menus and confirm dialogs',
  'ui.shortcut_help': 'Open this shortcut list',
  'ui.copy_https_only': 'The browser only allows copying over HTTPS',
  'ui.copy_denied': 'The browser denied the copy',
  'ui.copy_failed_sr': 'Copy failed: {why}',
  'ui.copy_failed_title': 'Copy failed: {why} — select the text manually',
  'ui.refresh_failed': 'Update failed: {error}. Showing the last data that loaded.',
} satisfies Catalog
