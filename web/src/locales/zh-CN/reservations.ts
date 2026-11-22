import type { Catalog } from '@danlu/core'

// 命名空间 reservations 的面板文案（源语言）。键以 "reservations." 开头。
// 覆盖 GPU 预约：首页列表、创建对话框、延长与释放确认。
// 选卡交互（按张数/指定卡号、步进器、至少选一张）与新建任务共用，收在 gpus.*。
export const reservations = {
  // 列表
  'reservations.heading': 'GPU 预约',
  'reservations.heading_hint': '预约收下的卡不分给丹炉任务',
  'reservations.label': '预约 #{id}',
  'reservations.extend': '延长 1 小时',
  'reservations.release': '释放',
  'reservations.copy_env': '复制环境变量',
  'reservations.copy_env_title': '复制 {text}（只限制 CUDA 程序；EGL/OpenGL 图形进程按自己的设备编号选卡）',

  // 状态行
  'reservations.waiting_cards': '等 GPU {gpus}',
  'reservations.claiming_progress': '收卡中 {held}/{want}',
  'reservations.waiting_state': '{what} · 已等 {duration}',
  'reservations.ready_manual': '已就绪 · 手动释放前一直保留',
  'reservations.ready_until': '已就绪 · 到 {time}（还剩 {remaining}）',

  // 释放确认
  'reservations.release_title': '释放预约 #{id}？',
  'reservations.release_cancel': '先不释放',
  'reservations.release_body_cards': 'GPU {gpus} 会交还给丹炉，排队的任务可能马上用上。',
  'reservations.release_body_empty': '这个预约还没收到卡，释放后不再等待。',
  'reservations.release_body_note': '卡上还有外部进程时，丹炉仍不会分配那张卡。',

  // 创建对话框
  'reservations.reserve_gpu': '预约 GPU',
  'reservations.submit': '预约',
  'reservations.intro': '给不在丹炉上跑的任务占卡。现在空闲的卡马上收下；不够的话，丹炉任务释放的卡先给这个预约、再给排队任务，凑齐后开始计时。',
  'reservations.intro_push': '卡全部到位和自动释放前 10 分钟会推送到手机。',
  'reservations.mode_label': '预约方式',
  'reservations.cards_summary': '共 {total} 张，现在空闲 {free} 张',
  'reservations.count_label': '预约张数',
  'reservations.count_suffix': '张卡，只挑没有任何进程的空闲卡',
  'reservations.short_warning': '现在空闲的卡不够，预约会先收下能收的卡，等丹炉任务结束再收齐。',
  'reservations.auto_release': '自动释放',
  'reservations.length_label': '自动释放时间',
  'reservations.length_1h': '1 小时',
  'reservations.length_3h': '3 小时',
  'reservations.length_8h': '8 小时',
  'reservations.length_custom': '自定义',
  'reservations.length_none': '不自动',
  'reservations.custom_hours_suffix': '小时后释放（从卡全部到位时算起）',
  'reservations.hours_range': '时长要在 10 分钟到 7 天（168 小时）之间',
  'reservations.none_hint': '卡会一直留着，用完记得回来释放。',
  'reservations.note_label': '用途',
  'reservations.note_placeholder': '可选，例如 手动调 DDP',
} satisfies Catalog
