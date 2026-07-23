#!/usr/bin/env python3
"""Synthesize an ANSI-colored training log for the Inspect log viewer (plan §7a).

Stdlib-only and deterministic (seeded), so re-running reproduces the same bytes.

    gen-train-log.py --small            # ~200-line train-small.log (the committed fixture)
    gen-train-log.py <MB> [out.log]     # e.g. `gen-train-log.py 120` for the 100 MB+
                                        # virtualization / search / index device gates

The output carries everything the viewer's features key on: `step N` / `epoch N`
markers, a WARN line, and a Python traceback mid-file.
"""
import random
import sys

GREEN, YELLOW, RED, CYAN, DIM, RESET = "\x1b[32m", "\x1b[33m", "\x1b[31m", "\x1b[36m", "\x1b[2m", "\x1b[0m"

TRACEBACK = (
    f"{RED}Traceback (most recent call last):{RESET}\n"
    '  File "trainer/run.py", line 88, in train_step\n'
    "    loss = criterion(logits, batch.labels)\n"
    '  File "trainer/losses.py", line 31, in __call__\n'
    "    return self.base(x.float(), y)\n"
    f"{RED}RuntimeError: CUDA error: an illegal memory access was encountered (recovered; retrying step){RESET}\n"
)


def emit(out, target_lines):
    rng = random.Random(1337)
    step = 0
    loss = 4.8
    out.write(f"{CYAN}trainer 0.4.1 — device-test fixture log (gen-train-log.py){RESET}\n")
    out.write(f"{DIM}config: epochs=3 batch_size=32 lr=3e-4 optimizer=adamw{RESET}\n")
    lines = 2
    epoch = 0
    while lines < target_lines:
        if step % 60 == 0:
            epoch += 1
            out.write(f"{CYAN}=== epoch {epoch} ==={RESET}\n")
            lines += 1
        step += 1
        loss = max(0.05, loss * (1 - rng.random() * 0.02))
        lr = 3e-4 * (0.999**step)
        out.write(
            f"{DIM}{step:>6}{RESET} step {step} epoch {epoch} "
            f"loss={GREEN}{loss:.4f}{RESET} lr={lr:.2e} grad_norm={rng.uniform(0.2, 2.5):.3f} "
            f"tok/s={rng.randint(1800, 2400)}\n"
        )
        lines += 1
        # one WARN and one traceback, both mid-file (deterministic steps)
        if step == 97:
            out.write(f"{YELLOW}WARN loss spike detected at step 97 (4.1x rolling median) — check data shard 12{RESET}\n")
            lines += 1
        if step == 130:
            out.write(TRACEBACK)
            lines += 6
    out.write(f"{GREEN}done: {step} steps, final loss {loss:.4f}{RESET}\n")


def main():
    if len(sys.argv) >= 2 and sys.argv[1] == "--small":
        with open("train-small.log", "w", encoding="utf-8") as f:
            emit(f, 200)
        print("wrote train-small.log")
        return
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(2)
    mb = float(sys.argv[1])
    path = sys.argv[2] if len(sys.argv) > 2 else f"train-{int(mb)}mb.log"
    # ~110 bytes/line -> lines for the requested size
    with open(path, "w", encoding="utf-8") as f:
        emit(f, int(mb * 1024 * 1024 / 110))
    print(f"wrote {path}")


if __name__ == "__main__":
    main()
