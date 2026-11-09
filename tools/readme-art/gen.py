#!/usr/bin/env python3
"""Draws the README's animated pictures into docs/readme/.

GitHub shows an SVG in a README as an image: its CSS and SMIL animations run, but it may not load anything
from outside, so the mascot is embedded as a data URI and the text uses the viewer's system fonts. Every
picture is complete without animation (a viewer that does not play it sees the whole thing), and motion
stops for viewers who ask for reduced motion.

    python3 tools/readme-art/gen.py

The numbers on the cards are counted from the code (see NUMBERS); change them when the product changes,
run this, and commit the SVGs.
"""

import base64
import pathlib
from xml.sax.saxutils import escape as esc

ROOT = pathlib.Path(__file__).resolve().parents[2]
OUT = ROOT / "docs" / "readme"
SANS = "-apple-system, 'Segoe UI', Inter, Helvetica, Arial, sans-serif"
MONO = "'SF Mono', SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace"
FA = "Vazirmatn, Tahoma, 'Geeza Pro', 'Noto Naskh Arabic', 'Segoe UI', sans-serif"
REDUCED = "@media (prefers-reduced-motion: reduce) { * { animation: none !important; } }"
BASE_CSS = f".sans {{ font-family: {SANS}; }} .mono {{ font-family: {MONO}; }} .fa {{ font-family: {FA}; }}"

VERSION = "0.24.1"

# Counted from the code on 2026-11-01 (v0.21.0).
NUMBERS = {
    "go_tests": "3,103", "web_tests": "1,464", "go_lines": "150K", "rpcs": "129", "routes": "294",
    "migrations": "70", "events": "21", "pages": "33", "bot_texts": "1,142", "alerts": "42",
    "runbooks": "17", "ci_jobs": "14", "releases": "32", "settings": "94",
}

CYAN, BLUE, PURPLE, PINK, ORANGE, AMBER, GREEN, TEAL = "#22d3ee", "#3b82f6", "#a855f7", "#ec4899", "#f97316", "#f59e0b", "#22c55e", "#14b8a6"


def write(name, svg):
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / name).write_text(svg.strip() + "\n", encoding="utf-8")
    print(f"docs/readme/{name}: {len(svg) // 1024} KB")


def svg(w, h, label, css, body, defs=""):
    return f'''<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="{esc(label, {'"': '&quot;'})}">
<style>{BASE_CSS}
{css}
{REDUCED}</style>
<defs>{defs}</defs>
{body}
</svg>'''


def frame(w, h, c1, c2, uid):
    """A dark rounded card with two coloured glows, a faint dot grid and a running gradient edge."""
    return (f'''
  <linearGradient id="edge{uid}" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="{c1}"/><stop offset="1" stop-color="{c2}"/></linearGradient>
  <radialGradient id="ga{uid}" cx=".12" cy=".1" r=".7"><stop offset="0" stop-color="{c1}" stop-opacity=".32"/><stop offset="1" stop-color="{c1}" stop-opacity="0"/></radialGradient>
  <radialGradient id="gb{uid}" cx=".92" cy=".95" r=".6"><stop offset="0" stop-color="{c2}" stop-opacity=".30"/><stop offset="1" stop-color="{c2}" stop-opacity="0"/></radialGradient>
  <pattern id="dots{uid}" width="22" height="22" patternUnits="userSpaceOnUse"><circle cx="2" cy="2" r="1" fill="#94a3b8" opacity=".12"/></pattern>
  <clipPath id="clip{uid}"><rect x="10" y="10" width="{w - 20}" height="{h - 20}" rx="28"/></clipPath>''',
            f'''
<g clip-path="url(#clip{uid})">
  <rect x="10" y="10" width="{w - 20}" height="{h - 20}" fill="#0a0e1a"/>
  <rect x="10" y="10" width="{w - 20}" height="{h - 20}" fill="url(#dots{uid})"/>
  <rect x="10" y="10" width="{w - 20}" height="{h - 20}" fill="url(#ga{uid})"/>
  <rect x="10" y="10" width="{w - 20}" height="{h - 20}" fill="url(#gb{uid})"/>
</g>
<rect x="10.75" y="10.75" width="{w - 21.5}" height="{h - 21.5}" rx="27.5" fill="none" stroke="url(#edge{uid})" stroke-opacity=".55" stroke-width="1.5"/>
<rect class="run" x="10.75" y="10.75" width="{w - 21.5}" height="{h - 21.5}" rx="27.5" fill="none" stroke="url(#edge{uid})" stroke-width="2.5" stroke-dasharray="80 920" pathLength="1000"/>''')


FRAME_CSS = ".run { animation: run 9s linear infinite; } @keyframes run { to { stroke-dashoffset: -1000; } }"


def pill(x, y, text, color, live=True):
    w = 26 + len(text) * 9.3 + (14 if live else 0)
    dot = f'<circle class="live" cx="{x + 16}" cy="{y}" r="4.5" fill="{color}"/>' if live else ""
    tx = x + (30 if live else 13)
    return (f'<rect x="{x}" y="{y - 14}" width="{w:.0f}" height="28" rx="14" fill="#0b1220" stroke="{color}" stroke-opacity=".7"/>{dot}'
            f'<text class="mono" x="{tx}" y="{y + 4.5}" font-size="12.5" font-weight="700" letter-spacing="1.5" fill="{color}">{esc(text)}</text>'), w


