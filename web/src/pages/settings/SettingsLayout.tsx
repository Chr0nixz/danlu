import { useQuery } from '@tanstack/react-query'
import { Archive, Gauge, History, type LucideIcon, Server, ShieldCheck, SlidersHorizontal, Smartphone } from 'lucide-react'
import { useEffect, useRef, type ComponentType } from 'react'
import { Navigate, NavLink, useNavigate, useParams } from 'react-router'
import type { Settings as SettingsData } from '@danlu/core'
import { cx, ErrorBox, Select, Skeleton } from '../../components/ui'
import { api } from '../../lib/api'
import { t } from '../../lib/i18n'
import { Audit } from './Audit'
import { Mobile } from './Mobile'
import { Overview } from './Overview'
import { Preferences } from './Preferences'
import { Retention } from './Retention'
import { Scheduling } from './Scheduling'
import { Security } from './Security'

interface Group {
  id: string
  /** 目录键而不是文案：模块级常量表不能冻结语言 */
  labelKey: Parameters<typeof t>[0]
  icon: LucideIcon
  Component: ComponentType<{ st: SettingsData }>
}

/** 分组的顺序：常看的在前，改得少的在后。每组一个地址，可以直接链接过来 */
const groups: Group[] = [
  { id: 'overview', labelKey: 'settings.group_overview', icon: Server, Component: Overview },
  { id: 'scheduling', labelKey: 'settings.group_scheduling', icon: Gauge, Component: Scheduling },
  { id: 'retention', labelKey: 'settings.group_retention', icon: Archive, Component: Retention },
  { id: 'preferences', labelKey: 'settings.group_preferences', icon: SlidersHorizontal, Component: Preferences },
  { id: 'security', labelKey: 'settings.group_security', icon: ShieldCheck, Component: Security },
  { id: 'mobile', labelKey: 'settings.group_mobile', icon: Smartphone, Component: Mobile },
  { id: 'audit', labelKey: 'settings.group_audit', icon: History, Component: Audit },
]

export function SettingsLayout() {
  const { group: id } = useParams()
  const navigate = useNavigate()
  const s = useQuery({ queryKey: ['settings'], queryFn: api.settings, refetchInterval: 5000 })
  const group = groups.find((g) => g.id === id)

  // 切换分组后焦点移到内容区开头，读屏和键盘用户不用再从导航往下找；第一次打开页面时不动焦点
  const content = useRef<HTMLDivElement>(null)
  const first = useRef(true)
  useEffect(() => {
    if (first.current) {
      first.current = false
      return
    }
    content.current?.focus({ preventScroll: true })
    window.scrollTo({ top: 0 })
  }, [id])

  if (!group) return <Navigate to="/settings/overview" replace />

  return (
    <div className="mx-auto max-w-5xl">
      <h1 className="text-lg font-semibold">{t('settings.title')}</h1>
      <div className="mt-4 md:grid md:grid-cols-[176px_minmax(0,1fr)] md:items-start md:gap-6">
        <nav aria-label={t('settings.nav_aria')} className="max-md:hidden md:sticky md:top-16">
          <ul className="space-y-0.5">
            {groups.map((g) => (
              <li key={g.id}>
                <NavLink
                  to={`/settings/${g.id}`}
                  className={({ isActive }) =>
                    cx(
                      'flex h-8 items-center gap-2 rounded-md px-2.5 text-ui transition-colors duration-150 [&_svg]:size-4 [&_svg]:shrink-0',
                      isActive ? 'bg-hover font-medium text-fg' : 'text-fg-3 hover:bg-hover hover:text-fg',
                    )
                  }
                >
                  <g.icon aria-hidden />
                  {t(g.labelKey)}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        {/* 手机上一行放不下七个分组，用下拉选择 */}
        <Select aria-label={t('settings.nav_aria')} className="mb-4 md:hidden" value={group.id} onChange={(e) => navigate(`/settings/${e.target.value}`)}>
          {groups.map((g) => (
            <option key={g.id} value={g.id}>
              {t(g.labelKey)}
            </option>
          ))}
        </Select>
        <div ref={content} tabIndex={-1} aria-label={t(group.labelKey)} role="region" className="min-w-0 space-y-4 outline-none">
          {s.isLoading ? (
            <Skeleton className="h-56 w-full rounded-lg" />
          ) : s.error ? (
            <ErrorBox error={s.error} />
          ) : (
            <group.Component st={s.data!} />
          )}
        </div>
      </div>
    </div>
  )
}
