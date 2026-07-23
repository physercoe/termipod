"""Device-test fixture for the Inspect run-scratch button (plan §7a).

Prints a few lines to stdout, then raises — so one run exercises BOTH the
captured-output panel AND the stderr -> stack-trace-lens path.
"""


def fib(n):
    a, b = 0, 1
    for _ in range(n):
        a, b = b, a + b
    return a


def main():
    for n in (5, 10, 20):
        print(f"fib({n}) = {fib(n)}")
    print("now the deliberate failure:")
    raise RuntimeError("algo.py always fails here (device-test fixture)")


if __name__ == "__main__":
    main()