PILL_CSS = ".live { animation: live 1.6s ease-in-out infinite; } @keyframes live { 50% { opacity: .25; } }"


def chips(x, y, items):
    out, cx = [], x
    for t in items:
        w = 22 + len(t) * 7.6
        out.append(f'<rect x="{cx:.0f}" y="{y}" width="{w:.0f}" height="28" rx="14" fill="#0f172a" stroke="#334155"/>'
                   f'<text class="mono" x="{cx + w / 2:.0f}" y="{y + 18.5}" font-size="12.5" fill="#cbd5e1" text-anchor="middle">{esc(t)}</text>')
        cx += w + 10
    return "".join(out)


# ---------------------------------------------------------------- hero

def hero():
    w, h = 1280, 420
    defs, frm = frame(w, h, CYAN, PINK, "h")
    streaks = []
    spec = [(70, 300, 7), (95, 220, 9), (120, 340, 6), (330, 260, 8), (346, 180, 5.5),
            (362, 300, 10), (376, 140, 7.5), (388, 240, 6.5), (398, 200, 9.5)]
    for i, (y, length, dur) in enumerate(spec):
        streaks.append(f'<rect class="streak" style="animation-duration:{dur}s;animation-delay:-{(i * 1.7) % dur:.1f}s" x="{(i * 263) % 1100}" y="{y}" width="{length}" height="2.5" rx="1.25" fill="url(#st{i % 3})"/>')
    corners = "".join(f'<path d="{d}" fill="none" stroke="{CYAN}" stroke-width="3" stroke-linecap="round"/>' for d in (
        "M40 66 V40 H66", f"M{w - 66} 40 H{w - 40} V66", f"M40 {h - 66} V{h - 40} H66", f"M{w - 66} {h - 40} H{w - 40} V{h - 66}"))
    p, pw = pill(0, 0, f"LIVE  v{VERSION} · STABLE", GREEN)
    css = f'''{FRAME_CSS} {PILL_CSS}
.streak {{ animation: streak 8s linear infinite; }}
@keyframes streak {{ from {{ transform: translateX(1300px); }} to {{ transform: translateX(-1400px); }} }}
.glow {{ animation: glow 4s ease-in-out infinite; }} @keyframes glow {{ 50% {{ opacity: .45; }} }}'''
    defs += f'''
  <linearGradient id="title" x1="0" x2="1"><stop offset="0" stop-color="{CYAN}"/><stop offset=".5" stop-color="{PURPLE}"/><stop offset="1" stop-color="{PINK}"/></linearGradient>
  <linearGradient id="st0" x1="0" x2="1"><stop offset="0" stop-color="{CYAN}" stop-opacity="0"/><stop offset="1" stop-color="{CYAN}"/></linearGradient>
  <linearGradient id="st1" x1="0" x2="1"><stop offset="0" stop-color="{PINK}" stop-opacity="0"/><stop offset="1" stop-color="{PINK}"/></linearGradient>
  <linearGradient id="st2" x1="0" x2="1"><stop offset="0" stop-color="{PURPLE}" stop-opacity="0"/><stop offset="1" stop-color="{PURPLE}"/></linearGradient>
  <filter id="blur" x="-20%" y="-50%" width="140%" height="200%"><feGaussianBlur stdDeviation="14"/></filter>'''
    body = f'''{frm}
<g clip-path="url(#cliph)">{"".join(streaks)}</g>
{corners}
<text class="mono" x="70" y="76" font-size="14" fill="#64748b">~/sobhanaz/bobres-3x-ui-telegram-panel</text>
<g transform="translate({w - 70 - pw:.0f} 70)">{p}</g>
<text class="fa" x="640" y="128" font-size="30" font-weight="700" fill="#e2e8f0" text-anchor="middle" direction="rtl">بابرس · فروشگاه VPN شما در تلگرام</text>
<text class="sans glow" x="640" y="232" font-size="118" font-weight="900" letter-spacing="6" fill="url(#title)" text-anchor="middle" filter="url(#blur)" opacity=".8">BOBRES</text>
<text class="sans" x="640" y="232" font-size="118" font-weight="900" letter-spacing="6" fill="url(#title)" text-anchor="middle">BOBRES</text>
<text class="mono" x="640" y="272" font-size="15" letter-spacing="5" fill="#94a3b8" text-anchor="middle">TELEGRAM  ·  3X-UI  ·  MARZBAN  ·  S-UI  ·  HIDDIFY</text>
<text class="sans" x="640" y="314" font-size="26" font-weight="600" fill="#f1f5f9" text-anchor="middle">Launch your own VPN store on Telegram, in one command.</text>'''
    return svg(w, h, f"BOBRES: launch your own VPN store on Telegram, in one command. Live v{VERSION}.", css, body, defs)


# ---------------------------------------------------------------- whoami: the beaver and a terminal

