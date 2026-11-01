"""S0 失败样本：2 卡 DDP，rank 1 不参加 all_reduce，rank 0 等到 NCCL watchdog 超时。

要用 torchrun 跑，至少 2 卡。超时设为 60 秒，大约 1–2 分钟后失败：

    torchrun --nproc_per_node=2 fail_nccl.py

不同版本的 PyTorch 在超时后的表现不一样（watchdog 报错后整个进程被 abort，或者抛出异常），
两种都是要收集的样本。
"""

import datetime
import os
import time

import torch
import torch.distributed as dist

rank = int(os.environ["RANK"])
local_rank = int(os.environ["LOCAL_RANK"])
torch.cuda.set_device(local_rank)
dist.init_process_group("nccl", timeout=datetime.timedelta(seconds=60))
print(f"[fail_nccl] rank {rank} 初始化完成", flush=True)

t = torch.ones(1, device="cuda")
dist.all_reduce(t)  # 先正常通信一次，确认 NCCL 本身能用
torch.cuda.synchronize()
print(f"[fail_nccl] rank {rank} 第一次 all_reduce 正常", flush=True)

if rank == 0:
    dist.all_reduce(t)  # rank 1 不来，这里一直等到超时
    torch.cuda.synchronize()
else:
    time.sleep(600)
