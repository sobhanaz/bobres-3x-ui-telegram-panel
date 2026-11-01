#!/usr/bin/env python3
"""Draws the README's animated pictures into docs/readme/.

GitHub shows an SVG in a README as an image: its CSS and SMIL animations run, but it may not load anything
from outside, so the mascot is embedded as a data URI and the text uses the viewer's system fonts. Every
animation stops for viewers who ask for reduced motion.

    python3 tools/readme-art/gen.py

Change the numbers here (STATS) when the product changes, run it, and commit the SVGs.
"""

import base64
import pathlib

ROOT = pathlib.Path(__file__).resolve().parents[2]
OUT = ROOT / "docs" / "readme"
FONT = "-apple-system, 'Segoe UI', Helvetica, Arial, sans-serif"

REDUCED = "@media (prefers-reduced-motion: reduce) { * { animation: none !important; } }"

STATS = [
    ("1", "command", "install · update · repair", "#14b8a6", "#0ea5e9"),
    ("4", "panels", "3x-ui · Marzban · S-UI · Hiddify", "#8b5cf6", "#ec4899"),
    ("10", "ways to pay", "cards · USDT · TON · Stars", "#f59e0b", "#ef4444"),
    ("5", "languages", "fa · en · ar · ru · tr", "#22c55e", "#14b8a6"),
    ("~400", "buys / second", "on one machine", "#3b82f6", "#8b5cf6"),
]

CARDS = [
    ("customers", "Customers", ["Bot and Mini App store", "Wallet, trial, renewals", "Codes, invites, support"], "#14b8a6", "#0ea5e9", "cart"),
    ("resellers", "Resellers", ["Tier prices on credit", "Their own prefix and group", "Messages and reminders"], "#8b5cf6", "#ec4899", "people"),
    ("you", "You", ["A dashboard for everything", "Alerts in Telegram", "Prometheus and status page"], "#f59e0b", "#ef4444", "compass"),
    ("payments", "Payments", ["Cards, USDT on TRON, TON", "Iranian gateways, Stars", "Each settles exactly once"], "#22c55e", "#14b8a6", "coin"),
    ("panels", "Panels", ["3x-ui, Marzban, S-UI, Hiddify", "Several at once, weighted", "Tested on real panels in CI"], "#3b82f6", "#8b5cf6", "server"),
    ("server", "Server", ["One bobres command", "Signed updates, rollback", "Encrypted backups, HTTPS"], "#ec4899", "#f59e0b", "shield"),
]


def write(name, svg):
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / name).write_text(svg.strip() + "\n", encoding="utf-8")
    print(f"docs/readme/{name}: {len(svg) // 1024} KB")


