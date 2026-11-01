"""S0 用的假训练：多卡 DDP，占一点显存，带 tqdm 进度条，跑够时间就正常退出。

用来验证排队、重启后接管、取消（所有 worker 退出、显存释放）和日志显示。
单卡直接 python 跑，多卡用 torchrun；master 端口由 danlu 注入的 PET_MASTER_PORT 决定，不用自己填。

    python ddp_dummy.py --minutes 30
    torchrun --nproc_per_node=2 ddp_dummy.py --minutes 30
"""

import argparse
import os
import time

import torch
import torch.distributed as dist
from tqdm import tqdm


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--minutes", type=float, default=30.0, help="跑多久")
    ap.add_argument("--mem-gb", type=float, default=2.0, help="每张卡占多少显存")
    ap.add_argument("--lr", type=float, default=3e-4, help="没有实际作用，用来测重跑改参数和两次任务对比")
    args = ap.parse_args()

    rank = int(os.environ.get("RANK", 0))
    local_rank = int(os.environ.get("LOCAL_RANK", 0))
    world = int(os.environ.get("WORLD_SIZE", 1))
    if world > 1:
        dist.init_process_group("nccl")
    torch.cuda.set_device(local_rank)

    if rank == 0:
        print(f"[ddp_dummy] world_size={world} lr={args.lr} 显存目标={args.mem_gb}GB/卡", flush=True)
        print(f"[ddp_dummy] CUDA_VISIBLE_DEVICES={os.environ.get('CUDA_VISIBLE_DEVICES')} "
              f"PET_MASTER_PORT={os.environ.get('PET_MASTER_PORT')} "
              f"DANLU_JOB_ID={os.environ.get('DANLU_JOB_ID')}", flush=True)
        # 假的 tracker 链接，用来验证面板能认出 W&B 链接（不会真的联网）
        print("wandb: 🚀 View run at https://wandb.ai/danlu-s0/smoke/runs/s0test01", flush=True)

    # 占显存：一块大 tensor 加一对用来做矩阵乘的方阵
    hold = torch.empty(int(args.mem_gb * 1024**3 // 4), dtype=torch.float32, device="cuda")
    hold.fill_(1.0)
    a = torch.randn(4096, 4096, device="cuda")
    b = torch.randn(4096, 4096, device="cuda")

    deadline = time.time() + args.minutes * 60
    steps = max(1, int(args.minutes * 60))
    bar = tqdm(range(steps), disable=rank != 0, ncols=80)
    for i in bar:
        for _ in range(20):
            a = (a @ b).div_(100.0)  # 让 GPU 真的有利用率
        if world > 1:
            t = torch.tensor([float(i)], device="cuda")
            dist.all_reduce(t)
        torch.cuda.synchronize()
        if rank == 0:
            bar.set_postfix(loss=f"{1.0 / (i + 1):.4f}")
        if i % 30 == 0 and rank == 0:
            print(f"[ddp_dummy] step {i} 还剩 {max(0, deadline - time.time()):.0f} 秒", flush=True)
        if time.time() > deadline:
            break
        time.sleep(1)

    if world > 1:
        dist.destroy_process_group()
    if rank == 0:
        print("[ddp_dummy] 正常结束", flush=True)


if __name__ == "__main__":
    main()
