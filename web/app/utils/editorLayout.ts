// The editor and the terminal split side by side as well as stacked (#309):
// the person chooses how the two share the column, the choice and the split
// of each layout kept per browser, the terminal keeping a minimum width
// side by side as it keeps a minimum height stacked. Pure.
import { clampSplit, readSplit, SPLIT_DEFAULT, writeSplit } from './editorTabs'

export type EditorLayout = 'stacked' | 'side'

export const LAYOUT_KEY = 'conductor.editor.layout'
export const SIDE_SPLIT_KEY = 'conductor.editor.split.side'
/** The editor's share of the width side by side, by default and at its ends. */
export const SIDE_SPLIT_DEFAULT = 0.5
export const SIDE_SPLIT_MIN = 0.25
export const SIDE_SPLIT_MAX = 0.75
/** The terminal keeps at least this width side by side: about forty columns at the default font. */
export const MIN_TERMINAL_WIDTH = 320
/** What one arrow press moves the split by. */
export const SPLIT_STEP = 0.02

type Store = Pick<Storage, 'getItem' | 'setItem'> | null

export function readLayout(storage: Pick<Storage, 'getItem'> | null): EditorLayout {
  try {
    return storage?.getItem(LAYOUT_KEY) === 'side' ? 'side' : 'stacked'
  } catch {
    return 'stacked'
  }
}

export function writeLayout(storage: Pick<Storage, 'setItem'> | null, layout: EditorLayout): void {
  try {
    storage?.setItem(LAYOUT_KEY, layout)
  } catch {
    // The browser may refuse storage; the layout then lasts the page.
  }
}

export function clampSideSplit(v: number): number {
  return Math.min(SIDE_SPLIT_MAX, Math.max(SIDE_SPLIT_MIN, v))
}

export function readSideSplit(storage: Pick<Storage, 'getItem'> | null): number {
  try {
    const v = Number(storage?.getItem(SIDE_SPLIT_KEY))
    return Number.isFinite(v) && v > 0 ? clampSideSplit(v) : SIDE_SPLIT_DEFAULT
  } catch {
    return SIDE_SPLIT_DEFAULT
  }
}

export function writeSideSplit(storage: Pick<Storage, 'setItem'> | null, v: number): void {
  try {
    storage?.setItem(SIDE_SPLIT_KEY, String(clampSideSplit(v)))
  } catch {
    // As above.
  }
}

/** The split kept for a layout: the editor's share of the height stacked, of the width side by side. */
export function readSplitFor(layout: EditorLayout, storage: Store): number {
  return layout === 'side' ? readSideSplit(storage) : readSplit(storage)
}

export function writeSplitFor(layout: EditorLayout, storage: Store, v: number): void {
  if (layout === 'side') writeSideSplit(storage, v)
  else writeSplit(storage, v)
}

export function clampSplitFor(layout: EditorLayout, v: number): number {
  return layout === 'side' ? clampSideSplit(v) : clampSplit(v)
}

export function defaultSplitFor(layout: EditorLayout): number {
  return layout === 'side' ? SIDE_SPLIT_DEFAULT : SPLIT_DEFAULT
}

/**
 * The split after a key on the handle: the arrows along the split's axis
 * move it a step (left or up gives the editor less, right or down more),
 * Home and End take it to its ends; any other key leaves it.
 */
export function stepSplit(layout: EditorLayout, v: number, key: string): number {
  const less = layout === 'side' ? 'ArrowLeft' : 'ArrowUp'
  const more = layout === 'side' ? 'ArrowRight' : 'ArrowDown'
  switch (key) {
    case less:
      return clampSplitFor(layout, v - SPLIT_STEP)
    case more:
      return clampSplitFor(layout, v + SPLIT_STEP)
    case 'Home':
      return clampSplitFor(layout, 0)
    case 'End':
      return clampSplitFor(layout, 1)
  }
  return v
}

/**
 * The editor's width in pixels for a column `total` pixels wide at `split`,
 * the terminal keeping at least MIN_TERMINAL_WIDTH plus the `reserved`
 * pixels between them (the handle).
 */
export function editorWidth(total: number, split: number, reserved: number): number {
  const most = Math.max(0, total - reserved - MIN_TERMINAL_WIDTH)
  return Math.max(0, Math.min(most, Math.round(total * clampSideSplit(split))))
}

/** The layout the column can show: side by side needs a wide column (`lg` and up); narrower, it is stacked whatever is kept. */
export function layoutFor(kept: EditorLayout, wide: boolean): EditorLayout {
  return kept === 'side' && wide ? 'side' : 'stacked'
}