def hero():
    mascot = base64.b64encode((ROOT / "docs" / "brand" / "bobres-mascot.webp").read_bytes()).decode()
    # The floor's grid lines, in perspective: verticals fan out from a vanishing point.
    vp_x, horizon, w, h = 900, 300, 1280, 440
    fan = "".join(f'<line x1="{vp_x}" y1="{horizon}" x2="{vp_x + (i - 12) * 140}" y2="{h}"/>' for i in range(25))
    rows = "".join(f'<line class="row" style="animation-delay:-{i * 0.6:.1f}s" x1="0" y1="{horizon}" x2="{w}" y2="{horizon}"/>' for i in range(5))
    chips = [("Telegram", "#38bdf8", 0), ("3x-ui", "#a78bfa", -2.67), ("USDT", "#34d399", -5.33)]
    orbit = "M 900 250 m -230 0 a 230 62 0 1 0 460 0 a 230 62 0 1 0 -460 0"
    chip_svg = "".join(f'''
  <g>
    <animateMotion dur="8s" repeatCount="indefinite" begin="{b}s" path="{orbit}"/>
    <animateTransform attributeName="transform" type="scale" additive="sum" values="0.8;1.15;0.8" keyTimes="0;0.5;1" dur="8s" begin="{b}s" repeatCount="indefinite"/>
    <rect x="-44" y="-15" width="88" height="30" rx="15" fill="#0b1220" stroke="{c}" stroke-width="2"/>
    <circle cx="-28" cy="0" r="5" fill="{c}"/>
    <text x="6" y="5" font-size="13" font-weight="700" fill="#e2e8f0" text-anchor="middle">{t}</text>
  </g>''' for t, c, b in chips)
    return f'''
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img"
     aria-label="BOBRES: launch your own VPN store on Telegram in one command">
<style>
  text {{ font-family: {FONT}; }}
  .bg {{ animation: hue 18s ease-in-out infinite alternate; }}
  @keyframes hue {{ to {{ filter: hue-rotate(50deg); }} }}
  .row {{ animation: row 3s linear infinite; }}
  @keyframes row {{ from {{ transform: translateY(0); opacity: .9; }} to {{ transform: translateY({h - horizon}px); opacity: 0; }} }}
  .float {{ animation: float 5s ease-in-out infinite; transform-origin: 900px 200px; }}
  @keyframes float {{ 50% {{ transform: translateY(-14px); }} }}
  .shadow {{ animation: shadow 5s ease-in-out infinite; transform-origin: 900px 392px; }}
  @keyframes shadow {{ 50% {{ transform: scaleX(.8); opacity: .35; }} }}
  .ring {{ animation: spin 14s linear infinite; transform-origin: 900px 200px; }}
  @keyframes spin {{ to {{ transform: rotate(360deg); }} }}
  .shine {{ animation: shine 4s ease-in-out infinite; }}
  @keyframes shine {{ 0% {{ transform: translateX(-500px); }} 60%,100% {{ transform: translateX(700px); }} }}
  .caret {{ animation: blink 1s step-end infinite; }}
  @keyframes blink {{ 50% {{ opacity: 0; }} }}
  .twinkle {{ animation: tw 3s ease-in-out infinite; }}
  @keyframes tw {{ 50% {{ opacity: .15; }} }}
  {REDUCED}
</style>
<defs>
  <linearGradient id="sky" x1="0" y1="0" x2="1" y2="1">
    <stop offset="0" stop-color="#0b1023"/><stop offset=".55" stop-color="#132a4a"/><stop offset="1" stop-color="#3b0f4f"/>
  </linearGradient>
  <linearGradient id="title" x1="0" x2="1">
    <stop offset="0" stop-color="#5eead4"/><stop offset=".5" stop-color="#a78bfa"/><stop offset="1" stop-color="#f472b6"/>
  </linearGradient>
  <linearGradient id="shineg" x1="0" x2="1"><stop offset="0" stop-color="#fff" stop-opacity="0"/><stop offset=".5" stop-color="#fff" stop-opacity=".55"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></linearGradient>
  <linearGradient id="floor" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#22d3ee" stop-opacity=".0"/><stop offset="1" stop-color="#22d3ee" stop-opacity=".55"/></linearGradient>
  <linearGradient id="ringg" x1="0" x2="1"><stop offset="0" stop-color="#5eead4"/><stop offset=".5" stop-color="#a78bfa" stop-opacity=".2"/><stop offset="1" stop-color="#f472b6"/></linearGradient>
  <radialGradient id="glow"><stop offset="0" stop-color="#2dd4bf" stop-opacity=".55"/><stop offset="1" stop-color="#2dd4bf" stop-opacity="0"/></radialGradient>
  <clipPath id="portal"><circle cx="900" cy="200" r="128"/></clipPath>
  <clipPath id="titleclip"><text x="70" y="168" font-size="96" font-weight="900" letter-spacing="2">BOBRES</text></clipPath>
</defs>

<g class="bg">
  <rect width="{w}" height="{h}" fill="url(#sky)"/>
  <g fill="#e0f2fe">
    <circle class="twinkle" cx="120" cy="60" r="1.6"/><circle class="twinkle" style="animation-delay:-1s" cx="380" cy="40" r="1.2"/>
    <circle class="twinkle" style="animation-delay:-2s" cx="620" cy="90" r="1.8"/><circle class="twinkle" style="animation-delay:-.5s" cx="1180" cy="70" r="1.4"/>
    <circle class="twinkle" style="animation-delay:-1.5s" cx="1080" cy="30" r="1.1"/><circle class="twinkle" style="animation-delay:-2.4s" cx="250" cy="110" r="1.3"/>
  </g>
  <rect y="{horizon}" width="{w}" height="{h - horizon}" fill="url(#floor)" opacity=".35"/>
  <g stroke="#22d3ee" stroke-opacity=".35" stroke-width="1.2">{fan}</g>
  <g stroke="#22d3ee" stroke-opacity=".6" stroke-width="1.4">{rows}</g>
</g>

<ellipse class="shadow" cx="900" cy="392" rx="120" ry="16" fill="#000" opacity=".5"/>
<circle cx="900" cy="200" r="190" fill="url(#glow)"/>
<path d="{orbit}" fill="none" stroke="#a78bfa" stroke-opacity=".45" stroke-width="1.5" stroke-dasharray="6 8"/>
<g class="float">
  <circle class="ring" cx="900" cy="200" r="142" fill="none" stroke="url(#ringg)" stroke-width="6" stroke-dasharray="120 40"/>
  <image clip-path="url(#portal)" x="760" y="60" width="280" height="280" preserveAspectRatio="xMidYMid slice"
         xlink:href="data:image/webp;base64,{mascot}" href="data:image/webp;base64,{mascot}"/>
  <circle cx="900" cy="200" r="128" fill="none" stroke="#5eead4" stroke-width="3"/>
</g>
{chip_svg}

<text x="70" y="168" font-size="96" font-weight="900" letter-spacing="2" fill="url(#title)">BOBRES</text>
<g clip-path="url(#titleclip)"><g transform="skewX(-20)"><rect class="shine" x="0" y="80" width="160" height="110" fill="url(#shineg)"/></g></g>
<text x="72" y="226" font-size="27" font-weight="600" fill="#e2e8f0">Your VPN store on Telegram, in one command.<tspan class="caret" fill="#5eead4"> ▍</tspan></text>
<text x="74" y="276" font-size="17" fill="#94a3b8">Sell, renew and support VPN subscriptions · bot · Mini App · dashboard</text>
<g transform="translate(74 310)">
  <rect width="150" height="40" rx="20" fill="#14b8a6"/><text x="75" y="26" font-size="16" font-weight="800" fill="#062925" text-anchor="middle">Self-hosted</text>
  <rect x="164" width="150" height="40" rx="20" fill="none" stroke="#a78bfa" stroke-width="2"/><text x="239" y="26" font-size="16" font-weight="700" fill="#ddd6fe" text-anchor="middle">White-label</text>
  <rect x="328" width="150" height="40" rx="20" fill="none" stroke="#f472b6" stroke-width="2"/><text x="403" y="26" font-size="16" font-weight="700" fill="#fbcfe8" text-anchor="middle">Signed updates</text>
</g>
</svg>'''


