import { t } from '../lib/i18n'
import { Button, Dialog, Kbd } from './ui'

const mac = /Mac|iPhone|iPad/.test(navigator.platform)
/** 提交快捷键里的修饰键：macOS 上是 ⌘，其他系统是 Ctrl */
export const modKey = mac ? '⌘' : 'Ctrl'

// 文案在组件渲染时经 t() 取，不做模块级常量表，免得冻结语言
function shortcutItems(): { keys: string[]; text: string }[] {
  return [
    { keys: ['N'], text: t('layout.new_job') },
    { keys: ['/'], text: t('ui.shortcut_search') },
    { keys: ['J', 'K'], text: t('ui.shortcut_move') },
    { keys: ['Enter'], text: t('ui.shortcut_open') },
    { keys: [modKey, 'Enter'], text: t('ui.shortcut_submit') },
    { keys: ['Esc'], text: t('ui.shortcut_esc') },
    { keys: ['?'], text: t('ui.shortcut_help') },
  ]
}

/** 快捷键一览。设置页和按 ? 打开的面板共用 */
export function ShortcutList() {
  return (
    <dl className="-my-1.5 divide-y divide-line text-ui">
      {shortcutItems().map((s) => (
        <div key={s.text} className="flex items-center gap-3 py-1.5">
          <dt className="flex w-24 shrink-0 items-center gap-1 text-fg-2">
            {s.keys.map((k) => (
              <Kbd key={k} always>
                {k}
              </Kbd>
            ))}
          </dt>
          <dd className="min-w-0 text-fg">{s.text}</dd>
        </div>
      ))}
    </dl>
  )
}

export function ShortcutDialog({ onClose }: { onClose: () => void }) {
  return (
    <Dialog title={t('ui.shortcuts_title')} onClose={onClose} footer={<Button size="sm" onClick={(e) => e.currentTarget.closest('dialog')?.close()}>{t('common.close')}</Button>}>
      <ShortcutList />
    </Dialog>
  )
}
