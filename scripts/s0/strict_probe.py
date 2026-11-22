#!/usr/bin/env python3
"""严格模式探测的被测程序：由 strict-probe.sh 调用，只用标准库，直接调 libcuda 和 libEGL。

不依赖 torch：探测的是驱动层的行为（CUDA 看到几张卡、怎么编号、EGL 能在哪些卡上初始化），
和训练框架无关。每个子命令打印人看的几行，再打印一行 "RESULT {json}" 供最后汇总。

  strict_probe.py cuda <标签> [ctx]        枚举 CUDA 设备；带 ctx 时在每张卡上建上下文并分配 1 MiB
  strict_probe.py egl <标签> [init]        枚举 EGL 设备；带 init 时逐个 eglInitialize
  strict_probe.py open <标签> <放行节点,..> <被拦节点,..>   直接打开设备文件
  strict_probe.py summary <结果文件> <放行总线号,..> <被拦总线号,..>
"""
import ctypes
import errno
import json
import os
import sys


def norm(bus):
    """总线号去掉域号再比："00000000:3B:00.0"、"0000:3b:00.0" 都成 "3b:00.0"（和 gpu-procs.sh 一致）"""
    b = bus.strip().lower()
    return b.split(":", 1)[1] if b.count(":") == 2 else b


def drm_bus(path):
    try:
        name = os.path.basename(path)
        return norm(os.path.basename(os.path.realpath("/sys/class/drm/%s/device" % name)))
    except OSError:
        return None


def emit(result):
    print("RESULT " + json.dumps(result, ensure_ascii=False), flush=True)


class Cuda:
    def __init__(self):
        self.lib = ctypes.CDLL("libcuda.so.1")

    def err(self, code):
        s = ctypes.c_char_p()
        if self.lib.cuGetErrorName(code, ctypes.byref(s)) == 0 and s.value:
            return "%s(%d)" % (s.value.decode(), code)
        return str(code)

    def enumerate(self):
        """返回 (错误, [{ordinal, bus, dev}])；cuInit 失败时第一个值是错误文字"""
        r = self.lib.cuInit(0)
        if r != 0:
            return "cuInit " + self.err(r), []
        n = ctypes.c_int()
        r = self.lib.cuDeviceGetCount(ctypes.byref(n))
        if r != 0:
            return "cuDeviceGetCount " + self.err(r), []
        out = []
        for i in range(n.value):
            d = {"ordinal": i}
            dev = ctypes.c_int()
            r = self.lib.cuDeviceGet(ctypes.byref(dev), i)
            if r != 0:
                d["error"] = "cuDeviceGet " + self.err(r)
            else:
                d["dev"] = dev.value
                buf = ctypes.create_string_buffer(64)
                if self.lib.cuDeviceGetPCIBusId(buf, 64, dev) == 0:
                    d["bus"] = norm(buf.value.decode())
            out.append(d)
        return None, out

    def try_context(self, dev):
        """建主上下文并分配、释放 1 MiB 显存，返回 "ok" 或错误名"""
        ctx = ctypes.c_void_p()
        r = self.lib.cuDevicePrimaryCtxRetain(ctypes.byref(ctx), ctypes.c_int(dev))
        if r != 0:
            return "cuDevicePrimaryCtxRetain " + self.err(r)
        try:
            r = self.lib.cuCtxSetCurrent(ctx)
            if r != 0:
                return "cuCtxSetCurrent " + self.err(r)
            ptr = ctypes.c_uint64()
            r = self.lib.cuMemAlloc_v2(ctypes.byref(ptr), ctypes.c_size_t(1 << 20))
            if r != 0:
                return "cuMemAlloc " + self.err(r)
            self.lib.cuMemFree_v2(ptr)
            return "ok"
        finally:
            release = getattr(self.lib, "cuDevicePrimaryCtxRelease_v2", None) or self.lib.cuDevicePrimaryCtxRelease
            release(ctypes.c_int(dev))


