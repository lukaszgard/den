#!/usr/bin/env python3
"""Animate den's gopher logo (den.avif): in the terminal, or as den-anim.avif for the README.

    python3 scripts/logo-anim.py                       # play it here, 40 columns wide
    python3 scripts/logo-anim.py 60                    # ... 60 columns wide
    python3 scripts/logo-anim.py --avif den-anim.avif  # write the README's animation

Playing needs a terminal with kitty graphics animation (Ghostty 1.4+ or kitty), and
not tmux. The script uploads the frames and exits; the terminal plays the loop. Remove
the images with:  printf '\\e_Ga=d,d=A\\e\\\\'

Needs Python 3 with Pillow (built with AVIF) and numpy, and macOS for the Menlo font.

A 4 s loop. The headlamp pulses and the gopher blinks, and the den is busy: the left
monitor scrolls a log, the prompt types `den doctor`, the right monitors show a
traffic chart, a health check sweep and a level meter, ROOT_ACCESS lights up, console
keys and lights blink and the screens shimmer. In the terminal, frame 1 is the whole
logo and every other frame only sends the areas that differ from frame 1.
"""
import base64
import io
import math
import os
import random
import subprocess
import sys

import numpy as np
from PIL import Image, ImageChops, ImageDraw, ImageFilter, ImageFont

LOGO = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "den.avif")
MENLO = "/System/Library/Fonts/Menlo.ttc"
SIZE = 720                    # sent to the terminal; the logo is 1440x1440
README_SIZE = 800             # shown 400 px wide in the README, sharp on retina
FRAMES, GAP = 40, 100         # 4 s loop
CHUNK = 4096                  # max base64 bytes per kitty graphics escape sequence

# Positions in the 1440x1440 logo.
LAMP = (664, 498)
EYES = [(627.5, 658, 56.5, 56), (798, 660.5, 62, 54.5)]  # centre, radii
SKIN, INK = (85, 175, 209), (8, 8, 12)
PROMPT = (432, 569)           # left end of the ">" baseline on the prompt screen
CURSOR = (445, 567, 463, 576)
LABEL = (1078, 546, 1224, 588)  # ROOT_ACCESS
GLOW_BOX = (400, 300, 880, 780)

# Each screen: seed points inside it and a box around it.
DARK = lambda c: max(c) < 110 and c[1] >= c[0] and c[1] > 25
CYAN = lambda c: c[1] > 150 and c[2] > 140 and c[1] - c[2] > -15 and c[0] < 200
SCREENS = {
    "log": ([(300, 650), (300, 600), (250, 700)], (205, 515, 395, 795), DARK, 22),
    "prompt": ([(470, 700), (440, 600)], (400, 520, 560, 790), CYAN, 18),
    "chart": ([(960, 700), (1020, 560)], (940, 510, 1055, 790), CYAN, 18),
    "health": ([(1170, 700), (1120, 650), (1230, 740)], (1070, 595, 1270, 795), DARK, 22),
    "meter": ([(1315, 700)], (1285, 570, 1345, 820), CYAN, 18),
}
KEYBOARDS = [(355, 795, 432, 852), (1030, 795, 1215, 870)]
DISPLAYS = [(240, 805, 375, 852), (430, 790, 548, 842), (935, 790, 1030, 842), (1230, 830, 1332, 880)]

# Areas that change, sent as patches over frame 1.
REGIONS = [GLOW_BOX, (200, 510, 400, 800), (930, 505, 1060, 790), (1060, 490, 1280, 800),
           (1280, 570, 1350, 820), (220, 780, 600, 900), (900, 780, 1350, 900)]

GREEN, DIM, WARN, ERR = (120, 235, 150), (60, 150, 105), (240, 200, 90), (250, 105, 90)
LOG = [  # at most 17 characters, the width of the log monitor
    (GREEN, "rds-prod up :5432"), (DIM, "iam token minted"),
    (GREEN, "redis up :6379"), (DIM, "health rds 12ms"),
    (DIM, "pgpass updated"), (WARN, "WARN docdb 840ms"),
    (DIM, "refresh in 13m"), (GREEN, "os proxy :9200"),
    (ERR, "ERR neptune reset"), (WARN, "retry neptune 1/3"),
    (GREEN, "neptune up :8182"), (DIM, "health redis 3ms"),
    (GREEN, "alloydb up :5433"), (GREEN, "memorystore ok"),
    (DIM, "token refreshed"), (GREEN, "docdb up :27017"),
]
LOG_AT = [1, 3, 4, 9, 12, 13, 14, 19, 22, 26, 27, 28, 31, 34, 37, 38]  # a line arrives
HEALTH = [("rds-prod", "12ms"), ("redis-cache", "3ms"), ("docdb", "21ms"), ("opensearch", "9ms"),
          ("neptune", "18ms"), ("cloudsql", "14ms"), ("alloydb", "11ms"), ("memorystore", "4ms")]