def terminal(x, y, w, h, title, lines, uid):
    out = [f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="16" fill="#0b0f19" stroke="#1f2937"/>',
           f'<rect x="{x}" y="{y}" width="{w}" height="36" rx="16" fill="#111827"/><rect x="{x}" y="{y + 20}" width="{w}" height="16" fill="#111827"/>',
           "".join(f'<circle cx="{x + 22 + i * 18}" cy="{y + 18}" r="6" fill="{c}"/>' for i, c in enumerate(("#ef4444", "#f59e0b", "#22c55e"))),
           f'<text class="mono" x="{x + w / 2}" y="{y + 23}" font-size="12" fill="#64748b" text-anchor="middle">{esc(title)}</text>']
    ly = y + 66
    for kind, text in lines:
        if kind == "cmd":
            out.append(f'<text class="mono" x="{x + 24}" y="{ly}" font-size="15" xml:space="preserve"><tspan fill="{CYAN}" font-weight="700">❯ </tspan><tspan fill="#f1f5f9">{esc(text)}</tspan></text>')
        elif kind == "ok":
            out.append(f'<text class="mono" x="{x + 44}" y="{ly}" font-size="14"><tspan fill="{GREEN}">✓ </tspan><tspan fill="#94a3b8">{esc(text)}</tspan></text>')
        elif kind == "fa":
            out.append(f'<text class="fa" x="{x + 44}" y="{ly}" font-size="17" font-weight="700" fill="url(#fag{uid})">{esc(text)}</text>')
        elif kind == "caret":
            out.append(f'<text class="mono" x="{x + 24}" y="{ly}" font-size="15"><tspan fill="{CYAN}" font-weight="700">❯ </tspan><tspan class="caret" fill="{CYAN}">▍</tspan></text>')
        elif kind == "out":
            out.append(f'<text class="mono" x="{x + 24}" y="{ly}" font-size="14" fill="#94a3b8" xml:space="preserve">{esc(text)}</text>')
        ly += 25 if kind != "gap" else 10
    return "".join(out)


TERM_CSS = ".caret { animation: blink 1s step-end infinite; } @keyframes blink { 50% { opacity: 0; } }"


def whoami():
    w, h = 1280, 440
    defs, frm = frame(w, h, TEAL, PINK, "w")
    mascot = base64.b64encode((ROOT / "docs" / "brand" / "bobres-mascot.webp").read_bytes()).decode()
    defs += f'''
  <clipPath id="portal"><circle cx="250" cy="215" r="150"/></clipPath>
  <radialGradient id="halo"><stop offset="0" stop-color="{TEAL}" stop-opacity=".45"/><stop offset="1" stop-color="{TEAL}" stop-opacity="0"/></radialGradient>
  <linearGradient id="fagw" x1="0" x2="1"><stop offset="0" stop-color="{PURPLE}"/><stop offset="1" stop-color="{PINK}"/></linearGradient>'''
    css = f'''{FRAME_CSS} {TERM_CSS}
.orbit {{ animation: spin 30s linear infinite; transform-origin: 250px 215px; }} @keyframes spin {{ to {{ transform: rotate(360deg); }} }}
.float {{ animation: float 5s ease-in-out infinite; }} @keyframes float {{ 50% {{ transform: translateY(-10px); }} }}'''
    lines = [("cmd", "bash <(curl -fsSL …/install/install.sh)"), ("ok", f"bobres v{VERSION} · signature and checksum verified"),
             ("ok", "3x-ui token checked against the real panel"), ("ok", "caddy · core · bot · payments · provisioner healthy"),
             ("gap", ""), ("cmd", "sudo bobres status"), ("out", "  store running (9/9) · license Pro · backup 6 h ago"),
             ("gap", ""), ("cmd", "echo $MOTTO"), ("fa", "فروشگاه خودت، روی سرور خودت"), ("caret", "")]
    body = f'''{frm}
<circle cx="250" cy="215" r="200" fill="url(#halo)"/>
<circle class="orbit" cx="250" cy="215" r="178" fill="none" stroke="{TEAL}" stroke-opacity=".7" stroke-width="2" stroke-dasharray="2 10" stroke-linecap="round"/>
<g class="float">
  <image clip-path="url(#portal)" x="80" y="45" width="340" height="340" preserveAspectRatio="xMidYMid slice"
         href="data:image/webp;base64,{mascot}" xlink:href="data:image/webp;base64,{mascot}"/>
  <circle cx="250" cy="215" r="150" fill="none" stroke="{TEAL}" stroke-width="3"/>
</g>
<g transform="translate(160 384)"><rect width="180" height="30" rx="15" fill="#0b1220" stroke="{PINK}"/><text class="mono" x="90" y="20" font-size="13" font-weight="700" letter-spacing="1.5" fill="{PINK}" text-anchor="middle">BOBRES · SHIELD UP</text></g>
{terminal(480, 46, 760, 348, "root@shop — zsh", lines, "w")}'''
    return svg(w, h, "BOBRES the beaver next to a terminal: the one-line install verifies the signature, checks the panel token and starts healthy services.", css, body, defs)


# ---------------------------------------------------------------- section headers

