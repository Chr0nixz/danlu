"""F1 真机验收：正常输出第一批，随后让 DataLoader 的下一个 worker 停住。"""

import threading

import torch
from torch.utils.data import DataLoader, Dataset


class StalledDataset(Dataset):
    def __len__(self):
        return 2

    def __getitem__(self, index):
        if index == 0:
            return torch.ones(32)
        # 给默认 30 分钟告警留足时间，忘记取消时也会自行结束。
        threading.Event().wait(timeout=45 * 60)
        raise RuntimeError("静默验收等待超时")


if __name__ == "__main__":
    for batch in DataLoader(StalledDataset(), num_workers=2):
        gpu_batch = batch.to("cuda:0")
        print(f"第一批已加载到 {gpu_batch.device}，下一批开始静默", flush=True)