TYPED = "den doctor"


def load_logo():
    try:
        return Image.open(LOGO).convert("RGB")
    except Exception:  # Pillow built without AVIF
        png = subprocess.run(["magick", LOGO, "png:-"], capture_output=True, check=True).stdout
        return Image.open(io.BytesIO(png)).convert("RGB")


def neighbours(x, y):
    return (x + 1, y), (x - 1, y), (x, y + 1), (x, y - 1)


def grow(logo, seeds, box, ok, tol):
    """Pixels reachable from seeds through neighbours of similar colour that pass ok."""
    px = logo.load()
    x0, y0, x1, y1 = box
    mask = np.zeros((logo.height, logo.width), bool)
    todo = [s for s in seeds if ok(px[s])]
    while todo:
        x, y = todo.pop()
        if mask[y, x]:
            continue
        mask[y, x] = True
        c = px[x, y]
        for nx, ny in neighbours(x, y):
            if x0 <= nx < x1 and y0 <= ny < y1 and not mask[ny, nx]:
                d = px[nx, ny]
                if max(abs(d[0] - c[0]), abs(d[1] - c[1]), abs(d[2] - c[2])) <= tol and ok(d):
                    todo.append((nx, ny))
    return mask


def fill_rows(mask):
    """Close the holes the old text left in a dark screen's mask."""
    out = mask.copy()
    for y in np.nonzero(mask.any(1))[0]:
        xs = np.nonzero(mask[y])[0]
        if len(xs) > 20:
            out[y, xs.min():xs.max() + 1] = True
    return out


def parts(logo, box, ok, lo=12, hi=900):
    """Connected groups of pixels inside box that pass ok: keys, display bars."""
    px = logo.load()
    x0, y0, x1, y1 = box
    seen = np.zeros((logo.height, logo.width), bool)
    found = []
    for y in range(y0, y1):
        for x in range(x0, x1):
            if seen[y, x] or not ok(px[x, y]):
                continue
            seen[y, x] = True
            todo, pts = [(x, y)], []
            while todo:
                p = todo.pop()
                pts.append(p)
                for nx, ny in neighbours(*p):
                    if x0 <= nx < x1 and y0 <= ny < y1 and not seen[ny, nx] and ok(px[nx, ny]):
                        seen[ny, nx] = True
                        todo.append((nx, ny))
            if lo <= len(pts) <= hi:
                found.append((np.array([p[1] for p in pts]), np.array([p[0] for p in pts])))
    return found


def bbox(mask, pad=0):
    ys, xs = np.nonzero(mask)
    return xs.min() - pad, ys.min() - pad, xs.max() + 1 + pad, ys.max() + 1 + pad


def cross(a, b):
    """Where the line through points a[0], a[1] meets the line through b[0], b[1]."""
    (x1, y1), (x2, y2) = a
    (x3, y3), (x4, y4) = b
    d = (x1 - x2) * (y3 - y4) - (y1 - y2) * (x3 - x4)
    p, q = x1 * y2 - y1 * x2, x3 * y4 - y3 * x4
    return (p * (x3 - x4) - (x1 - x2) * q) / d, (p * (y3 - y4) - (y1 - y2) * q) / d


def corners(mask):
    """A screen's corners (TL, TR, BR, BL), from lines along its four edges."""
    x0, y0, x1, y1 = bbox(mask)
    xa, xb = x0 + (x1 - x0) // 4, x1 - (x1 - x0) // 4
    ya, yb = y0 + (y1 - y0) // 4, y1 - (y1 - y0) // 4
    row = lambda y: np.nonzero(mask[y])[0]
    col = lambda x: np.nonzero(mask[:, x])[0]
    left = ((row(ya).min(), ya), (row(yb).min(), yb))
    right = ((row(ya).max(), ya), (row(yb).max(), yb))
    top = ((xa, col(xa).min()), (xb, col(xb).min()))
    bottom = ((xa, col(xa).max()), (xb, col(xb).max()))
    return [cross(top, left), cross(top, right), cross(bottom, right), cross(bottom, left)]