def header(num, title, fa, c1, c2):
    w, h = 1280, 80
    defs = f'''<linearGradient id="t" x1="0" x2="1"><stop offset="0" stop-color="{c1}"/><stop offset="1" stop-color="{c2}"/></linearGradient>
  <linearGradient id="ln" x1="{int(len(title) * 25 + 96)}" x2="1080" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="{c2}"/><stop offset="1" stop-color="{c2}" stop-opacity="0"/></linearGradient>'''
    tw = int(len(title) * 25 + 96)
    travel = 1060 - tw - 100
    css = f".dot {{ animation: travel 4s ease-in-out infinite; }} @keyframes travel {{ 50% {{ transform: translateX({travel}px); }} }}"
    body = f'''<text class="mono" x="20" y="51" font-size="17" fill="{c1}">/{num}</text>
<text class="sans" x="70" y="53" font-size="34" font-weight="900" letter-spacing="1.5" fill="url(#t)">{esc(title)}</text>
<line x1="{tw}" y1="41" x2="1080" y2="41" stroke="url(#ln)" stroke-width="2.5"/>
<circle cx="{tw}" cy="41" r="5" fill="{c2}"/>
<circle class="dot" cx="{tw + 60}" cy="41" r="3.5" fill="{c1}"/>
<text class="fa" x="1260" y="50" font-size="22" font-weight="700" fill="#94a3b8" text-anchor="end">{esc(fa)}</text>'''
    return svg(w, h, f"{num} {title}", css, body, defs)


# ---------------------------------------------------------------- the big cards

def b(t):
    return f'<tspan font-weight="700" fill="#f8fafc">{esc(t)}</tspan>'


def plain(parts):
    return " ".join(parts).replace('<tspan font-weight="700" fill="#f8fafc">', "").replace("</tspan>", "")


def card(uid, num, kicker, title, pill_text, desc, stats, chip_list, c1, c2, right, right_css=""):
    w, h = 1280, 450
    defs, frm = frame(w, h, c1, c2, uid)
    defs += f'<linearGradient id="tt{uid}" x1="0" x2="1"><stop offset="0" stop-color="{c1}"/><stop offset="1" stop-color="{c2}"/></linearGradient>'
    live = pill_text.startswith("LIVE")
    p, pw = pill(0, 0, pill_text, c1 if live else c2, live)
    dl = "".join(f'<text class="sans" x="60" y="{214 + i * 29}" font-size="19" fill="#cbd5e1">{t}</text>' for i, t in enumerate(desc))
    st = "".join(f'<text class="sans" x="{60 + i * 205}" y="334" font-size="40" font-weight="900" fill="#f8fafc">{esc(n)}</text>'
                 f'<text class="mono" x="{62 + i * 205}" y="358" font-size="11.5" letter-spacing="2" fill="#64748b">{esc(l)}</text>' for i, (n, l) in enumerate(stats))
    body = f'''{frm}
<text class="mono" x="60" y="74" font-size="13" letter-spacing="2.5" xml:space="preserve"><tspan fill="{c1}" font-weight="700">{num}</tspan><tspan fill="#64748b">   {esc(kicker)}</tspan></text>
<text class="sans" x="56" y="158" font-size="70" font-weight="900" fill="url(#tt{uid})">{esc(title)}</text>
<g transform="translate({690 - pw:.0f} 70)">{p}</g>
{dl}
{st}
{chips(60, 382, chip_list)}
{right}'''
    return svg(w, h, f"{title}: {plain(desc)}", f"{FRAME_CSS} {PILL_CSS} {right_css}", body, defs)


def pipeline(steps, c1, c2, x=740, box_h=62, gap=18):
    n = len(steps)
    total = n * box_h + (n - 1) * gap
    y0 = 225 - total / 2
    out = []
    for i, (icon, title, sub, fa) in enumerate(steps):
        y = y0 + i * (box_h + gap)
        col = c1 if i < n / 2 else c2
        out.append(f'''<g class="step" style="animation-delay:-{i * 0.6:.1f}s">
  <rect x="{x}" y="{y}" width="490" height="{box_h}" rx="14" fill="#0b1020" stroke="{col}" stroke-opacity=".75" stroke-width="1.5"/>
  <rect x="{x + 12}" y="{y + (box_h - 40) / 2}" width="40" height="40" rx="10" fill="{col}" fill-opacity=".14" stroke="{col}" stroke-opacity=".5"/>
  <text x="{x + 32}" y="{y + box_h / 2 + 7}" font-size="20" text-anchor="middle">{icon}</text>
  <text class="sans" x="{x + 66}" y="{y + box_h / 2 - 4}" font-size="17" font-weight="700" fill="#f1f5f9">{esc(title)}</text>
  <text class="mono" x="{x + 66}" y="{y + box_h / 2 + 16}" font-size="12" fill="#64748b">{esc(sub)}</text>
  <text class="fa" x="{x + 474}" y="{y + box_h / 2 + 6}" font-size="15" font-weight="700" fill="{col}" text-anchor="end">{esc(fa)}</text>
</g>''')
        if i < n - 1:
            cy = y + box_h
            out.append(f'<line x1="{x + 32}" y1="{cy}" x2="{x + 32}" y2="{cy + gap}" stroke="{col}" stroke-opacity=".5" stroke-width="2" stroke-dasharray="3 4"/>'
                       f'<circle class="flowdot" style="animation-delay:-{i * 0.4:.1f}s" cx="{x + 32}" cy="{cy + 3}" r="3.5" fill="{col}"/>')
    css = (f".flowdot {{ animation: drop 1.6s ease-in infinite; }} @keyframes drop {{ 0% {{ transform: translateY(0); opacity: 1; }} 100% {{ transform: translateY({gap - 6}px); opacity: .2; }} }}"
           " .step { animation: glowstep 4.8s ease-in-out infinite; } @keyframes glowstep { 50% { opacity: .78; } }")
    return "".join(out), css


