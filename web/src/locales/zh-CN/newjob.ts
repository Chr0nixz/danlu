import type { Catalog } from '@danlu/core'

// 命名空间 newjob 的面板文案（源语言）。键以 "newjob." 开头。
// 覆盖新建/复制/重跑任务表单：标题、草稿恢复、快照重跑与代码比较、各字段与校验提示。
// 选卡交互（按张数/指定卡号、步进器、至少选一张）与预约对话框共用，收在 gpus.*。
export const newjob = {
  // 标题与副标题
  'newjob.title_new': '新建任务',
  'newjob.title_rerun': '重跑任务 #{id}',
  'newjob.title_copy': '复制任务 #{id}',
  'newjob.subtitle_new': '提交后按顺序排队，有足够的空卡就自动开始。',
  'newjob.subtitle_rerun': '设置已从「{name}」复制过来，可以先改参数再提交。',
  'newjob.subtitle_copy': '设置已从「{name}」复制过来，修改后提交为独立的新任务。',
  'newjob.title_edit': '修改排队任务 #{id}',
  'newjob.subtitle_edit': '「{name}」还在排队。保存后原地更新，编号和排队位置都不变。',
  'newjob.save_edit': '保存修改',
  'newjob.edit_closed': '任务 #{id} 已经不在排队中，不能再修改。',
  'newjob.edit_view_job': '查看任务',
  'newjob.source_failed': '原任务失败：',

  // 草稿恢复
  'newjob.draft_restored': '已恢复上次没提交的内容。',
  'newjob.draft_reset': '重新填写',

  // 代码版本（快照重跑）
  'newjob.code_version': '代码版本',
  'newjob.code_hint_unavailable': '快照已不可用。请选择现在的工作目录再提交。',
  'newjob.code_hint_snapshot': '在独立目录恢复代码。未收进快照的文件可能来自原目录，命令中写死的绝对路径也仍指向原处。conda 使用当前环境。',
  'newjob.code_hint_pruned': '源任务的快照已清理。',
  'newjob.code_hint_not_ready': '源任务的快照还不可用。',
  'newjob.code_hint_default': '可改用原任务的快照，再调整这次运行的参数。',
  'newjob.code_current': '现在的工作目录',
  'newjob.code_snapshot': '任务 #{id} 的快照',
  'newjob.code_fill_workdir': '先填工作目录',
  'newjob.code_reading': '正在读取…',
  'newjob.code_not_git': '不是 git 仓库',
  'newjob.snap_pruned': '已清理',
  'newjob.snap_not_ready': '还不可用',
  'newjob.snap_non_git': '非 git，已存快照',
  'newjob.dirty_mark': ' · 有未提交改动',

  // 重跑时的代码比较提示（{code} 链接在调用点插入）
  'newjob.code_link': '原任务的代码快照',
  'newjob.code_view_after': '可以查看和下载。',
  'newjob.not_git_before': '工作目录不是 git 仓库，没法比较代码。重跑会使用目录里现在的文件；',
  'newjob.code_same': '工作目录现在的代码和原任务相同：commit {commit}，都没有未提交的改动。',
  'newjob.diff_intro_before': '重跑会使用工作目录',
  'newjob.diff_now': '现在',
  'newjob.diff_intro_after': '的代码，不是原任务快照里的那份：',
  'newjob.diff_commit': '当前 commit 是 {current}，原任务是 {original}',
  'newjob.diff_dirty': '工作目录有未提交的改动，看不出和原任务运行时是否一样',
  'newjob.diff_source_dirty': '原任务运行时有未提交的改动，现在工作目录是干净的',

  // 工作目录
  'newjob.workdir': '工作目录',
  'newjob.workdir_source': '原工作目录',
  'newjob.workdir_hint': '服务器上的绝对路径，输入或按 ↓ 列出子目录。启动时会自动保存一份代码快照。',
  'newjob.workdir_hint_snapshot': '从这里补齐数据和权重。命令在恢复后的目录中执行。',
  'newjob.workdir_placeholder': '例如 /home/you/projects/vit',
  'newjob.subdirs': '子目录',
  'newjob.recent': '最近用过',

  // 命令
  'newjob.cmd': '命令',
  'newjob.cmd_hint': '在工作目录下用 bash 执行，可以写多行。torchrun 不用写 --master_port，会自动分配。',
  'newjob.cmd_hint_snapshot': '用 bash 在恢复后的目录中执行，可以写多行。torchrun 的端口会自动分配。',
  'newjob.cmd_picks_gpus': '命令里自己选了卡（{what}），会盖掉丹炉分配的卡，任务可能跑到别的任务或预约的卡上。去掉它，程序会按丹炉设好的 CUDA_VISIBLE_DEVICES 用卡。',
  'newjob.cmd_picks_gpus_strict': '命令里自己选了卡（{what}）。严格限卡已开启：程序只看得到分到的卡，编号从 0 开始，按这个编号写（比如 0,1）才对，写物理编号会找不到卡。',
  'newjob.cmd_placeholder': '例如 torchrun --nproc_per_node 2 train.py --lr 3e-4',

  // conda 环境
  'newjob.conda': 'conda 环境',
  'newjob.conda_hint': '任务启动前自动激活。',
  'newjob.conda_none': '不使用 conda',

  // GPU 选择与占卡提示
  'newjob.gpu_hint': '共 {total} 张，现在空闲 {free} 张。整卡独占。',
  'newjob.gpu_mode_label': 'GPU 分配方式',
  'newjob.gpu_count_label': 'GPU 张数',
  'newjob.gpu_zero': '不使用 GPU',
  'newjob.gpu_count_suffix': '张卡，自动挑空闲的',
  'newjob.reserved_sep': '，',
  'newjob.pinned_reserved_item': 'GPU {gpu} 已被预约 #{id}',
  'newjob.pinned_reserved_warning': '{list}，释放前丹炉不会分配它。任务会一直排在队首、挡住后面所有任务，直到预约释放。',
  'newjob.pinned_external_warning': 'GPU {gpus} 被外部进程或别的任务的进程占用，丹炉不会分配它。任务会一直排在队首、挡住后面所有任务，直到那个进程退出。',
  'newjob.pinned_unknown_warning': 'GPU {gpus} 有进程列表无法解释的显存占用，丹炉不会分配它。任务会排在队首，直到显存释放或状态恢复。',
  'newjob.will_queue': '现在空闲的卡不够，提交后会排队，等卡空出来自动开始。',

  // 任务名、备注、手机推送
  'newjob.name': '任务名',
  'newjob.name_hint': '留空则用目录名加时间。',
  'newjob.name_placeholder': '例如 vit-lr3e-4',
  'newjob.memo': '备注',
  'newjob.memo_hint': '可选，最多 500 字。',
  'newjob.notify': '手机推送',
  'newjob.notify_hint': '失败仍会按服务器的推送设置提醒。',
  'newjob.notify_done': '结束时推送',
  'newjob.notify_push_off': '这台服务器还没开启手机推送，勾上也收不到。',
  'newjob.notify_push_settings': '推送设置',

  // 环境变量
  'newjob.env': '环境变量',
  'newjob.env_hint': '可选，每行一个 KEY=VALUE。',
  'newjob.env_placeholder': '例如 WANDB_PROJECT=vit\nOMP_NUM_THREADS=8',
  'newjob.env_format': '格式应为 KEY=VALUE：{line}',
  'newjob.env_name_invalid': '变量名只能用字母、数字和下划线，且不能以数字开头：{name}',

  // Bash 环境
  'newjob.bashrc': 'Bash 环境',
  'newjob.bashrc_hint': '缺失或加载超时会启动失败',
  'newjob.bashrc_load': '加载服务器 ~/.bashrc 导出的变量',

  // 校验与提交
  'newjob.workdir_required': '请填写工作目录',
  'newjob.cmd_required': '请填写要运行的命令',
  'newjob.memo_max': '备注最多 500 字',
  'newjob.back': '返回',
  'newjob.submit': '提交任务',
} satisfies Catalog
