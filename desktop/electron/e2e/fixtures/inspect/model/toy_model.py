"""Self-contained nn.Module for the Inspect tracer demo (plan §7a).

Entry expression on a torch venue:  toy_model.TinyNet()
Example input shape:                (1, 3, 32, 32)

No imports beyond torch — the tracer runs it on the meta device (weightless),
so any interpreter preset with torch >= 2.1 works; no GPU needed.
"""
import torch
import torch.nn as nn


class Block(nn.Module):
    def __init__(self, ch):
        super().__init__()
        self.conv = nn.Conv2d(ch, ch, 3, padding=1)
        self.norm = nn.BatchNorm2d(ch)
        self.act = nn.ReLU()

    def forward(self, x):
        return self.act(self.norm(self.conv(x))) + x


class TinyNet(nn.Module):
    """Stem -> 3 residual blocks (the xN collapse) -> pooled linear head."""

    def __init__(self, num_classes=10):
        super().__init__()
        self.stem = nn.Conv2d(3, 16, 3, padding=1)
        self.blocks = nn.Sequential(*(Block(16) for _ in range(3)))
        self.pool = nn.AdaptiveAvgPool2d(1)
        self.head = nn.Linear(16, num_classes)

    def forward(self, x):
        x = self.pool(self.blocks(self.stem(x)))
        return self.head(torch.flatten(x, 1))


if __name__ == "__main__":
    with torch.device("meta"):
        model = TinyNet()
    n = sum(p.numel() for p in model.parameters())
    print(f"TinyNet: {n} params (meta device — no memory allocated)")
