/// Unit tests for the FLOPS / throughput estimator. Run: node --test src/state/flops.test.ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  estimateFlops,
  effectiveDeviceFlops,
  peakTflops,
  humanFlops,
  humanDuration,
  GPU_PRESETS,
  type FlopsInputs,
  type FlopsRuntime,
} from './flops.ts';

const gpu = (id: string) => {
  const g = GPU_PRESETS.find((x) => x.id === id);
  if (g === undefined) throw new Error(`preset ${id} missing`);
  return g;
};

test('inference: linear-only (no attn dims) matches 2·N per token', () => {
  const inp: FlopsInputs = { activeParams: 10e9 };
  const rt: FlopsRuntime = { mode: 'inference', context: 2048, batch: 1, deviceFlops: 1e14 };
  const e = estimateFlops(inp, rt);
  assert.equal(e.attnComputable, false);
  assert.equal(e.linearFlopsPerToken, 2e10);
  assert.equal(e.attnFlopsPerToken, 0);
  assert.equal(e.stepTokens, 2048);
  assert.equal(e.stepFlops, 2e10 * 2048);
  assert.ok(Math.abs(e.stepSeconds - 0.4096) < 1e-9);
  assert.ok(Math.abs(e.tokensPerSecond - 5000) < 1e-6);
  // decode: 2·N / deviceFlops × 1000 = 2e10/1e14×1000 = 0.2 ms
  assert.ok(e.decodeMsPerToken !== undefined && Math.abs(e.decodeMsPerToken - 0.2) < 1e-9);
});

test('training: forward+backward = 6·N per token, no decode latency', () => {
  const inp: FlopsInputs = { activeParams: 10e9 };
  const rt: FlopsRuntime = { mode: 'training', context: 2048, batch: 1, deviceFlops: 1e14 };
  const e = estimateFlops(inp, rt);
  assert.equal(e.linearFlopsPerToken, 6e10);
  assert.equal(e.stepFlops, 6e10 * 2048);
  assert.ok(Math.abs(e.tokensPerSecond - 2048 / 1.2288) < 1e-6);
  assert.equal(e.decodeMsPerToken, undefined);
});

test('attention term scales with s² (independent hand reference)', () => {
  // Isolate attention: tiny params so the linear term is negligible.
  const inp: FlopsInputs = { activeParams: 1, layers: 2, attnDim: 100 };
  const base: FlopsRuntime = { mode: 'inference', context: 1000, batch: 1, deviceFlops: 1e12 };
  const e1 = estimateFlops(inp, base);
  assert.equal(e1.attnComputable, true);
  // attnStep = fwdMult(1)·b(1)·layers(2)·2·attnDim(100)·s²(1e6) = 4e8
  assert.ok(Math.abs(e1.stepFlops - (4e8 + 2 * 1000)) < 1); // + linear 2·1·1000
  const e2 = estimateFlops(inp, { ...base, context: 2000 });
  // Doubling s → attention term ×4 (s²): 4e8 → 1.6e9
  const attn1 = e1.stepFlops - 2 * 1 * 1000;
  const attn2 = e2.stepFlops - 2 * 1 * 2000;
  assert.ok(Math.abs(attn2 / attn1 - 4) < 1e-6);
});

test('per-token attention at full context = 4·layers·s·attnDim', () => {
  const inp: FlopsInputs = { activeParams: 1, layers: 3, attnDim: 128 };
  const rt: FlopsRuntime = { mode: 'inference', context: 4096, batch: 1, deviceFlops: 1e12 };
  const e = estimateFlops(inp, rt);
  assert.equal(e.attnFlopsPerToken, 4 * 3 * 4096 * 128);
});

test('effectiveDeviceFlops = peak × devices × MFU', () => {
  assert.ok(Math.abs(effectiveDeviceFlops(495, 1, 0.4) - 1.98e14) < 1e6);
  assert.ok(Math.abs(effectiveDeviceFlops(495, 8, 0.4) - 8 * 1.98e14) < 1e8);
  // numGpus floored at 1
  assert.equal(effectiveDeviceFlops(100, 0, 0.5), 100e12 * 0.5);
});

test('peakTflops honours per-GPU precision support', () => {
  assert.equal(peakTflops(gpu('h100'), 'bf16'), 495);
  assert.equal(peakTflops(gpu('h100'), 'fp8'), 989);
  assert.equal(peakTflops(gpu('h100'), 'fp4'), undefined); // Hopper has no FP4
  assert.equal(peakTflops(gpu('a100'), 'fp8'), undefined); // Ampere: bf16 only
  assert.equal(peakTflops(gpu('b200'), 'fp4'), 9000);
});

test('deviceFlops 0 yields Infinity / 0 rather than NaN', () => {
  const e = estimateFlops({ activeParams: 1e9 }, { mode: 'inference', context: 1024, batch: 1, deviceFlops: 0 });
  assert.equal(e.stepSeconds, Infinity);
  assert.equal(e.tokensPerSecond, 0);
  assert.equal(e.decodeMsPerToken, Infinity);
});

test('humanFlops formats magnitudes', () => {
  assert.equal(humanFlops(4.4e10), '44.0 GFLOP');
  assert.equal(humanFlops(1.3e15), '1.3 PFLOP');
  assert.equal(humanFlops(0), '—');
});

test('humanDuration formats across ranges', () => {
  assert.equal(humanDuration(0.0002), '200 µs');
  assert.equal(humanDuration(0.4096), '410 ms');
  assert.equal(humanDuration(1.2288), '1.23 s');
  assert.equal(humanDuration(3600), '1.0 h');
  assert.equal(humanDuration(0), '—');
});
