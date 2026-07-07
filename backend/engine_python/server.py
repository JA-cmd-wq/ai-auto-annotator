#!/usr/bin/env python3
"""LocateAnything-3B HTTP sidecar for the ai-auto-annotator Go backend.

The Go backend's `engine.InferenceEngine.Locate(ctx, imagePath, prompt, mode)`
contract is exposed over HTTP so the model can stay resident on the GPU instead
of being reloaded per request. Only the Python standard library is used for the
HTTP layer (no FastAPI/Flask/uvicorn), so the only heavy deps are the ones the
model already needs: torch / transformers / pillow / opencv.

Endpoints:
  GET  /health  -> {"ok": true}
  GET  /status  -> {"loaded": bool, "model_path": str, "device": str, ...}
  POST /locate  -> {"image": <abs path>, "prompt": str, "mode": int}
                -> {"detections": [{"label": str, "box": [x1,y1,x2,y2], "score": float}]}

The prompt arriving from Go is already the full instruction built by
service.BuildPrompt ("Locate all the instances that matches the following
description: <cats>.") which matches infer_single.py's detect template exactly,
so we pass it through to the model as-is (task="raw").

Run:
  python3 server.py
  # env: LA_MODEL_PATH, LA_HOST, LA_PORT, LA_DEVICE, LA_COORD_ORDER,
  #      LA_GENERATION_MODE, LA_MAX_NEW_TOKENS, LA_TEMPERATURE,
  #      LA_TOP_P, LA_REPETITION_PENALTY, LA_ATTN_IMPLEMENTATION
"""
import argparse
import importlib.machinery
import json
import os
import re
import sys
import threading
import types
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


# ---------------------------------------------------------------------------
# decord / lmdb are hard-imported by the model's remote processor code but only
# used for video / LMDB-dataset inputs. Stub them before transformers imports
# the remote code (same trick as infer_single.py).
# ---------------------------------------------------------------------------
def _ensure_stub(name: str) -> None:
    if name in sys.modules:
        return
    try:
        __import__(name)
    except Exception:
        stub = types.ModuleType(name)
        stub.__stub__ = True
        stub.__version__ = "0.0.0-stub"
        stub.__spec__ = importlib.machinery.ModuleSpec(name, loader=None)
        sys.modules[name] = stub


for _optional in ("decord", "lmdb"):
    _ensure_stub(_optional)


import torch  # noqa: E402
from PIL import Image  # noqa: E402
from transformers import AutoModel, AutoProcessor, AutoTokenizer  # noqa: E402


# Go side model.Mode* constants (see backend/internal/model/model.go)
MODE_HYBRID = 0
MODE_SLOW = 1
MODE_FAST = 2


def _clean_label(raw: str) -> str:
    raw = re.sub(r"<\|.*?\|>", "", raw)
    raw = re.sub(r"</?(ref|box|c)>", "", raw)
    raw = re.sub(r"<(?:null|switch|\d+)>", "", raw)
    return raw.strip().strip(".").strip()


def parse_detections(answer: str, width: int, height: int, coord_order: str):
    """Parse `<ref>label</ref><box><a><b><c><d></box>` into pixel boxes."""
    token_re = re.compile(r"<ref>(.*?)</ref>|<box>(.*?)</box>", re.S)
    detections = []
    current_label = None
    for m in token_re.finditer(answer):
        ref, box = m.group(1), m.group(2)
        if ref is not None:
            current_label = _clean_label(ref)
            continue
        nums = [int(n) for n in re.findall(r"<(\d+)>", box)]
        if len(nums) != 4:
            continue  # skip <box>none</box> or 2-number point boxes
        a, b, c, d = nums
        if coord_order == "xxyy":
            x1, x2, y1, y2 = a, b, c, d
        else:  # xyxy
            x1, y1, x2, y2 = a, b, c, d
        px1, px2 = sorted((x1 / 1000 * width, x2 / 1000 * width))
        py1, py2 = sorted((y1 / 1000 * height, y2 / 1000 * height))
        detections.append({
            "label": current_label or "object",
            "box": [round(px1, 2), round(py1, 2), round(px2, 2), round(py2, 2)],
            "score": 1.0,
        })
    return detections


PALETTE = [
    (255, 59, 48), (52, 199, 89), (0, 122, 255), (255, 149, 0),
    (175, 82, 222), (255, 45, 85), (90, 200, 250), (255, 204, 0),
]


