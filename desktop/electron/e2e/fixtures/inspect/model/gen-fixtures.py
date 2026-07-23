#!/usr/bin/env python3
"""Write the Inspect checkpoint fixtures by hand (plan §7a) — stdlib only.

No torch, no gguf pip, no onnx pip: all three formats are simple enough to
emit directly (safetensors = 8-byte header-len + JSON; GGUF v3 = a documented
little-endian header; ONNX = protobuf wire format, hand-encoded). Deterministic
— re-running reproduces the same bytes. Emits, next to this script:

  tiny.safetensors       ~16 KB, model.layers.{0,1}.* namespacing (tree / xN grouping)
  truncated.safetensors  header length says N but the JSON is cut mid-way (typed error)
  not-a-model.bin        not a checkpoint at all (unsupported-format error)
  tiny.gguf              GGUF v3, llama-arch metadata + blk.{0,1}.* tensors
  tiny.onnx              3-op graph (MatMul->Add->Relu) with raw_data initializers,
                         proving the parser skips embedded weight bytes
"""
import json
import struct

F32 = 4  # bytes per element


# ── safetensors ────────────────────────────────────────────────────────────────

def tensor_shapes():
    """Llama-shaped-in-miniature: 2 layers so the xN layer collapse groups."""
    shapes = [("model.embed_tokens.weight", [32, 16])]
    for i in range(2):
        p = f"model.layers.{i}"
        for w in ("q_proj", "k_proj", "v_proj", "o_proj"):
            shapes.append((f"{p}.self_attn.{w}.weight", [16, 16]))
        for w in ("gate_proj", "up_proj", "down_proj"):
            shapes.append((f"{p}.mlp.{w}.weight", [16, 16]))
        shapes.append((f"{p}.input_layernorm.weight", [16]))
        shapes.append((f"{p}.post_attention_layernorm.weight", [16]))
    shapes.append(("model.norm.weight", [16]))
    shapes.append(("lm_head.weight", [32, 16]))
    return shapes


def write_safetensors():
    header, offset = {}, 0
    for name, shape in tensor_shapes():
        size = F32
        for d in shape:
            size *= d
        header[name] = {"dtype": "F32", "shape": shape, "data_offsets": [offset, offset + size]}
        offset += size
    header["__metadata__"] = {"format": "pt", "purpose": "termipod inspect device-test fixture"}
    hjson = json.dumps(header, sort_keys=True).encode("utf-8")
    with open("tiny.safetensors", "wb") as f:
        f.write(struct.pack("<Q", len(hjson)))
        f.write(hjson)
        f.write(b"\x00" * offset)
    # Same header length, but the JSON stops half-way (zero-padded to the declared
    # size, so the parser reaches its JSON.parse and fails there — the typed error).
    with open("truncated.safetensors", "wb") as f:
        f.write(struct.pack("<Q", len(hjson)))
        f.write(hjson[: len(hjson) // 2])
        f.write(b"\x00" * (len(hjson) - len(hjson) // 2 + 64))
    with open("not-a-model.bin", "wb") as f:
        f.write(b"This is not a checkpoint; it exists to exercise the typed-error path.\n" * 12)


# ── gguf (v3, little-endian) ───────────────────────────────────────────────────

def gs(s):  # gguf string: u64 length + utf-8 bytes
    b = s.encode("utf-8")
    return struct.pack("<Q", len(b)) + b


def kv_u32(key, v):
    return gs(key) + struct.pack("<I", 4) + struct.pack("<I", v)  # type 4 = uint32


def kv_str(key, v):
    return gs(key) + struct.pack("<I", 8) + gs(v)  # type 8 = string


def write_gguf():
    kvs = [
        kv_str("general.architecture", "llama"),
        kv_str("general.name", "tiny-fixture"),
        kv_u32("general.alignment", 32),
        kv_u32("llama.block_count", 2),
        kv_u32("llama.embedding_length", 16),
        kv_u32("llama.context_length", 128),
        kv_u32("llama.feed_forward_length", 16),
        kv_u32("llama.attention.head_count", 4),
        kv_u32("llama.attention.head_count_kv", 2),
    ]
    # tensor infos: name, n_dims u32, dims u64[] (ne order), ggml type u32 (0 = F32),
    # u64 offset into the (32-byte-aligned) data section
    names = ["token_embd.weight"]
    for i in range(2):
        names += [f"blk.{i}.attn_q.weight", f"blk.{i}.attn_k.weight", f"blk.{i}.ffn_gate.weight"]
    names += ["output_norm.weight", "output.weight"]
    infos, offset = b"", 0
    dims_of = {"output_norm.weight": [16]}
    for name in names:
        dims = dims_of.get(name, [16, 16])
        size = F32
        for d in dims:
            size *= d
        infos += gs(name) + struct.pack("<I", len(dims))
        for d in dims:
            infos += struct.pack("<Q", d)
        infos += struct.pack("<I", 0) + struct.pack("<Q", offset)
        offset += (size + 31) // 32 * 32
    header = b"GGUF" + struct.pack("<I", 3) + struct.pack("<Q", len(names)) + struct.pack("<Q", len(kvs))
    header += b"".join(kvs) + infos
    pad = (32 - len(header) % 32) % 32
    with open("tiny.gguf", "wb") as f:
        f.write(header + b"\x00" * pad + b"\x00" * offset)


# ── onnx (protobuf wire format, hand-encoded) ──────────────────────────────────

def varint(n):
    out = b""
    while True:
        b7, n = n & 0x7F, n >> 7
        out += bytes([b7 | (0x80 if n else 0)])
        if not n:
            return out


def fv(field, n):  # varint field
    return varint(field << 3) + varint(n)


def fb(field, payload):  # length-delimited field
    return varint((field << 3) | 2) + varint(len(payload)) + payload


def fs(field, s):
    return fb(field, s.encode("utf-8"))


def onnx_tensor(name, dims, data):
    packed = b"".join(varint(d) for d in dims)
    # dims(1, packed) + data_type(2)=1 float32 + name(8) + raw_data(9)
    return fb(1, packed) + fv(2, 1) + fs(8, name) + fb(9, data)


def onnx_node(name, op, inputs, outputs):
    out = b""
    for i in inputs:
        out += fs(1, i)
    for o in outputs:
        out += fs(2, o)
    return out + fs(3, name) + fs(4, op)


def write_onnx():
    w0 = onnx_tensor("w0", [16, 16], b"\x00" * (16 * 16 * F32))
    b0 = onnx_tensor("b0", [16], b"\x00" * (16 * F32))
    graph = (
        fb(1, onnx_node("matmul0", "MatMul", ["input", "w0"], ["h0"]))
        + fb(1, onnx_node("add0", "Add", ["h0", "b0"], ["h1"]))
        + fb(1, onnx_node("relu0", "Relu", ["h1"], ["output"]))
        + fs(2, "tiny_graph")
        + fb(5, w0)
        + fb(5, b0)
        + fb(11, fs(1, "input"))
        + fb(12, fs(1, "output"))
    )
    model = (
        fv(1, 8)  # ir_version
        + fs(2, "termipod-gen-fixtures")
        + fs(3, "1.0")
        + fb(7, graph)
        + fb(8, fv(2, 17))  # opset_import { version: 17 }
        + fb(14, fs(1, "purpose") + fs(2, "inspect device-test fixture"))
    )
    with open("tiny.onnx", "wb") as f:
        f.write(model)


if __name__ == "__main__":
    write_safetensors()
    write_gguf()
    write_onnx()
    print("wrote tiny.safetensors truncated.safetensors not-a-model.bin tiny.gguf tiny.onnx")