def cmd_cuda(label, make_ctx):
    cvd = os.environ.get("CUDA_VISIBLE_DEVICES")
    res = {"kind": "cuda", "label": label, "cvd": cvd, "devices": []}
    print("CUDA_VISIBLE_DEVICES=%s" % ("（未设置）" if cvd is None else cvd))
    try:
        cu = Cuda()
    except OSError as e:
        res["error"] = "加载 libcuda.so.1 失败：%s" % e
        print("  " + res["error"])
        emit(res)
        return
    error, devices = cu.enumerate()
    if error:
        res["error"] = error
        print("  " + error)
        emit(res)
        return
    print("  CUDA 看到 %d 个设备" % len(devices))
    for d in devices:
        if make_ctx and "dev" in d:
            d["ctx"] = cu.try_context(d["dev"])
        d.pop("dev", None)
        line = "  设备 %d：总线 %s" % (d["ordinal"], d.get("bus", "?"))
        if "error" in d:
            line += "，" + d["error"]
        if "ctx" in d:
            line += "，建上下文/分配显存：" + d["ctx"]
        print(line)
    res["devices"] = devices
    emit(res)


EGL_EXTENSIONS = 0x3055
EGL_DRM_DEVICE_FILE_EXT = 0x3233
EGL_DRM_RENDER_NODE_FILE_EXT = 0x3377
EGL_CUDA_DEVICE_NV = 0x323A
EGL_PLATFORM_DEVICE_EXT = 0x313F


def cmd_egl(label, do_init):
    res = {"kind": "egl", "label": label, "devices": []}
    try:
        lib = ctypes.CDLL("libEGL.so.1")
    except OSError as e:
        res["error"] = "加载 libEGL.so.1 失败：%s" % e
        print("  " + res["error"])
        emit(res)
        return
    lib.eglGetProcAddress.restype = ctypes.c_void_p
    lib.eglGetProcAddress.argtypes = [ctypes.c_char_p]

    def fn(name, restype, *argtypes):
        addr = lib.eglGetProcAddress(name.encode())
        return ctypes.CFUNCTYPE(restype, *argtypes)(addr) if addr else None

    boolean = ctypes.c_uint
    query = fn("eglQueryDevicesEXT", boolean, ctypes.c_int, ctypes.POINTER(ctypes.c_void_p), ctypes.POINTER(ctypes.c_int))
    qstr = fn("eglQueryDeviceStringEXT", ctypes.c_char_p, ctypes.c_void_p, ctypes.c_int)
    qattr = fn("eglQueryDeviceAttribEXT", boolean, ctypes.c_void_p, ctypes.c_int, ctypes.POINTER(ctypes.c_ssize_t))
    getdisp = fn("eglGetPlatformDisplayEXT", ctypes.c_void_p, ctypes.c_uint, ctypes.c_void_p, ctypes.POINTER(ctypes.c_int))
    lib.eglInitialize.argtypes = [ctypes.c_void_p, ctypes.POINTER(ctypes.c_int), ctypes.POINTER(ctypes.c_int)]
    lib.eglInitialize.restype = boolean
    lib.eglTerminate.argtypes = [ctypes.c_void_p]
    lib.eglTerminate.restype = boolean
    lib.eglGetError.restype = ctypes.c_int
    if not query:
        res["error"] = "没有 eglQueryDevicesEXT"
        print("  " + res["error"])
        emit(res)
        return
    n = ctypes.c_int()
    if not query(0, None, ctypes.byref(n)):
        res["error"] = "eglQueryDevicesEXT 0x%x" % lib.eglGetError()
        print("  " + res["error"])
        emit(res)
        return
    devs = (ctypes.c_void_p * max(n.value, 1))()
    query(n.value, devs, ctypes.byref(n))

    # EGL_CUDA_DEVICE_NV 给的是这个进程里的 CUDA 编号，换算成总线号要同一进程里枚举 CUDA
    cuda_bus = {}
    try:
        _, cuda_devs = Cuda().enumerate()
        cuda_bus = {d["ordinal"]: d.get("bus") for d in cuda_devs}
    except OSError:
        pass

    print("  EGL 列出 %d 个设备" % n.value)
    for i in range(n.value):
        dev = devs[i]
        d = {"index": i}
        exts = (qstr(dev, EGL_EXTENSIONS) or b"").decode() if qstr else ""
        if "EGL_MESA_device_software" in exts:
            d["software"] = True
        if "EGL_EXT_device_drm" in exts:
            f = qstr(dev, EGL_DRM_DEVICE_FILE_EXT)
            if f:
                d["drm"] = f.decode()
                d["bus"] = drm_bus(d["drm"])
        if "EGL_NV_device_cuda" in exts and qattr:
            v = ctypes.c_ssize_t()
            if qattr(dev, EGL_CUDA_DEVICE_NV, ctypes.byref(v)):
                d["cuda"] = v.value
                if not d.get("bus") and cuda_bus.get(v.value):
                    d["bus"] = cuda_bus[v.value]
        if do_init and getdisp:
            disp = getdisp(EGL_PLATFORM_DEVICE_EXT, dev, None)
            if not disp:
                d["init"] = "eglGetPlatformDisplayEXT 0x%x" % lib.eglGetError()
            else:
                major, minor = ctypes.c_int(), ctypes.c_int()
                if lib.eglInitialize(disp, ctypes.byref(major), ctypes.byref(minor)):
                    d["init"] = "ok"
                    lib.eglTerminate(disp)
                else:
                    d["init"] = "eglInitialize 0x%x" % lib.eglGetError()
        line = "  EGL 设备 %d：" % i
        line += "软件渲染" if d.get("software") else "总线 %s" % (d.get("bus") or "?")
        if "drm" in d:
            line += "，%s" % d["drm"]
        if "cuda" in d:
            line += "，CUDA 编号 %d" % d["cuda"]
        if "init" in d:
            line += "，初始化：" + d["init"]
        print(line)
        res["devices"].append(d)
    emit(res)