def _hex_to_rgb(h):
    h = (h or "").lstrip("#")
    if len(h) == 6:
        return tuple(int(h[i:i + 2], 16) for i in (0, 2, 4))
    return None


def _font(size: int):
    from PIL import ImageFont
    for path in ("/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
                 "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"):
        if os.path.exists(path):
            return ImageFont.truetype(path, size)
    return ImageFont.load_default()


def render_detections(image_path, detections, output_path):
    """Draw bboxes + labels onto a copy of the image and save to output_path."""
    from PIL import ImageDraw
    img = Image.open(image_path).convert("RGB").copy()
    draw = ImageDraw.Draw(img)
    label_colors = {}
    line_w = max(2, round(min(img.size) / 300))
    font = _font(max(14, round(min(img.size) / 45)))
    for det in detections:
        label = det.get("label", "object")
        color = det.get("color")
        if color:
            color = _hex_to_rgb(color) or PALETTE[0]
        else:
            color = label_colors.setdefault(label, PALETTE[len(label_colors) % len(PALETTE)])
        x1, y1, x2, y2 = det["box"]
        draw.rectangle([x1, y1, x2, y2], outline=color, width=line_w)
        try:
            tb = draw.textbbox((0, 0), label, font=font)
            tw, th = tb[2] - tb[0], tb[3] - tb[1]
        except Exception:
            tw, th = len(label) * 8, 14
        ty = max(0, y1 - th - 4)
        draw.rectangle([x1, ty, x1 + tw + 6, ty + th + 4], fill=color)
        draw.text((x1 + 3, ty + 2), label, fill=(255, 255, 255), font=font)
    img.save(output_path)
    return output_path


def extract_frames(video_path, output_dir, fps_sample=0.0, max_frames=0):
    """Extract frames from a video with cv2.

    fps_sample <= 0 means every frame (stride 1); otherwise
    stride = round(src_fps / fps_sample). max_frames <= 0 means no cap.
    """
    import cv2
    cap = cv2.VideoCapture(video_path)
    if not cap.isOpened():
        raise RuntimeError(f"cannot open video: {video_path}")
    src_fps = cap.get(cv2.CAP_PROP_FPS) or 25.0
    total = int(cap.get(cv2.CAP_PROP_FRAME_COUNT) or 0)
    if fps_sample and fps_sample > 0:
        stride = max(1, int(round(src_fps / fps_sample)))
    else:
        stride = 1
    os.makedirs(output_dir, exist_ok=True)
    frames = []
    idx = 0
    saved = 0
    while True:
        ret, frame = cap.read()
        if not ret:
            break
        if idx % stride == 0:
            h, w = frame.shape[:2]
            fname = f"frame_{saved:06d}.jpg"
            fpath = os.path.join(output_dir, fname)
            cv2.imwrite(fpath, frame)
            frames.append({"path": fpath, "frame_index": saved, "width": w, "height": h})
            saved += 1
            if max_frames > 0 and saved >= max_frames:
                break
        idx += 1
    cap.release()
    return {"frames": frames, "fps": round(src_fps, 3),
            "total_frames": total, "sampled": saved}