def phone(c1, c2):
    x, y = 860, 34
    bubbles = [("bot", "🛒  Choose a plan"), ("btn", "30 days · 50 GB   180,000 T"), ("btn", "90 days · 150 GB   480,000 T"),
               ("user", "Paid with the wallet ✓"), ("bot", "✅ Your service is ready"), ("link", "vless://…  ·  QR  ·  page")]
    out = [f'<defs><linearGradient id="ph" x1="0" x2="1"><stop offset="0" stop-color="{c1}"/><stop offset="1" stop-color="{c2}"/></linearGradient></defs>',
           f'<rect x="{x}" y="{y}" width="250" height="384" rx="36" fill="#05070d" stroke="#334155" stroke-width="2"/>',
           f'<rect x="{x + 95}" y="{y + 12}" width="60" height="8" rx="4" fill="#1f2937"/>',
           f'<rect x="{x + 12}" y="{y + 32}" width="226" height="40" rx="10" fill="#0f172a"/>',
           f'<circle cx="{x + 34}" cy="{y + 52}" r="12" fill="url(#ph)"/>',
           f'<text class="sans" x="{x + 54}" y="{y + 50}" font-size="13" font-weight="700" fill="#f1f5f9">Your Store</text>',
           f'<text class="sans" x="{x + 54}" y="{y + 65}" font-size="10.5" fill="{GREEN}">bot · online</text>']
    by = y + 92
    for kind, text in bubbles:
        if kind == "bot":
            out.append(f'<rect x="{x + 14}" y="{by}" width="186" height="34" rx="12" fill="#1e293b"/><text class="sans" x="{x + 26}" y="{by + 22}" font-size="12.5" fill="#e2e8f0">{esc(text)}</text>')
            by += 42
        elif kind == "btn":
            out.append(f'<rect class="btn" x="{x + 14}" y="{by}" width="222" height="30" rx="9" fill="{c1}" fill-opacity=".16" stroke="{c1}" stroke-opacity=".6"/><text class="sans" x="{x + 125}" y="{by + 20}" font-size="11.5" font-weight="600" fill="#e0f2fe" text-anchor="middle" xml:space="preserve">{esc(text)}</text>')
            by += 36
        elif kind == "user":
            out.append(f'<rect x="{x + 70}" y="{by + 4}" width="166" height="32" rx="12" fill="url(#ph)"/><text class="sans" x="{x + 228}" y="{by + 25}" font-size="12.5" font-weight="600" fill="#fff" text-anchor="end">{esc(text)}</text>')
            by += 46
        else:
            out.append(f'<rect x="{x + 14}" y="{by}" width="186" height="30" rx="10" fill="#0f172a" stroke="{GREEN}" stroke-opacity=".6"/><text class="mono" x="{x + 26}" y="{by + 20}" font-size="11" fill="{GREEN}" xml:space="preserve">{esc(text)}</text>')
            by += 38
    css = ".btn { animation: tap 3s ease-in-out infinite; } @keyframes tap { 45%, 55% { fill-opacity: .4; } }"
    return "".join(out), css


def browser(c1, c2):
    x, y, w, h = 740, 40, 490, 370
    bars = [62, 88, 74, 120, 96, 140, 118, 164]
    bw = 30
    out = [f'<defs><linearGradient id="bars" x1="0" y1="1" x2="0" y2="0"><stop offset="0" stop-color="{c2}"/><stop offset="1" stop-color="{c1}"/></linearGradient></defs>',
           f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="16" fill="#0b0f19" stroke="#1f2937"/>',
           f'<rect x="{x}" y="{y}" width="{w}" height="38" rx="16" fill="#111827"/><rect x="{x}" y="{y + 22}" width="{w}" height="16" fill="#111827"/>',
           "".join(f'<circle cx="{x + 22 + i * 18}" cy="{y + 19}" r="6" fill="{c}"/>' for i, c in enumerate(("#ef4444", "#f59e0b", "#22c55e"))),
           f'<rect x="{x + 90}" y="{y + 9}" width="{w - 120}" height="20" rx="10" fill="#0b0f19"/><text class="mono" x="{x + w / 2 + 15}" y="{y + 23}" font-size="11.5" fill="#64748b" text-anchor="middle">shop.example.com/admin</text>',
           f'<rect x="{x}" y="{y + 38}" width="110" height="{h - 38}" fill="#0d1322"/>']
    for i, t in enumerate(["Overview", "Services", "Payments", "Reports", "Resellers", "Servers", "System"]):
        sel = i == 3
        out.append(f'<rect x="{x + 10}" y="{y + 54 + i * 34}" width="90" height="26" rx="7" fill="{c1 if sel else "#0d1322"}" fill-opacity="{.22 if sel else 1}"/>'
                   f'<text class="sans" x="{x + 22}" y="{y + 72 + i * 34}" font-size="12" fill="{"#f8fafc" if sel else "#64748b"}">{t}</text>')
    kx = x + 126
    for i, (n, l, c) in enumerate([("12.4M T", "revenue · week", c1), ("318", "new services", c2), ("97%", "renewed on time", GREEN)]):
        out.append(f'<rect x="{kx + i * 118}" y="{y + 54}" width="108" height="66" rx="10" fill="#111827"/>'
                   f'<text class="sans" x="{kx + 10 + i * 118}" y="{y + 84}" font-size="19" font-weight="800" fill="{c}">{n}</text>'
                   f'<text class="sans" x="{kx + 10 + i * 118}" y="{y + 104}" font-size="10.5" fill="#64748b">{l}</text>')
    base = y + h - 30
    for i, bh in enumerate(bars):
        bx = kx + 8 + i * (bw + 12)
        out.append(f'<rect class="bar" style="animation-delay:-{i * 0.35:.2f}s;transform-origin:{bx + bw / 2}px {base}px" x="{bx}" y="{base - bh}" width="{bw}" height="{bh}" rx="6" fill="url(#bars)"/>')
    out.append(f'<line x1="{kx}" y1="{base}" x2="{x + w - 16}" y2="{base}" stroke="#334155"/>')
    css = ".bar { animation: grow 3.2s ease-in-out infinite; } @keyframes grow { 50% { transform: scaleY(.72); } }"
    return "".join(out), css