def cmd_open(label, allowed, denied):
    res = {"kind": "open", "label": label, "allowed": {}, "denied": {}}
    for group, paths in (("allowed", allowed), ("denied", denied)):
        for p in paths:
            try:
                os.close(os.open(p, os.O_RDWR))
                res[group][p] = "ok"
            except OSError as e:
                res[group][p] = errno.errorcode.get(e.errno, str(e.errno))
            print("  打开 %s（%s）：%s" % (p, "放行" if group == "allowed" else "应被拦", res[group][p]))
    emit(res)


def buses_of(devices):
    return [d.get("bus") for d in devices]


def cmd_summary(path, allowed, denied):
    results = {}
    with open(path) as f:
        for line in f:
            r = json.loads(line)
            results[r["label"]] = r
    allowed_set, k = set(allowed), len(allowed)
    lines, blockers = [], []

    o = results.get("open")
    if o:
        bad_allowed = [p for p, v in o["allowed"].items() if v != "ok"]
        leaked = [p for p, v in o["denied"].items() if v == "ok"]
        if leaked:
            blockers.append("设备限制没生效：被拦的 %s 仍然能打开" % ", ".join(leaked))
        elif bad_allowed:
            blockers.append("放行的设备也打不开：%s" % ", ".join("%s=%s" % (p, o["allowed"][p]) for p in bad_allowed))
        else:
            lines.append("设备限制生效：被拦的卡打不开（%s），放行的卡正常。" % ", ".join(sorted(set(o["denied"].values()))))

    smi = results.get("smi")
    if smi:
        lines.append("受限后的 nvidia-smi：退出码 %d，列出 %d 张卡（全部 %d 张）。" % (smi["code"], smi["gpus"], k + len(denied)))

    def describe(label, title, critical=False):
        r = results.get(label)
        if not r:
            return None
        if r.get("error"):
            # 只有不设变量时 CUDA 起不来才是硬伤；设了变量起不来本身就是要看的结果
            (blockers if critical else lines).append("%s：%s%s" % (title, r["error"], "" if critical else "。"))
            return None
        b = buses_of(r["devices"])
        inside = [x for x in b if x in allowed_set]
        text = "%s：CUDA 看到 %d 个设备，总线 %s" % (title, len(b), ", ".join(x or "?" for x in b) or "无")
        ctx = [d.get("ctx") for d in r["devices"] if "ctx" in d]
        if ctx:
            ok = sum(1 for c in ctx if c == "ok")
            text += "；能建上下文的 %d 个" % ok
            fails = sorted(set(c for c in ctx if c != "ok"))
            if fails:
                text += "（失败：%s）" % ", ".join(fails)
        lines.append(text + "。")
        return b, inside

    plain = describe("cuda_plain", "不设 CUDA_VISIBLE_DEVICES", critical=True)
    if plain:
        b, inside = plain
        if len(b) == k and set(b) == allowed_set:
            lines.append("→ CUDA 只看到放行的卡，并从 0 重新编号：严格模式下丹炉应注入 0..%d，脚本写死的 0,1,… 也会落在分到的卡上。" % (k - 1))
        elif len(b) == k + len(denied):
            lines.append("→ CUDA 仍列出全部卡、不重新编号：丹炉继续注入物理编号；写死的编号会指到被拦的卡上，任务报错而不是越界。")
    phys = describe("cuda_physical", "CUDA_VISIBLE_DEVICES=放行卡的物理编号")
    if not phys and plain and len(plain[0]) == k:
        lines.append("→ 重新编号以后，按物理编号写的选卡变量就失效了，所以严格模式下丹炉要改成注入 0 起的编号。")
    rel = describe("cuda_relative", "CUDA_VISIBLE_DEVICES=0..%d" % (k - 1))
    if rel:
        b, inside = rel
        if b and len(inside) == len(b):
            lines.append("→ 按 0..%d 写的选卡变量指向的就是放行的卡。" % (k - 1))

    base, egl = results.get("egl_base"), results.get("egl_strict")
    if egl and egl.get("error"):
        lines.append("EGL：%s（没有 EGL 渲染需求可以忽略）。" % egl["error"])
    elif egl:
        ok = [d for d in egl["devices"] if d.get("init") == "ok"]
        gpu_ok = [d for d in ok if not d.get("software")]
        outside = [d for d in gpu_ok if d.get("bus") and d["bus"] not in allowed_set]
        unknown = [d for d in gpu_ok if not d.get("bus")]
        total = len(base["devices"]) if base and not base.get("error") else "?"
        lines.append("EGL：不受限时列出 %s 个设备，受限后列出 %d 个，初始化成功 %d 个（显卡 %d 个，总线 %s）。" % (
            total, len(egl["devices"]), len(ok), len(gpu_ok), ", ".join(d.get("bus") or "?" for d in gpu_ok) or "无"))
        if outside:
            blockers.append("EGL 在被拦的卡上也初始化成功了：%s" % ", ".join(d["bus"] for d in outside))
        elif gpu_ok and not unknown:
            lines.append("→ 能初始化的 EGL 设备都在放行的卡上，按顺序挑第一个能用的设备（MuJoCo 的做法）会落在分到的卡上。")
        elif not gpu_ok:
            lines.append("→ 受限后没有显卡能做 EGL 渲染，需要再看放行的设备清单。")

    print("\n== 结论 ==")
    for line in lines:
        print(line)
    if blockers:
        print("\n需要注意：")
        for b in blockers:
            print("  ✗ " + b)
    print("\n把整份报告发回来，据此决定严格模式怎么做。")


def main():
    args = sys.argv[1:]
    if not args:
        print(__doc__)
        sys.exit(2)
    cmd = args[0]
    if cmd == "cuda":
        cmd_cuda(args[1], len(args) > 2 and args[2] == "ctx")
    elif cmd == "egl":
        cmd_egl(args[1], len(args) > 2 and args[2] == "init")
    elif cmd == "open":
        split = lambda s: [x for x in s.split(",") if x]
        cmd_open(args[1], split(args[2]), split(args[3]))
    elif cmd == "summary":
        split = lambda s: [norm(x) for x in s.split(",") if x]
        cmd_summary(args[1], split(args[2]), split(args[3]))
    else:
        print(__doc__)
        sys.exit(2)


if __name__ == "__main__":
    main()
