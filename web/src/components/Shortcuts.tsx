import { Button, Dialog, Kbd } from './ui'

const mac = /Mac|iPhone|iPad/.test(navigator.platform)
/** 提交快捷键里的修饰键：macOS 上是 ⌘，其他系统是 Ctrl */
export const modKey = mac ? '⌘' : 'Ctrl'

const shortcuts: { keys: string[]; text: string }[] = [
  { keys: ['N'], text: '新建任务' },
  { keys: ['/'], text: '搜索任务，或粘贴产物路径反查任务' },
  { keys: ['J', 'K'], text: '在总览和任务页移到下一个、上一个任务' },
  { keys: ['Enter'], text: '打开选中的任务' },
  { keys: [modKey, 'Enter'], text: '在新建页提交任务' },
  { keys: ['Esc'], text: '清空搜索，关闭菜单和确认框' },
  { keys: ['?'], text: '打开这份快捷键一览' },
]

/** 快捷键一览。设置页和按 ? 打开的面板共用 */
export function ShortcutList() {
  return (
    <dl className="-my-1.5 divide-y divide-line text-ui">
      {shortcuts.map((s) => (
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
    <Dialog title="快捷键" onClose={onClose} footer={<Button size="sm" onClick={(e) => e.currentTarget.closest('dialog')?.close()}>关闭</Button>}>
      <ShortcutList />
    </Dialog>
  )
}