def cli_terminal():
    lines = [("cmd", "sudo bobres"), ("out", f"   n____n   BOBRES manager  bobres v{VERSION}"), ("out", "  ( o  o )  Store   running (9/9 services)"),
             ("out", "   \\_||_/   License Pro · HTTPS auto · backup 6 h"), ("gap", ""), ("out", "   1. Status         10. Back up now"),
             ("out", "   2. Live logs      13. Update BOBRES"), ("out", "   7. Bot token      19. HTTPS certificate"), ("caret", "")]
    return terminal(740, 40, 490, 370, "root@shop — bobres", lines, "c"), TERM_CSS


def products():
    cards = []
    r, rc = phone(CYAN, BLUE)
    cards.append(("product-store", card("p1", "01", "CUSTOMER STORE · TELEGRAM", "Bot + Mini App", "LIVE  IN TELEGRAM",
        [f"Customers {b('buy, pay, renew and get support')} without", "leaving Telegram. Plans in groups, a wallet, a free trial,",
         "codes, invite links and a branded page for every service."],
        [(NUMBERS["bot_texts"], "BOT TEXTS"), ("5", "LANGUAGES"), ("10", "WAYS TO PAY")],
        ["Bot API", "Mini App", "Vue 3", "RTL", "Stars"], CYAN, BLUE, r, rc)))
    r, rc = browser(PURPLE, PINK)
    cards.append(("product-dashboard", card("p2", "02", "WEB DASHBOARD · OWNER AND STAFF", "Dashboard", "OWNER · ADMIN · SUPPORT",
        [f"{b('Plans, payments, users, resellers, reports')}, branding,", "panels, gateways, staff, backups, updates, licenses,",
         "monitoring and an append-only audit log."],
        [(NUMBERS["pages"], "PAGES"), (NUMBERS["routes"], "API ROUTES"), (NUMBERS["settings"], "SETTINGS")],
        ["Vue 3", "TypeScript", "Vite", "CSRF + CSP", "TOTP"], PURPLE, PINK, r, rc)))
    r, rc = cli_terminal()
    cards.append(("product-cli", card("p3", "03", "SERVER · ONE COMMAND", "bobres CLI", "SIGNED · AUTO ROLLBACK",
        [f"{b('Install, update, back up and repair')} from one", "command. Updates check signatures, back up first and",
         "roll back by themselves when the new version is not healthy."],
        [("20", "COMMANDS"), (NUMBERS["runbooks"], "RUNBOOKS"), ("9", "SERVICES RUN")],
        ["Go", "Docker Compose", "systemd", "Ed25519", "cosign"], AMBER, ORANGE, r, rc)))
    r, rc = pipeline([("🟢", "3x-ui", "v3.8.5 → v3.10.2 · groups · inbounds", "ایکس‌یوآی"), ("🟣", "Marzban", "0.8 · admin login · all proxies", "مرزبان"),
                      ("🔵", "S-UI", "1.6 · sing-box · API token · groups", "اس‌یوآی"), ("🟠", "Hiddify Manager", "14 · admin UUID · days-based expiry", "هیدیفای")], GREEN, TEAL)
    cards.append(("product-panels", card("p4", "04", "PANELS · PAYMENTS", "Panels + Pay", "TESTED ON REAL PANELS",
        [f"{b('Four panel kinds')}, several at once, with weights and a", f"client limit per server. {b('Ten ways to pay')}: cards, USDT",
         "on TRON, TON, Iranian gateways and Telegram Stars."],
        [("4", "PANEL KINDS"), ("7", "REAL-PANEL CI JOBS"), ("1×", "EACH PAYMENT SETTLES")],
        ["Zarinpal", "IDPay", "NextPay", "TRON", "TON"], GREEN, TEAL, r, rc)))
    return cards