def warp(canvas, quad):
    """Lay a flat canvas onto a screen seen at an angle; returns the layer and its spot."""
    x0, y0 = int(min(x for x, _ in quad)), int(min(y for _, y in quad))
    x1, y1 = int(max(x for x, _ in quad)) + 1, int(max(y for _, y in quad)) + 1
    w, h = canvas.size
    rows = []
    for (x, y), (u, v) in zip(quad, [(0, 0), (w, 0), (w, h), (0, h)]):
        x, y = x - x0, y - y0
        rows += [([x, y, 1, 0, 0, 0, -u * x, -u * y], u), ([0, 0, 0, x, y, 1, -v * x, -v * y], v)]
    coeffs = np.linalg.solve([r for r, _ in rows], [b for _, b in rows])
    return canvas.transform((x1 - x0, y1 - y0), Image.PERSPECTIVE, coeffs.tolist(),
                            Image.BICUBIC), (x0, y0)


def mask_image(mask, box):
    x0, y0, x1, y1 = box
    return Image.fromarray((mask[y0:y1, x0:x1] * 255).astype(np.uint8))


def put(frame, layer, at, mask):
    """Paste an RGBA layer at `at`, only where the screen mask is set."""
    x0, y0 = at
    m = mask_image(mask, (x0, y0, x0 + layer.width, y0 + layer.height))
    frame.paste(layer.convert("RGB"), at, ImageChops.multiply(layer.getchannel("A"), m))


def glow_mask(w, h, cx, cy, sigma=70):
    mask = Image.new("L", (w, h))
    mask.putdata([int(255 * math.exp(-((x - cx) ** 2 + (y - cy) ** 2) / sigma ** 2))
                  for y in range(h) for x in range(w)])
    return mask


def screen_blend(img, mask, strength, color):
    glow = Image.merge("RGB", [mask.point(lambda v, c=c: v * strength * c / 255) for c in color])
    return ImageChops.screen(img, glow)


def blink(img, closed):
    """Lower an eyelid over each eye; closed runs from 0 (open) to 1 (shut)."""
    px = img.load()
    for cx, cy, rx, ry in EYES:
        curve = 0.3 * ry                     # the lid's edge bulges down
        side = cy - ry - curve + closed * (2 * ry + curve)
        for y in range(int(cy - ry), int(cy + ry) + 1):
            for x in range(int(cx - rx), int(cx + rx) + 1):
                u, v = (x - cx) / rx, (y - cy) / ry
                if u * u + v * v > 1:
                    continue
                edge = side + curve * (1 - u * u)
                if y < edge:
                    px[x, y] = SKIN
                elif y < edge + 8:
                    px[x, y] = INK
                # A shut eye shows the lid's edge as a curved line.
                lash = cy + 0.1 * ry + 0.25 * ry * (1 - u * u)
                if closed == 1 and abs(u) < 0.8 and abs(y - lash) < 4.5:
                    px[x, y] = INK


