import type { Catalog } from '@danlu/core'

// 命名空间 login 的英文文案。键集必须与 zh-CN/login.ts 完全一致。
export const login = {
  'login.title': 'Sign in to Danlu',
  'login.subtitle': 'GPU training job management',
  'login.username': 'Username',
  'login.password': 'Password',
  'login.totp': 'Two-factor code',
  'login.submit': 'Sign in',
  'login.username_required': 'Enter your username',
  'login.password_required': 'Enter your password',
  'login.totp_invalid': 'Enter the 6-digit code from your authenticator app',
  'login.bookmark_hint': 'Bookmark the full URL with the secret entrance',
} satisfies Catalog
