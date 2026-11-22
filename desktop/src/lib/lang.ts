import { useQuery, useQueryClient } from '@tanstack/react-query'
import { listen } from '@tauri-apps/api/event'
import { useEffect } from 'react'
import { getPrefs } from './backend'
import { applyPrefLang } from './i18n'

/** 主窗和托盘都用它：取偏好、监听 prefs-changed、把语言应用到 i18n 运行时 */
export function useLangSync(): void {
  const qc = useQueryClient()
  const prefs = useQuery({ queryKey: ['prefs'], queryFn: getPrefs })
  useEffect(() => {
    const off = listen('prefs-changed', () => qc.invalidateQueries({ queryKey: ['prefs'] }))
    return () => {
      off.then((u) => u())
    }
  }, [qc])
  const lang = prefs.data?.lang
  useEffect(() => {
    if (lang !== undefined) applyPrefLang(lang)
  }, [lang])
}