def stats():
    w, h, gap = 1280, 170, 16
    cw = (w - gap * 6) / 5
    cards = []
    for i, (num, label, sub, c1, c2) in enumerate(STATS):
        x = gap + i * (cw + gap)
        cards.append(f'''
<g class="card" style="animation-delay:-{i * 0.8:.1f}s">
  <defs>
    <linearGradient id="s{i}" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="{c1}"/><stop offset="1" stop-color="{c2}"/></linearGradient>
  </defs>
  <rect x="{x:.1f}" y="18" width="{cw:.1f}" height="{h - 36}" rx="22" fill="#0b1220"/>
  <rect class="edge" x="{x:.1f}" y="18" width="{cw:.1f}" height="{h - 36}" rx="22" fill="none" stroke="url(#s{i})" stroke-width="3" stroke-dasharray="60 30" pathLength="540"/>
  <rect x="{x + 18:.1f}" y="34" width="{cw - 36:.1f}" height="6" rx="3" fill="url(#s{i})" opacity=".9"/>
  <text class="num" x="{x + cw / 2:.1f}" y="98" font-size="44" font-weight="900" fill="url(#s{i})" text-anchor="middle">{num}</text>
  <text x="{x + cw / 2:.1f}" y="124" font-size="17" font-weight="700" fill="#f1f5f9" text-anchor="middle">{label}</text>
  <text x="{x + cw / 2:.1f}" y="145" font-size="12.5" fill="#94a3b8" text-anchor="middle">{sub}</text>
</g>''')
    return f'''
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img"
     aria-label="1 command, 4 panels, 10 ways to pay, 5 languages, about 400 purchases a second">
<style>
  text {{ font-family: {FONT}; }}
  .card {{ animation: hover 4s ease-in-out infinite; }}
  @keyframes hover {{ 50% {{ transform: translateY(-5px); }} }}
  .edge {{ animation: run 6s linear infinite; }}
  @keyframes run {{ to {{ stroke-dashoffset: -540; }} }}
  .num {{ animation: pulse 3s ease-in-out infinite; transform-box: fill-box; transform-origin: center; }}
  @keyframes pulse {{ 50% {{ transform: scale(1.08); }} }}
  {REDUCED}
</style>
{"".join(cards)}
</svg>'''


