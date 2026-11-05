"""S0 失败样本：不断申请显存，直到 torch.OutOfMemoryError（CUDA 显存不足）。

    python fail_cuda_oom.py
    torchrun --nproc_per_node=2 fail_cuda_oom.py   # 多卡时各个 rank 的报错交织在一起，看归类和"跳到报错"
"""

import argparse
import os
import time

import torch
from tqdm import tqdm

parser = argparse.ArgumentParser()
parser.add_argument("--blocks", type=int, default=10_000, help="最多申请多少个 1 GiB 的块")
args = parser.parse_args()
local_rank = int(os.environ.get("LOCAL_RANK", 0))
torch.cuda.set_device(local_rank)
print(f"[fail_cuda_oom] rank {os.environ.get('RANK', 0)} 开始申请显存", flush=True)

blocks = []
for i in tqdm(range(args.blocks), ncols=80, disable=local_rank != 0):
    blocks.append(torch.empty(1024**3 // 4, dtype=torch.float32, device="cuda"))  # 每次 1 GiB
    time.sleep(0.2)