class Den:
    def __init__(self, logo):
        self.logo = logo
        self.masks = {}
        for name, (seeds, box, ok, tol) in SCREENS.items():
            mask = grow(logo, seeds, box, ok, tol)
            self.masks[name] = fill_rows(mask) if ok is DARK else mask
        # The dark screens, with their old text wiped by a min filter.
        self.wiped = {}
        for name in ("log", "health"):
            box = bbox(self.masks[name], 8)
            bg = logo.crop(box).filter(ImageFilter.MinFilter(5)).filter(ImageFilter.GaussianBlur(3))
            self.wiped[name] = (bg, box)
        self.quads = {name: corners(self.masks[name]) for name in ("log", "health")}
        x0, y0, x1, y1 = GLOW_BOX
        self.glow = glow_mask(x1 - x0, y1 - y0, LAMP[0] - x0, LAMP[1] - y0)
        label = np.asarray(logo.crop(LABEL)).astype(int)
        self.label = (label[..., 1] > label[..., 0] + 40) & (label[..., 1] > 110)
        self.keys = [k for box in KEYBOARDS
                     for k in parts(logo, box, lambda c: c[1] > 150 and c[2] > 130 and c[0] < 170)]
        self.bars = [b for box in DISPLAYS
                     for b in parts(logo, box, lambda c: c[1] > 190 and c[0] < 200, lo=6)]
        self.font = {s: ImageFont.truetype(MENLO, s) for s in (11, 12, 14, 18)}

    # -- the five monitors ----------------------------------------------------

    def dark_screen(self, frame, name, draw_canvas):
        bg, box = self.wiped[name]
        frame.paste(bg, box[:2], mask_image(self.masks[name], box))
        quad = self.quads[name]
        w = int(max(quad[1][0] - quad[0][0], quad[2][0] - quad[3][0]))
        h = int(max(quad[3][1] - quad[0][1], quad[2][1] - quad[1][1]))
        canvas = Image.new("RGBA", (w, h))
        draw_canvas(ImageDraw.Draw(canvas), w, h)
        # Text glows a little, like on a CRT.
        canvas = Image.alpha_composite(canvas.filter(ImageFilter.GaussianBlur(2)), canvas)
        layer, at = warp(canvas, quad)
        put(frame, layer, at, self.masks[name])

    def log(self, frame, n):
        shown = sum(1 for f in LOG_AT if f <= n)
        font, lh, pad, top = self.font[12], 15, 12, 18  # the top-left corner is rounded

        def draw(d, w, h):
            rows = (h - top - pad) // lh
            for i in range(rows):
                k = (shown - rows + 1 + i) % len(LOG)
                color, text = LOG[k]
                newest = i == rows - 1
                if newest and n in LOG_AT:
                    color = tuple(min(255, c + 60) for c in color)
                y = top + i * lh
                d.text((pad, y), text, font=font, fill=color + (255,))
                if newest and n % 4 < 2:     # block cursor after the newest line
                    x = pad + d.textlength(text, font=font) + 3
                    d.rectangle((x, y + 2, x + 6, y + 13), fill=GREEN + (255,))

        self.dark_screen(frame, "log", draw)

    def health(self, frame, n):
        font, lh, pad = self.font[12], 16, 15
        row, step = divmod(n, FRAMES // len(HEALTH))

        def draw(d, w, h):
            d.text((pad, pad), "HEALTH", font=font, fill=(150, 255, 200, 255))
            d.text((w - pad, pad), f"{len(HEALTH)}/{len(HEALTH)}", font=font,
                   fill=DIM + (255,), anchor="ra")
            for i, (svc, ms) in enumerate(HEALTH):
                y = pad + (i + 1) * lh + 4
                status, color = ms, GREEN
                if i == row and step < 3:
                    status, color = "..", WARN
                elif i == row:
                    d.rectangle((pad - 3, y - 1, w - pad + 3, y + lh - 3), fill=(40, 120, 80, 150))
                d.text((pad, y), svc, font=font, fill=DIM + (255,))
                d.text((w - pad, y), status, font=font, fill=color + (255,), anchor="ra")

        self.dark_screen(frame, "health", draw)

    def prompt(self, frame, n):
        mask = self.masks["prompt"]
        x0, y0, x1, y1 = bbox(mask)
        layer = Image.new("RGBA", (x1 - x0, y1 - y0))
        d = ImageDraw.Draw(layer)
        ink, small = (235, 255, 250, 235), self.font[14]
        px, py = PROMPT[0] - x0, PROMPT[1] - y0

        def cursor(x, y):
            d.rectangle((x, y + 1, x + 10, y + 3), fill=ink)

        original = n < 6 or n >= 34
        if not (original and n % 6 < 3):    # wipe the drawn "_" with the screen below it
            cx0, cy0, cx1, cy1 = CURSOR
            frame.paste(frame.crop((cx0, cy0 + 9, cx1, cy1 + 9)), (cx0, cy0))
        if not original:
            typed = TYPED[:n - 5]
            d.text((px + 17, py), typed, font=small, fill=ink, anchor="ls")
            if n < 18:
                if n < 16 or n % 2:
                    cursor(px + 19 + d.textlength(typed, font=small), py)
            else:
                d.text((px, py + 19), "9/9 ok", font=small, fill=ink, anchor="ls")
                d.text((px, py + 38), ">", font=self.font[18], fill=ink, anchor="ls")
                if n % 6 < 3:
                    cursor(px + 15, py + 38)
        put(frame, layer, (x0, y0), mask)

    def chart(self, frame, n):
        mask = self.masks["chart"]
        x0, y0, x1, y1 = bbox(mask)
        layer = Image.new("RGBA", (x1 - x0, y1 - y0))
        d = ImageDraw.Draw(layer)
        w = x1 - x0
        shift = 2 * n                        # 80 px per loop, the period of the data

        def traffic(x):
            t = 2 * math.pi * (x + shift) / 80
            spike = 26 * math.exp(-(((x + shift) % 80 - 50) / 4) ** 2)
            return 14 * math.sin(t) + 8 * math.sin(2 * t + 1) + 5 * math.sin(5 * t + 2) + spike

        top, base = 30, 105
        for y in range(top, base + 1, 25):
            d.line((0, y, w, y), fill=(0, 120, 125, 60))
        pts = [(x, base - 32 - traffic(x)) for x in range(0, w + 1, 2)]
        d.polygon(pts + [(w, base), (0, base)], fill=(0, 140, 150, 70))
        d.line(pts, fill=(0, 90, 100, 235), width=3)
        d.text((4, top - 16), "rx", font=self.font[11], fill=(0, 90, 100, 220))
        heights = [22, 35, 18, 48, 30, 26, 55, 20, 38, 28]
        for i in range(-1, w // 8 + 2):
            x = i * 8 - shift % 8
            hgt = heights[(i + shift // 8) % len(heights)]
            d.rectangle((x, 205 - hgt, x + 5, 205), fill=(0, 105, 115, 170))
        d.text((4, 130), "ms", font=self.font[11], fill=(0, 90, 100, 220))
        put(frame, layer, (x0, y0), mask)

    def meter(self, frame, n):
        mask = self.masks["meter"]
        x0, y0, x1, y1 = bbox(mask)
        layer = Image.new("RGBA", (x1 - x0, y1 - y0))
        d = ImageDraw.Draw(layer)
        t = 2 * math.pi * n / FRAMES
        level = 6 + 3.5 * math.sin(2 * t) + 2 * math.sin(5 * t + 1)
        segments, top, bottom = 12, 12, y1 - y0 - 10
        step = (bottom - top) / segments
        for i in range(segments):
            y = bottom - (i + 1) * step
            color = (40, 200, 110) if i < 8 else (235, 190, 60) if i < 10 else (235, 80, 70)
            alpha = 235 if i < level else 45
            d.rectangle((0, y + 2, x1 - x0, y + step - 2), fill=color + (alpha,))
        put(frame, layer, (x0, y0), mask)

    # -- extras -----------------------------------------------------------------

    def shimmer(self, arr, n):
        """Each screen's brightness wavers and a soft bright band rolls down it."""
        for k, mask in enumerate(self.masks.values()):
            x0, y0, x1, y1 = bbox(mask)
            sub = arr[y0:y1, x0:x1].astype(float)
            m = mask[y0:y1, x0:x1]
            h = y1 - y0
            centre = ((n / FRAMES + k * 0.37) % 1) * (h + 60) - 30
            band = 14 * np.exp(-((np.arange(h) - centre) / 10) ** 2)[:, None, None]
            out = sub * (1 + random.Random(n * 7 + k).uniform(-0.04, 0.04)) + band
            sub[m] = out[m]
            arr[y0:y1, x0:x1] = np.clip(sub, 0, 255)

    def console(self, arr, n):
        """Keys light up as if someone types, and display bars blink like LEDs."""
        rng = random.Random(n)
        busy = 6 <= n < 16                   # while the prompt is being typed
        lit = rng.sample(range(len(self.keys)), 2 if busy else int(rng.random() < 0.4))
        for i in lit:
            ys, xs = self.keys[i]
            arr[ys, xs] = arr[ys, xs] * 0.35 + np.array([210, 255, 245]) * 0.65
        for i, (ys, xs) in enumerate(self.bars):
            if random.Random(i * 97 + n // 4).random() < 0.3:
                arr[ys, xs] = arr[ys, xs] * 0.45

    def root_access(self, frame, n):
        if not (8 <= n < 30 and n != 9):     # flickers on, stays lit 2 s
            return frame
        x0, y0, x1, y1 = LABEL
        arr = np.asarray(frame).copy()
        sub = arr[y0:y1, x0:x1]
        sub[self.label] = (205, 255, 205)
        frame = Image.fromarray(arr)
        pad = 10
        box = (x0 - pad, y0 - pad, x1 + pad, y1 + pad)
        text = Image.new("L", (x1 - x0 + 2 * pad, y1 - y0 + 2 * pad))
        text.paste(Image.fromarray((self.label * 255).astype(np.uint8)), (pad, pad))
        halo = text.filter(ImageFilter.GaussianBlur(5))
        frame.paste(screen_blend(frame.crop(box), halo, 0.9, (60, 255, 120)), box[:2])
        return frame

    # -- one frame --------------------------------------------------------------

    def render(self, n, size=SIZE):
        frame = self.logo.copy()
        self.log(frame, n)
        self.health(frame, n)
        self.prompt(frame, n)
        self.chart(frame, n)
        self.meter(frame, n)
        arr = np.asarray(frame).copy()
        self.shimmer(arr, n)
        self.console(arr, n)
        frame = self.root_access(Image.fromarray(arr.astype(np.uint8)), n)

        lid = {28: 0.55, 29: 1.0, 30: 0.55}.get(n, 0)
        if lid:
            blink(frame, lid)
        light = 0.15 + 0.65 * (0.5 - 0.5 * math.cos(2 * math.pi * n / FRAMES))
        if n in (13, 15):
            light *= 0.2                     # flicker
        frame.paste(screen_blend(frame.crop(GLOW_BOX), self.glow, light, (255, 236, 170)),
                    GLOW_BOX[:2])
        return frame.resize((size, size), Image.LANCZOS)


def send(keys, payload=b""):
    """Write one kitty graphics command, split into chunks of at most 4096 bytes."""
    data = base64.b64encode(payload).decode()
    if len(data) <= CHUNK:
        sys.stdout.write(f"\x1b_G{keys};{data}\x1b\\")
        return
    # Follow-up chunks carry only m (and q); frame data must also repeat a=f.
    cont = "a=f,q=2" if keys.startswith("a=f") else "q=2"
    for i in range(0, len(data), CHUNK):
        more = int(i + CHUNK < len(data))
        head = keys if i == 0 else cont
        sys.stdout.write(f"\x1b_G{head},m={more};{data[i:i + CHUNK]}\x1b\\")


def png(img):
    buf = io.BytesIO()
    img.save(buf, "PNG")
    return buf.getvalue()


def play(den, cols):
    k = den.logo.width // SIZE
    boxes = [tuple(c // k for c in r) for r in REGIONS]
    root = den.render(0)
    img = random.randint(1, 2**31 - 1)  # fresh id, so older animations keep running
    send(f"a=t,i={img},f=100,q=2", png(root))
    for n in range(1, FRAMES):
        frame = den.render(n)
        changed = [b for b in boxes if frame.crop(b).tobytes() != root.crop(b).tobytes()]
        for i, b in enumerate(changed or boxes[:1]):
            # The first patch makes frame n+1 from frame 1 (c=1); the rest edit it (r=).
            base = "c=1" if i == 0 else f"r={n + 1}"
            send(f"a=f,i={img},f=100,{base},x={b[0]},y={b[1]},X=1,z={GAP},q=2",
                 png(frame.crop(b)))
    send(f"a=a,i={img},r=1,z={GAP},q=2")    # the root frame's gap defaults to 0
    send(f"a=p,i={img},c={cols},q=2")
    send(f"a=a,i={img},s=3,v=1,q=2")        # play, loop forever
    sys.stdout.write("\n")
    sys.stdout.flush()


def save_avif(den, path):
    frames = [den.render(n, README_SIZE) for n in range(FRAMES)]
    frames[0].save(path, save_all=True, append_images=frames[1:], duration=GAP, loop=0,
                   quality=70, speed=6)
    print(f"saved {path}")


if __name__ == "__main__":
    den = Den(load_logo())
    if sys.argv[1:2] == ["--avif"]:
        save_avif(den, sys.argv[2])
    else:
        play(den, int(sys.argv[1]) if len(sys.argv) > 1 else 40)