def designs():
    cards = []
    r, rc = pipeline([("🌐", "caddy", "the only public entry · /admin /app /sub /pay", "ورودی"),
                      ("🧠", "core", "users · orders · ledger · no internet", "هسته"),
                      ("💳", "payments", "gateway secrets · TRON and TON watchers", "پرداخت"),
                      ("🛰️", "provisioner", "the only holder of the panels' tokens", "پنل‌ها"),
                      ("🤖", "bot", "Telegram · alerts · core's narrow way out", "ربات")], PINK, ORANGE, box_h=60, gap=12)
    cards.append(("design-architecture", card("d1", "05", "ARCHITECTURE · DOCKER COMPOSE", "9 Services", "CORE HAS NO INTERNET",
        [f"{b('A Go monorepo')} on one server: Caddy, PostgreSQL, Redis", "and the BOBRES services, each with its own database role,",
         "service token and network reach."],
        [("9", "SERVICES"), (NUMBERS["rpcs"], "gRPC CALLS"), (NUMBERS["migrations"], "MIGRATIONS")],
        ["Go", "gRPC", "PostgreSQL", "Redis", "Caddy"], PINK, ORANGE, r, rc)))
    r, rc = pipeline([("👆", "Customer taps Buy", "bot → core · CreateOrder, idempotent", "سفارش"),
                      ("💰", "Wallet or a gateway", "payments verifies with the provider", "پرداخت"),
                      ("✅", "Order paid, once", "outbox event · order.paid.v1", "تأیید"),
                      ("🛰️", "Client on the panel", "provisioner · create or renew", "ساخت"),
                      ("📲", "Link, QR and a page", "subscription.provisioned.v1 → chat", "تحویل")], CYAN, PURPLE, box_h=60, gap=12)
    cards.append(("design-order", card("d2", "06", "FLOW · FROM TAP TO CONFIG", "Order Flow", "EXACTLY ONCE",
        [f"{b('Money is whole numbers')} in minor units, never floats;", "the ledger is append-only and every money call carries",
         "an idempotency key. Events go through an outbox and an inbox."],
        [(NUMBERS["events"], "EVENT TYPES"), ("0", "FLOATS FOR MONEY"), ("1", "APPEND-ONLY LEDGER")],
        ["outbox", "inbox", "idempotency", "dead letters"], CYAN, PURPLE, r, rc)))
    r, rc = pipeline([("🧪", "Pull request", "race tests · lint · scans · real panels", "بررسی"),
                      ("🏷️", "Tag vX.Y.Z", "release workflow · amd64 + arm64", "انتشار"),
                      ("🔏", "Signed everything", "Ed25519 checksums · cosign images", "امضا"),
                      ("⬇️", "bobres update", "verifies digests · backs up first", "به‌روزرسانی"),
                      ("↩️", "Healthy or rolled back", "by itself, with the database", "بازگشت")], GREEN, CYAN, box_h=60, gap=12)
    cards.append(("design-releases", card("d3", "07", "RELEASES · CHAIN OF TRUST", "Releases", "SIGNED · VERIFIED",
        [f"{b('Two offline root keys')} sign the release keys; a release", "key signs the checksums; the CLI carries its images'",
         "digests and the stack runs by them."],
        [(NUMBERS["ci_jobs"], "CI JOBS"), (NUMBERS["releases"], "RELEASES"), ("2", "OFFLINE ROOT KEYS")],
        ["GitHub Actions", "Ed25519", "cosign", "GHCR"], GREEN, CYAN, r, rc)))
    r, rc = pipeline([("🌍", "public", "caddy only · ports 80 and 443", "عمومی"),
                      ("🔁", "edge", "internal · caddy ↔ services", "لبه"),
                      ("🗄️", "backend", "internal · services ↔ PostgreSQL, Redis", "داده"),
                      ("🔒", "offline", "core · no route out at all", "آفلاین"),
                      ("📤", "egress", "bot · payments · provisioner only", "خروجی")], BLUE, PURPLE, box_h=60, gap=12)
    cards.append(("design-security", card("d4", "08", "SECURITY · NETWORKS AND SECRETS", "Security", "NO DOCKER SOCKET",
        [f"{b('Secrets are sealed')} with a master key per service; only", "the provisioner holds panel tokens, only payments holds",
         "gateway secrets; a sealed tls-agent writes Caddy's files."],
        [("5", "NETWORKS"), (NUMBERS["alerts"], "ALERT KINDS"), ("0", "DOCKER SOCKETS")],
        ["CSRF", "CSP", "TOTP", "audit log", "rotate-key"], BLUE, PURPLE, r, rc)))
    r, rc = pipeline([("🧾", "Buyer pays", "from the wallet · Toman or USDT", "خرید"),
                      ("🏭", "licensesrv", "the vendor's server · makes the key", "کلید"),
                      ("🤖", "core asks the bot", "core has no internet · /v1/activate", "فعال‌سازی"),
                      ("🔑", "Signed token", "checked with the public key built in", "توکن"),
                      ("🔄", "Refreshed daily", "a lapse stops new things, deletes nothing", "تمدید")], AMBER, PINK, box_h=60, gap=12)
    cards.append(("design-licenses", card("d5", "09", "LICENSES · FREE CORE, PAID PREMIUM", "Licenses", "30-DAY TRIAL",
        [f"{b('No license is needed to run a store.')} The free tier", "keeps working; a lapsed license only stops new servers,",
         "services and resellers, never what already exists."],
        [("4", "TIERS"), ("30", "TRIAL DAYS"), ("1×", "REFRESH A DAY")],
        ["free", "trial", "Standard", "Pro", "licensesrv"], AMBER, PINK, r, rc)))
    return cards