ICONS = {
    # Isometric shapes: a top face, a left face and a right face, shaded from the card's two colours.
    "cart": '<path d="M-34 -6 L0 -24 L34 -6 L0 12 Z" fill="url(#t)"/><path d="M-34 -6 L0 12 L0 40 L-34 22 Z" fill="url(#l)"/><path d="M34 -6 L0 12 L0 40 L34 22 Z" fill="url(#r)"/>'
            '<circle cx="-14" cy="48" r="6" fill="#f8fafc"/><circle cx="16" cy="48" r="6" fill="#f8fafc"/>',
    "people": '<circle cx="-14" cy="-14" r="14" fill="url(#t)"/><circle cx="18" cy="-8" r="11" fill="url(#r)"/>'
              '<path d="M-38 34 Q-14 0 10 34 Z" fill="url(#l)"/><path d="M0 34 Q18 8 38 34 Z" fill="url(#r)"/>',
    "compass": '<path d="M0 -40 L36 -20 L36 20 L0 40 L-36 20 L-36 -20 Z" fill="url(#l)"/><path d="M0 -40 L36 -20 L0 0 L-36 -20 Z" fill="url(#t)"/>'
               '<path d="M0 0 L36 -20 L36 20 L0 40 Z" fill="url(#r)"/><path d="M-6 -6 L18 -18 L6 6 L-18 18 Z" fill="#f8fafc" opacity=".9"/>',
    "coin": '<ellipse cx="0" cy="18" rx="36" ry="14" fill="url(#l)"/><rect x="-36" y="-6" width="72" height="24" fill="url(#l)"/>'
            '<ellipse cx="0" cy="-6" rx="36" ry="14" fill="url(#t)"/><text x="0" y="-1" font-size="16" font-weight="900" fill="#0b1220" text-anchor="middle">₮</text>',
    "server": ''.join(f'<g transform="translate(0 {y})"><path d="M-36 -8 L0 -26 L36 -8 L0 10 Z" fill="url(#t)"/>'
                      f'<path d="M-36 -8 L0 10 L0 22 L-36 4 Z" fill="url(#l)"/><path d="M36 -8 L0 10 L0 22 L36 4 Z" fill="url(#r)"/></g>' for y in (24, 0, -24)),
    "shield": '<path d="M0 -42 L34 -28 L34 4 Q34 30 0 44 Q-34 30 -34 4 L-34 -28 Z" fill="url(#l)"/>'
              '<path d="M0 -42 L34 -28 L34 4 Q34 30 0 44 Z" fill="url(#r)"/><circle cx="0" cy="-2" r="8" fill="#0b1220"/><rect x="-3" y="2" width="6" height="16" fill="#0b1220"/>',
}


