"""Device-test fixture for the Inspect code tab (plan §7a).

A small module with a deliberate bug: `normalize([])` divides by zero.
`traceback.txt` next to this file is the captured output of running it —
open both in Inspect to test the stack-trace lens chip jumps back here.
"""


def mean(values):
    """Arithmetic mean — the deliberate bug: no guard for an empty list."""
    return sum(values) / len(values)


def normalize(values):
    """Scale values so they sum to 1 (breaks on an empty list)."""
    m = mean(values)
    return [v / m for v in values]


def load_batches():
    """Pretend-load three batches; the last one is empty (the trigger)."""
    return [[3.0, 4.0, 5.0], [10.0, 20.0], []]


def main():
    for i, batch in enumerate(load_batches()):
        print(f"batch {i}: normalized={normalize(batch)}")


if __name__ == "__main__":
    main()
