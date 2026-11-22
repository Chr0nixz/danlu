import type { Catalog } from '@danlu/core'

// 命名空间 login 的面板文案（源语言）。键以 "login." 开头。
export const login = {
  'login.title': '登录丹炉',
  'login.subtitle': 'GPU 训练任务管理',
  'login.username': '用户名',
  'login.password': '密码',
  'login.totp': '两步验证码',
  'login.submit': '登录',
  'login.username_required': '请填写用户名',
  'login.password_required': '请填写密码',
  'login.totp_invalid': '请输入手机验证器 App 里的 6 位数字',
  'login.bookmark_hint': '建议把带安全入口的完整地址存成书签',
} satisfies Catalog
