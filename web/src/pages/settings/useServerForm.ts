import { useMutation } from '@tanstack/react-query'
import { useEffect, useId, useState } from 'react'
import { cx } from '../../components/ui'

type Key<F> = keyof F & string
export type FormErrors<F> = Partial<Record<Key<F>, string>>

/**
 * 设置页上"改几项、一起保存、立即生效"的表单。
 *
 * - 表单里存的是输入框里的原样内容；有没有改动按内容比，没动过的项不提交，免得服务器上的旧值被悄悄改掉。
 * - 没在编辑时跟上服务器上的值（例如刚在另一台设备或服务器上改过）；正在编辑时不打断。
 * - 保存成功后以服务器返回的值为准。
 */
export function useServerForm<F extends Record<string, unknown>, R>(opt: {
  /** 从服务器数据换算出的表单内容 */
  server: F
  /** 提交改了的项；changed 标出哪些项和保存时的内容不同 */
  save: (form: F, changed: Record<Key<F>, boolean>) => Promise<R>
  /** 从保存结果换算出表单内容 */
  fromResult: (r: R) => F
  onSaved?: (r: R) => void
  onSettled?: () => void
}) {
  const [base, setBase] = useState(opt.server)
  const [form, setForm] = useState(opt.server)
  const [errors, setErrors] = useState<FormErrors<F>>({})
  const uid = useId()
  const keys = Object.keys(base) as Key<F>[]
  const changedFrom = (f: F) => Object.fromEntries(keys.map((k) => [k, JSON.stringify(f[k]) !== JSON.stringify(base[k])])) as Record<Key<F>, boolean>
  const changed = changedFrom(form)
  const dirty = keys.some((k) => changed[k])

  const m = useMutation({
    // 默认提交此刻的表单。传快照时提交快照：数据保留先预览、确认后才保存，保存的必须是预览过的那一份
    mutationFn: (snapshot: F | void) => {
      const f = (snapshot as F | undefined) ?? form
      return opt.save(f, changedFrom(f))
    },
    onSuccess: (r) => {
      const f = opt.fromResult(r)
      setBase(f)
      setForm(f)
      setErrors({})
      opt.onSaved?.(r)
    },
    onSettled: () => opt.onSettled?.(),
  })

  const serverKey = JSON.stringify(opt.server)
  useEffect(() => {
    if (!dirty) {
      setBase(opt.server)
      setForm(opt.server)
    }
    // 只在服务器上的值变了时同步
  }, [serverKey]) // eslint-disable-line react-hooks/exhaustive-deps

  const edit = <K extends Key<F>>(k: K, v: F[K]) => {
    setForm((f) => ({ ...f, [k]: v }))
    if (errors[k]) setErrors((e) => ({ ...e, [k]: undefined }))
    if (m.isSuccess || m.isError) m.reset()
  }
  const id = (k: Key<F>) => `${uid}-${k}`
  const errId = (k: Key<F>) => `${id(k)}-err`
  const describe = (k: Key<F>, hint = false) => cx(hint && `${id(k)}-hint`, errors[k] && errId(k)) || undefined
  const field = (k: Key<F>, hint = false) => ({ id: id(k), 'aria-invalid': !!errors[k] || undefined, 'aria-describedby': describe(k, hint) })
  /** 显示校验结果；有错时焦点移到第一个出错的字段，返回 false */
  const check = (found: FormErrors<F>, order: Key<F>[] = keys) => {
    setErrors(found)
    const first = order.find((k) => found[k])
    if (first) {
      document.getElementById(id(first))?.focus()
      return false
    }
    return true
  }
  /** 放弃没保存的改动 */
  const discard = () => {
    setForm(base)
    setErrors({})
    m.reset()
  }

  return { form, base, changed, dirty, errors, edit, setForm, m, id, errId, describe, field, check, discard, saved: m.isSuccess && !dirty }
}