def card(key, title, lines, c1, c2, icon):
    w, h = 420, 230
    body = "".join(f'<text x="28" y="{124 + i * 28}" font-size="16" fill="#e2e8f0"><tspan fill="{c1}" font-weight="900">›</tspan> {t}</text>'
                   for i, t in enumerate(lines))
    return f'''
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="{title}: {'; '.join(lines)}">
<style>
  text {{ font-family: {FONT}; }}
  .bob {{ animation: bob 4s ease-in-out infinite; }}
  @keyframes bob {{ 0%, 100% {{ transform: translate(340px, 66px) rotate(-6deg); }} 50% {{ transform: translate(340px, 58px) rotate(-6deg); }} }}
  .blob {{ animation: drift 9s ease-in-out infinite alternate; }}
  @keyframes drift {{ to {{ transform: translate(-40px, 30px) scale(1.2); }} }}
  .edge {{ animation: run 7s linear infinite; }}
  @keyframes run {{ to {{ stroke-dashoffset: -700; }} }}
  {REDUCED}
</style>
<defs>
  <linearGradient id="bg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#0b1220"/><stop offset="1" stop-color="#111a33"/></linearGradient>
  <linearGradient id="g" x1="0" x2="1"><stop offset="0" stop-color="{c1}"/><stop offset="1" stop-color="{c2}"/></linearGradient>
  <linearGradient id="t" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#ffffff" stop-opacity=".95"/><stop offset="1" stop-color="{c1}"/></linearGradient>
  <linearGradient id="l" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="{c1}"/><stop offset="1" stop-color="{c2}"/></linearGradient>
  <linearGradient id="r" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="{c2}"/><stop offset="1" stop-color="#1e1b4b"/></linearGradient>
  <radialGradient id="blobg"><stop offset="0" stop-color="{c2}" stop-opacity=".45"/><stop offset="1" stop-color="{c2}" stop-opacity="0"/></radialGradient>
  <clipPath id="cl"><rect width="{w}" height="{h}" rx="24"/></clipPath>
</defs>
<g clip-path="url(#cl)">
  <rect width="{w}" height="{h}" fill="url(#bg)"/>
  <circle class="blob" cx="330" cy="40" r="150" fill="url(#blobg)"/>
  <rect y="0" width="{w}" height="6" fill="url(#g)"/>
</g>
<rect class="edge" x="1.5" y="1.5" width="{w - 3}" height="{h - 3}" rx="23" fill="none" stroke="url(#g)" stroke-width="3" stroke-dasharray="90 50" pathLength="700"/>
<text x="28" y="78" font-size="32" font-weight="900" fill="url(#g)">{title}</text>
{body}
<g class="bob" transform="translate(340 66) rotate(-6)">{ICONS[icon]}</g>
</svg>'''


def divider():
    w, h = 1280, 24
    return f'''
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="presentation">
<style>
  .flow {{ animation: flow 5s linear infinite; }}
  @keyframes flow {{ to {{ transform: translateX(-640px); }} }}
  {REDUCED}
</style>
<defs><linearGradient id="d" x1="0" x2="640" spreadMethod="repeat" gradientUnits="userSpaceOnUse">
  <stop offset="0" stop-color="#14b8a6"/><stop offset=".33" stop-color="#8b5cf6"/><stop offset=".66" stop-color="#ec4899"/><stop offset="1" stop-color="#14b8a6"/>
</linearGradient></defs>
<g class="flow"><rect x="0" y="10" width="{w * 2}" height="4" rx="2" fill="url(#d)"/></g>
</svg>'''


if __name__ == "__main__":
    write("hero.svg", hero())
    write("stats.svg", stats())
    for c in CARDS:
        write(f"card-{c[0]}.svg", card(*c))
    write("divider.svg", divider())