class LocateEngine:
    """Holds the resident model and serializes inference (batch_size == 1)."""

    def __init__(self, args):
        self.args = args
        self.mu = threading.Lock()
        self.tokenizer = None
        self.processor = None
        self.model = None
        self.device = args.device
        self.dtype = torch.bfloat16
        self.loaded = False
        self.load_error = None

    def load(self):
        try:
            print(f"[sidecar] loading model from {self.args.model_path} on "
                  f"{self.device} ({self.dtype})...", flush=True)
            self.tokenizer = AutoTokenizer.from_pretrained(
                self.args.model_path, trust_remote_code=True)
            self.processor = AutoProcessor.from_pretrained(
                self.args.model_path, trust_remote_code=True)
            self.model = AutoModel.from_pretrained(
                self.args.model_path,
                torch_dtype=self.dtype,
                trust_remote_code=True,
                attn_implementation=self.args.attn_implementation,
            ).to(self.device).eval()
            self.loaded = True
            print("[sidecar] model loaded and ready", flush=True)
        except Exception as e:  # noqa: BLE001
            self.load_error = f"{type(e).__name__}: {e}"
            print(f"[sidecar] model load FAILED: {self.load_error}", file=sys.stderr, flush=True)

    @torch.no_grad()
    def _generate(self, image: Image.Image, prompt: str, mode: int):
        args = self.args
        messages = [{"role": "user", "content": [
            {"type": "image", "image": image},
            {"type": "text", "text": prompt},
        ]}]
        text = self.processor.py_apply_chat_template(
            messages, tokenize=False, add_generation_prompt=True)
        images, videos = self.processor.process_vision_info(messages)
        inputs = self.processor(
            text=[text], images=images, videos=videos, return_tensors="pt"
        ).to(self.model.device)

        pixel_values = inputs["pixel_values"].to(self.model.dtype)
        input_ids = inputs["input_ids"]
        attention_mask = inputs["attention_mask"]
        image_grid_hws = inputs.get("image_grid_hws", None)

        # fast/hybrid need MagiAttention (Hopper/Blackwell only). The SDPA
        # fallback is NOT usable for parallel box decoding here: empirically it
        # degenerates into thousands of repeated boxes per frame, so force slow
        # (pure AR) whenever magi is unavailable.
        gen_mode = args.generation_mode
        if mode == MODE_SLOW:
            gen_mode = "slow"
        if gen_mode in ("fast", "hybrid") and not _magi_available():
            gen_mode = "slow"

        do_sample = args.temperature > 0
        response = self.model.generate(
            pixel_values=pixel_values,
            input_ids=input_ids,
            attention_mask=attention_mask,
            image_grid_hws=image_grid_hws,
            tokenizer=self.tokenizer,
            max_new_tokens=args.max_new_tokens,
            use_cache=True,
            generation_mode=gen_mode,
            temperature=args.temperature,
            do_sample=do_sample,
            top_p=args.top_p if do_sample else None,
            repetition_penalty=args.repetition_penalty,
            verbose=False,
        )
        if isinstance(response, tuple):
            return response[0]
        return response

    def locate(self, image_path: str, prompt: str, mode: int):
        if not self.loaded:
            raise RuntimeError(self.load_error or "model is not loaded")
        with self.mu:
            image = Image.open(image_path).convert("RGB")
            w, h = image.size
            answer = self._generate(image, prompt, mode)
            dets = parse_detections(answer, w, h, self.args.coord_order)
            return dets, answer


_magi_available_flag = None


def _magi_available() -> bool:
    global _magi_available_flag
    if _magi_available_flag is None:
        try:
            import magi_attention  # noqa: F401
            _magi_available_flag = True
        except Exception:
            _magi_available_flag = False
    return _magi_available_flag


ENGINE: LocateEngine  # populated in main()


def _send_json(handler, code: int, payload: dict):
    body = json.dumps(payload).encode("utf-8")
    handler.send_response(code)
    handler.send_header("Content-Type", "application/json")
    handler.send_header("Content-Length", str(len(body)))
    handler.end_headers()
    handler.wfile.write(body)


