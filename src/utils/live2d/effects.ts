// Live2D special-effect primitives (pure PIXI): full-screen color fades and
// directional black wipes. Ported from sekai-viewer's Fullcolor/Wipe layers.

import * as PIXI from 'pixi.js'

const STAGE_W = 1920
const STAGE_H = 1080
// Curtains/wipes are drawn oversized so they fully cover content that extends
// past the 1920×1080 stage box (cover-scaled backgrounds, character sprites with
// a vertical offset). Without this margin the overlay leaves a thin gap at the
// edges. The rect spans from -OVER to STAGE + OVER on each axis.
const OVER = 600

/** rAF tween with easeInOutQuad; resolves when done. `instant` jumps to end.
 *  `isAborted`, when provided and it returns true mid-flight, snaps the value to
 *  the final `to` and resolves immediately — so a skipped/clicked-through step
 *  never leaves a curtain or wipe stuck at a partial alpha/position. */
export function tween(
  setter: (v: number) => void,
  from: number,
  to: number,
  ms: number,
  instant = false,
  isAborted?: () => boolean,
): Promise<void> {
  if (instant || ms <= 0 || (isAborted && isAborted())) {
    setter(to)
    return Promise.resolve()
  }
  const t0 = performance.now()
  return new Promise((resolve) => {
    const tick = () => {
      if (isAborted && isAborted()) { setter(to); return resolve() }
      const p = Math.min(1, (performance.now() - t0) / ms)
      const e = p < 0.5 ? 2 * p * p : 1 - Math.pow(-2 * p + 2, 2) / 2
      setter(from + (to - from) * e)
      if (p < 1) requestAnimationFrame(tick)
      else resolve()
    }
    tick()
  })
}

/** Full-screen solid color overlay (black/white curtain). */
export class Fullcolor {
  readonly g = new PIXI.Graphics()
  constructor(parent: PIXI.Container) {
    this.g.alpha = 0
    parent.addChild(this.g)
  }
  /** Paint the full-screen rect a color (alpha is controlled by show/hide).
   *  Oversized by OVER on every side so no edge gap shows over content that
   *  extends past the stage box. */
  draw(color: number) {
    this.g.clear()
    this.g.beginFill(color, 1).drawRect(-OVER, -OVER, STAGE_W + OVER * 2, STAGE_H + OVER * 2).endFill()
  }
  /** Fade the curtain in (cover) to alpha 1. */
  show(ms: number, instant = false, isAborted?: () => boolean) {
    return tween((a) => { this.g.alpha = a }, this.g.alpha, 1, ms, instant, isAborted)
  }
  /** Fade the curtain out (reveal) to alpha 0. */
  hide(ms: number, instant = false, isAborted?: () => boolean) {
    return tween((a) => { this.g.alpha = a }, this.g.alpha, 0, ms, instant, isAborted)
  }
}

export type WipeDir = 'left' | 'right' | 'top' | 'bottom'

/** Full-screen black rect that slides in/out from a direction. Drawn oversized
 *  (OVER margin on every side) so the panel never leaves an edge gap while
 *  covering content that extends past the stage box. */
export class Wipe {
  readonly g = new PIXI.Graphics()
  constructor(parent: PIXI.Container) {
    this.g.beginFill(0x000000, 1)
      .drawRect(-OVER, -OVER, STAGE_W + OVER * 2, STAGE_H + OVER * 2).endFill()
    this.g.visible = false
    parent.addChild(this.g)
  }
  private offscreen(dir: WipeDir): [number, number] {
    // Move a full stage + margin so the oversized panel sits completely clear.
    switch (dir) {
      case 'left': return [-(STAGE_W + OVER * 2), 0]
      case 'right': return [STAGE_W + OVER * 2, 0]
      case 'top': return [0, -(STAGE_H + OVER * 2)]
      case 'bottom': return [0, STAGE_H + OVER * 2]
    }
  }
  /** Slide the black panel IN from `dir` to cover the screen. */
  async wipeIn(dir: WipeDir, ms: number, instant = false, isAborted?: () => boolean) {
    const [ox, oy] = this.offscreen(dir)
    this.g.visible = true
    this.g.position.set(ox, oy)
    const horiz = dir === 'left' || dir === 'right'
    await tween(
      (v) => { if (horiz) this.g.x = v; else this.g.y = v },
      horiz ? ox : oy,
      0,
      ms,
      instant,
      isAborted,
    )
  }
  /** Slide the black panel OUT toward `dir`, revealing the screen. */
  async wipeOut(dir: WipeDir, ms: number, instant = false, isAborted?: () => boolean) {
    const [ox, oy] = this.offscreen(dir)
    const horiz = dir === 'left' || dir === 'right'
    this.g.visible = true
    await tween(
      (v) => { if (horiz) this.g.x = v; else this.g.y = v },
      horiz ? this.g.x : this.g.y,
      horiz ? ox : oy,
      ms,
      instant,
      isAborted,
    )
    this.g.visible = false
  }
}
