// The geometry of the 3D charts: an orthographic camera that turns around the
// vertical axis (yaw) and looks down at a pitch, boxes drawn back to front, and
// a donut seen at a tilt. Plain SVG polygons from plain arithmetic: no WebGL, no
// library, nothing to load but this file. Pure functions, unit tested.
//
// World axes: x runs along the categories, y is depth, z is up. The camera is at
// yaw degrees around z (0 = looking straight at the front, +y face) and pitch
// degrees above the floor (90 = straight down).

export interface Camera {
  yaw: number
  pitch: number
}

export interface Pt {
  x: number
  y: number
}

const rad = (deg: number) => (deg * Math.PI) / 180

/** project puts a world point on the screen (y grows downward) and gives its
 * depth along the ground (bigger is nearer the viewer). */
export function project(x: number, y: number, z: number, cam: Camera): Pt & { depth: number } {
  const t = rad(cam.yaw)
  const p = rad(cam.pitch)
  const depth = x * Math.sin(t) + y * Math.cos(t)
  return { x: x * Math.cos(t) - y * Math.sin(t), y: -z * Math.cos(p) + depth * Math.sin(p), depth }
}

export interface Box {
  x0: number
  x1: number
  y0: number
  y1: number
  z0: number
  z1: number
}

export type Face = 'top' | 'front' | 'back' | 'right' | 'left'

/** visibleFaces lists the faces of a box the camera sees: the top, and the
 * vertical faces that look toward it. */
export function visibleFaces(cam: Camera): Face[] {
  const t = rad(cam.yaw)
  const p = rad(cam.pitch)
  const out: Face[] = []
  if (Math.sin(p) > 1e-6) out.push('top')
  if (Math.cos(p) > 1e-6) {
    const sx = Math.sin(t)
    const cy = Math.cos(t)
    if (cy > 1e-6) out.push('front')
    if (cy < -1e-6) out.push('back')
    if (sx > 1e-6) out.push('right')
    if (sx < -1e-6) out.push('left')
  }
  return out
}

export type Shade = 'top' | 'light' | 'dark'

/** shadeOf is how dark a face is drawn: the top is the lightest, the faces
 * toward the viewer are the colour itself and the side ones are darker. */
export function shadeOf(face: Face): Shade {
  if (face === 'top') return 'top'
  return face === 'front' || face === 'back' ? 'light' : 'dark'
}

export interface Poly {
  face: Face
  shade: Shade
  points: Pt[]
}

/** boxFaces are the visible faces of a box as screen polygons. */
export function boxFaces(b: Box, cam: Camera): Poly[] {
  const P = (x: number, y: number, z: number): Pt => {
    const q = project(x, y, z, cam)
    return { x: q.x, y: q.y }
  }
  const corner: Record<Face, Pt[]> = {
    top: [P(b.x0, b.y0, b.z1), P(b.x1, b.y0, b.z1), P(b.x1, b.y1, b.z1), P(b.x0, b.y1, b.z1)],
    front: [P(b.x0, b.y1, b.z0), P(b.x1, b.y1, b.z0), P(b.x1, b.y1, b.z1), P(b.x0, b.y1, b.z1)],
    back: [P(b.x0, b.y0, b.z0), P(b.x1, b.y0, b.z0), P(b.x1, b.y0, b.z1), P(b.x0, b.y0, b.z1)],
    right: [P(b.x1, b.y0, b.z0), P(b.x1, b.y1, b.z0), P(b.x1, b.y1, b.z1), P(b.x1, b.y0, b.z1)],
    left: [P(b.x0, b.y0, b.z0), P(b.x0, b.y1, b.z0), P(b.x0, b.y1, b.z1), P(b.x0, b.y0, b.z1)],
  }
  return visibleFaces(cam).map((face) => ({ face, shade: shadeOf(face), points: corner[face] }))
}

/** depthOf is how near the viewer a box is: draw the smallest first. */
export function depthOf(b: Box, cam: Camera): number {
  return project((b.x0 + b.x1) / 2, (b.y0 + b.y1) / 2, 0, cam).depth
}

export interface Fit {
  scale: number
  tx: number
  ty: number
}

/** fit scales and moves points so they fill a width x height area inside a
 * padding (top, right, bottom, left), centred, keeping the proportions. */
export function fit(points: Pt[], width: number, height: number, pad: { top: number; right: number; bottom: number; left: number }): Fit {
  if (!points.length) return { scale: 1, tx: 0, ty: 0 }
  let minX = Infinity
  let maxX = -Infinity
  let minY = Infinity
  let maxY = -Infinity
  for (const p of points) {
    minX = Math.min(minX, p.x)
    maxX = Math.max(maxX, p.x)
    minY = Math.min(minY, p.y)
    maxY = Math.max(maxY, p.y)
  }
  const w = Math.max(1e-6, maxX - minX)
  const h = Math.max(1e-6, maxY - minY)
  const aw = Math.max(1, width - pad.left - pad.right)
  const ah = Math.max(1, height - pad.top - pad.bottom)
  const scale = Math.min(aw / w, ah / h)
  return { scale, tx: pad.left + (aw - w * scale) / 2 - minX * scale, ty: pad.top + (ah - h * scale) / 2 - minY * scale }
}

export const place = (p: Pt, f: Fit): Pt => ({ x: p.x * f.scale + f.tx, y: p.y * f.scale + f.ty })

/** clampCamera keeps the camera where the picture still reads: turned at most
 * 70 degrees either way, looking down between 18 and 80 degrees. */
