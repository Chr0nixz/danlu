"""S0 失败样本：训练跑了几步之后抛一个普通的 Python 异常，调用栈有好几层。

    python fail_exception.py
"""

import time

from tqdm import tqdm


def load_batch(step: int) -> dict:
    if step == 5:
        return {"image": None}
    return {"image": [step] * 4}


def train_step(step: int) -> float:
    batch = load_batch(step)
    return sum(batch["image"]) / len(batch["image"])  # step 5 时 TypeError


for step in tqdm(range(20), ncols=80):
    train_step(step)
    time.sleep(0.5)
