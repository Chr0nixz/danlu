// 只存在这台电脑上的界面状态（localStorage，主窗口和托盘浮窗同源共用）。
// 已读状态和偏好在 Rust 层（托盘和通知要用），这里只放界面自己用的。

const MAP_KEY = 'danlu.project-map'

export interface Placement {
  workdir: string
  conda_env: string
}

type ProjectMap = Record<string, Record<string, Placement>>

function readMap(): ProjectMap {
  try {
    return JSON.parse(localStorage.getItem(MAP_KEY) ?? '{}') as ProjectMap
  } catch {
    return {}
  }
}

/** "项目 × 服务器 → 目录、环境"：复制到另一台时第一次手动选，之后自动填好 */
export function placementFor(project: string, server: string): Placement | undefined {
  return readMap()[project]?.[server]
}

export function rememberPlacement(project: string, server: string, p: Placement) {
  const m = readMap()
  m[project] = { ...m[project], [server]: p }
  localStorage.setItem(MAP_KEY, JSON.stringify(m))
}