export function clampCamera(c: Camera): Camera {
  return { yaw: Math.min(70, Math.max(-70, c.yaw)), pitch: Math.min(80, Math.max(18, c.pitch)) }
}

/** orderedByDepth lists the indexes of boxes far to near. */
export function orderedByDepth(boxes: Box[], cam: Camera): number[] {
  return boxes.map((b, i) => ({ i, d: depthOf(b, cam) })).sort((a, b) => a.d - b.d || a.i - b.i).map((e) => e.i)
}

// ------------------------------------------------------------- the tilted donut

export interface DonutView {
  /** the angle the first slice starts at, degrees clockwise from twelve o'clock */
  rotation: number
  /** degrees above the table: 90 is the flat donut, smaller leans it back */
  pitch: number
}

/** donutPoint is the screen point at angle a (radians clockwise from twelve) on
 * a circle of radius r, flattened by the pitch and turned by the rotation. */
export function donutPoint(cx: number, cy: number, r: number, a: number, view: DonutView, lift = 0): Pt {
  const t = a + rad(view.rotation)
  return { x: cx + r * Math.sin(t), y: cy - r * Math.cos(t) * Math.sin(rad(view.pitch)) + lift }
}

const n2 = (v: number) => Number(v.toFixed(2))

/** ringTop is the top face of a donut slice between the radii r0 and r1. */
export function ringTop(cx: number, cy: number, r0: number, r1: number, a0: number, a1: number, view: DonutView, lift = 0): string {
  const k = Math.sin(rad(view.pitch))
  const span = Math.min(a1 - a0, Math.PI * 2)
  if (!(span > 0)) return ''
  const large = span > Math.PI ? 1 : 0
  const o0 = donutPoint(cx, cy, r1, a0, view, lift)
  const o1 = donutPoint(cx, cy, r1, a0 + span, view, lift)
  const i1 = donutPoint(cx, cy, r0, a0 + span, view, lift)
  const i0 = donutPoint(cx, cy, r0, a0, view, lift)
  if (span >= Math.PI * 2 - 1e-6) {
    const om = donutPoint(cx, cy, r1, a0 + Math.PI, view, lift)
    const im = donutPoint(cx, cy, r0, a0 + Math.PI, view, lift)
    return (
      `M${n2(o0.x)},${n2(o0.y)}A${n2(r1)},${n2(r1 * k)} 0 1 1 ${n2(om.x)},${n2(om.y)}A${n2(r1)},${n2(r1 * k)} 0 1 1 ${n2(o0.x)},${n2(o0.y)}Z` +
      `M${n2(i0.x)},${n2(i0.y)}A${n2(r0)},${n2(r0 * k)} 0 1 0 ${n2(im.x)},${n2(im.y)}A${n2(r0)},${n2(r0 * k)} 0 1 0 ${n2(i0.x)},${n2(i0.y)}Z`
    )
  }
  return `M${n2(o0.x)},${n2(o0.y)}A${n2(r1)},${n2(r1 * k)} 0 ${large} 1 ${n2(o1.x)},${n2(o1.y)}L${n2(i1.x)},${n2(i1.y)}A${n2(r0)},${n2(r0 * k)} 0 ${large} 0 ${n2(i0.x)},${n2(i0.y)}Z`
}

/** facing says whether the point of a circle at angle t (radians, already
 * including the rotation) is on the near half, where the outer wall shows. */
const nearHalf = (t: number) => Math.cos(t) < 0

/** frontWall is the part of the outer wall of a slice that faces the viewer: the
 * strip between the slice's outer rim and the same rim `depth` px lower. It is
 * empty when the slice lies wholly on the far half. Sampled in short steps, so
 * the wall follows the ellipse exactly enough at any size. */
export function frontWall(cx: number, cy: number, r1: number, a0: number, a1: number, view: DonutView, depth: number): string {
  return wall(cx, cy, r1, a0, a1, view, depth, true)
}

/** backWall is the inside of the hole on its far half, seen through the hole. */
export function backWall(cx: number, cy: number, r0: number, a0: number, a1: number, view: DonutView, depth: number): string {
  return wall(cx, cy, r0, a0, a1, view, depth, false)
}

function wall(cx: number, cy: number, r: number, a0: number, a1: number, view: DonutView, depth: number, near: boolean): string {
  const rot = rad(view.rotation)
  const steps = Math.max(2, Math.ceil(((a1 - a0) * 180) / Math.PI / 4))
  // The rim is sampled in short steps; every run of samples on the visible half
  // becomes one strip (a slice wider than a half turn can show two).
  const runs: Pt[][] = []
  let run: Pt[] = []
  for (let i = 0; i <= steps; i++) {
    const a = a0 + ((a1 - a0) * i) / steps
    if (nearHalf(a + rot) === near) run.push(donutPoint(cx, cy, r, a, view))
    else if (run.length) {
      runs.push(run)
      run = []
    }
  }
  if (run.length) runs.push(run)
  return runs.map((rim) => strip(rim, depth)).join('')
}

function strip(rim: Pt[], depth: number): string {
  if (rim.length < 2) return ''
  const lower = [...rim].reverse().map((p) => ({ x: p.x, y: p.y + depth }))
  return `M${rim.map((p) => `${n2(p.x)},${n2(p.y)}`).join('L')}L${lower.map((p) => `${n2(p.x)},${n2(p.y)}`).join('L')}Z`
}