def numbers():
    w, h = 1280, 190
    items = [(NUMBERS["go_lines"], "LINES OF GO", CYAN), (NUMBERS["go_tests"], "GO TESTS", PURPLE), (NUMBERS["web_tests"], "WEB TESTS", PINK),
             (NUMBERS["rpcs"], "gRPC CALLS", AMBER), (NUMBERS["routes"], "HTTP ROUTES", GREEN), (NUMBERS["migrations"], "MIGRATIONS", BLUE)]
    defs, frm = frame(w, h, CYAN, PINK, "n")
    cw = (w - 100) / len(items)
    body = [frm]
    for i, (n, l, c) in enumerate(items):
        cx = 50 + i * cw + cw / 2
        body.append(f'<text class="sans num" style="animation-delay:-{i * .5:.1f}s" x="{cx:.0f}" y="102" font-size="46" font-weight="900" fill="{c}" text-anchor="middle">{n}</text>'
                    f'<text class="mono" x="{cx:.0f}" y="134" font-size="12.5" letter-spacing="2.5" fill="#94a3b8" text-anchor="middle">{l}</text>')
        if i:
            body.append(f'<line x1="{50 + i * cw:.0f}" y1="64" x2="{50 + i * cw:.0f}" y2="144" stroke="#1f2937"/>')
    css = FRAME_CSS + " .num { animation: pulse 3s ease-in-out infinite; transform-box: fill-box; transform-origin: center; } @keyframes pulse { 50% { transform: scale(1.06); } }"
    return svg(w, h, "150K lines of Go, 3,103 Go tests, 1,464 web tests, 129 gRPC calls, 294 HTTP routes, 70 migrations", css, "".join(body), defs)


def glance():
    w, h = 1280, 170
    items = [("1", "command", "install · update · repair", CYAN, BLUE), ("4", "panels", "3x-ui · Marzban · S-UI · Hiddify", PURPLE, PINK),
             ("10", "ways to pay", "cards · USDT · TON · Stars", AMBER, ORANGE), ("5", "languages", "fa · en · ar · ru · tr", GREEN, TEAL),
             ("~400", "buys / second", "on one machine", BLUE, PURPLE)]
    gap = 16
    cw = (w - gap * 6) / 5
    defs, body = "", []
    for i, (n, l, s, c1, c2) in enumerate(items):
        x = gap + i * (cw + gap)
        defs += f'<linearGradient id="g{i}" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="{c1}"/><stop offset="1" stop-color="{c2}"/></linearGradient>'
        body.append(f'''<g class="hover" style="animation-delay:-{i * .8:.1f}s">
  <rect x="{x:.1f}" y="18" width="{cw:.1f}" height="{h - 36}" rx="22" fill="#0b1220"/>
  <rect class="edge" x="{x:.1f}" y="18" width="{cw:.1f}" height="{h - 36}" rx="22" fill="none" stroke="url(#g{i})" stroke-width="2.5" stroke-dasharray="60 30" pathLength="540"/>
  <text class="sans" x="{x + cw / 2:.1f}" y="92" font-size="44" font-weight="900" fill="url(#g{i})" text-anchor="middle">{n}</text>
  <text class="sans" x="{x + cw / 2:.1f}" y="118" font-size="17" font-weight="700" fill="#f1f5f9" text-anchor="middle">{l}</text>
  <text class="mono" x="{x + cw / 2:.1f}" y="140" font-size="12" fill="#94a3b8" text-anchor="middle">{s}</text>
</g>''')
    css = (".hover { animation: hover 4s ease-in-out infinite; } @keyframes hover { 50% { transform: translateY(-5px); } }"
           " .edge { animation: e 6s linear infinite; } @keyframes e { to { stroke-dashoffset: -540; } }")
    return svg(w, h, "1 command, 4 panels, 10 ways to pay, 5 languages, about 400 purchases a second", css, "".join(body), defs)


if __name__ == "__main__":
    for old in OUT.glob("*.svg"):
        old.unlink()
    write("hero.svg", hero())
    write("whoami.svg", whoami())
    write("glance.svg", glance())
    write("numbers.svg", numbers())
    for num, title, fa, c1, c2 in [("01", "THE PRODUCT", "محصول", CYAN, PURPLE), ("02", "SYSTEM DESIGN", "طراحی سیستم", PINK, ORANGE),
                                   ("03", "RUN IT", "نصب و اجرا", GREEN, CYAN), ("04", "EVERY DETAIL", "همهٔ جزئیات", PURPLE, PINK),
                                   ("05", "BY THE NUMBERS", "به عدد", AMBER, PINK)]:
        write(f"section-{num}.svg", header(num, title, fa, c1, c2))
    for name, s in products() + designs():
        write(f"{name}.svg", s)