class Handler(BaseHTTPRequestHandler):
    server_version = "LocateAnythingSidecar/1.0"

    def log_message(self, fmt, *args):  # quieter, prefixed
        sys.stdout.write("[sidecar] " + (fmt % args) + "\n")
        sys.stdout.flush()

    def _body(self):
        length = int(self.headers.get("Content-Length", "0") or "0")
        if length <= 0:
            return {}
        raw = self.rfile.read(length)
        try:
            return json.loads(raw.decode("utf-8"))
        except Exception:
            return None

    def do_GET(self):
        if self.path == "/health":
            _send_json(self, 200, {"ok": True})
            return
        if self.path == "/status":
            _send_json(self, 200, {
                "loaded": ENGINE.loaded,
                "model_path": ENGINE.args.model_path,
                "device": ENGINE.device,
                "engine": "python",
                "abi_version": 0,
                "threads": 0,
                "error": ENGINE.load_error or "",
            })
            return
        _send_json(self, 404, {"error": "not found"})

    def do_POST(self):
        data = self._body()
        if not isinstance(data, dict):
            _send_json(self, 400, {"error": "invalid json body"})
            return
        if self.path == "/locate":
            self._handle_locate(data)
        elif self.path == "/extract":
            self._handle_extract(data)
        elif self.path == "/render":
            self._handle_render(data)
        else:
            _send_json(self, 404, {"error": "not found"})

    def _send_error(self, what, e):
        msg = f"{type(e).__name__}: {e}"
        self.log_message("%s error: %s", what, msg)
        _send_json(self, 500, {"error": msg})

    def _handle_locate(self, data):
        image_path = data.get("image")
        prompt = data.get("prompt")
        mode = int(data.get("mode", MODE_HYBRID))
        if not image_path or not prompt:
            _send_json(self, 400, {"error": "image and prompt are required"})
            return
        if not os.path.isfile(image_path):
            _send_json(self, 404, {"error": f"image not found: {image_path}"})
            return
        try:
            dets, raw = ENGINE.locate(image_path, prompt, mode)
            self.log_message("locate ok: image=%s mode=%d -> %d box(es)",
                             os.path.basename(image_path), mode, len(dets))
            _send_json(self, 200, {"detections": dets, "raw": raw})
        except Exception as e:  # noqa: BLE001
            self._send_error("locate", e)

    def _handle_extract(self, data):
        video_path = data.get("video")
        output_dir = data.get("output_dir")
        if not video_path or not output_dir:
            _send_json(self, 400, {"error": "video and output_dir are required"})
            return
        if not os.path.isfile(video_path):
            _send_json(self, 404, {"error": f"video not found: {video_path}"})
            return
        fps = float(data.get("fps", 0.0))
        max_frames = int(data.get("max_frames", 0))
        try:
            result = extract_frames(video_path, output_dir, fps, max_frames)
            self.log_message("extract ok: video=%s -> %d frame(s)",
                             os.path.basename(video_path), result["sampled"])
            _send_json(self, 200, result)
        except Exception as e:  # noqa: BLE001
            self._send_error("extract", e)

    def _handle_render(self, data):
        image_path = data.get("image")
        output_path = data.get("output")
        detections = data.get("detections", [])
        if not image_path or not output_path:
            _send_json(self, 400, {"error": "image and output are required"})
            return
        if not os.path.isfile(image_path):
            _send_json(self, 404, {"error": f"image not found: {image_path}"})
            return
        try:
            render_detections(image_path, detections, output_path)
            self.log_message("render ok: %s -> %d box(es)",
                             os.path.basename(image_path), len(detections))
            _send_json(self, 200, {"ok": True, "output": output_path})
        except Exception as e:  # noqa: BLE001
            self._send_error("render", e)


def parse_args():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--model-path", default=os.environ.get(
        "LA_MODEL_PATH", "/mnt/LocateAnything-3B"))
    ap.add_argument("--host", default=os.environ.get("LA_HOST", "127.0.0.1"))
    ap.add_argument("--port", type=int, default=int(os.environ.get("LA_PORT", "8001")))
    ap.add_argument("--device", default=os.environ.get("LA_DEVICE", "cuda"))
    ap.add_argument("--coord-order", default=os.environ.get("LA_COORD_ORDER", "xyxy"),
                    choices=["xyxy", "xxyy"])
    ap.add_argument("--generation-mode", default=os.environ.get("LA_GENERATION_MODE", "hybrid"),
                    choices=["slow", "fast", "hybrid"])
    # 2048 caps runaway generations (a repetition loop at 8192 wastes 4x the
    # time per bad frame); normal frames stop early at im_end regardless.
    ap.add_argument("--max-new-tokens", type=int,
                    default=int(os.environ.get("LA_MAX_NEW_TOKENS", "2048")))
    ap.add_argument("--temperature", type=float,
                    default=float(os.environ.get("LA_TEMPERATURE", "0.0")))
    ap.add_argument("--top-p", type=float,
                    default=float(os.environ.get("LA_TOP_P", "0.9")))
    ap.add_argument("--repetition-penalty", type=float,
                    default=float(os.environ.get("LA_REPETITION_PENALTY", "1.1")))
    ap.add_argument("--attn-implementation", default=os.environ.get("LA_ATTN_IMPLEMENTATION", "sdpa"),
                    choices=["sdpa", "eager", "magi", "flash_attention_2"])
    return ap.parse_args()


def main():
    global ENGINE
    args = parse_args()
    if args.device.startswith("cuda") and not torch.cuda.is_available():
        print("[sidecar] CUDA not available, falling back to CPU (very slow).",
              file=sys.stderr, flush=True)
        args.device = "cpu"

    ENGINE = LocateEngine(args)
    ENGINE.load()

    srv = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"[sidecar] listening on http://{args.host}:{args.port} "
          f"(loaded={ENGINE.loaded})", flush=True)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        print("[sidecar] shutting down", flush=True)
    finally:
        srv.server_close()


if __name__ == "__main__":
    main()
