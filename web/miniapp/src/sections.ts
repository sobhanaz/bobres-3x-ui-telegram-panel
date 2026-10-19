// The Mini App's sections beyond the shop (a free trial, invites, support, the
// reseller panel...). A section is a folder, src/sections/<name>/, with an
// index.ts that calls registerSection and a messages.ts with its texts in both
// languages; nothing else has to change to add one (sections/index.ts loads
// every folder). The More tab lists the sections that are visible to the
// signed-in customer, and opens one in place of the tabs.
import type { Component } from 'vue'
import type { Me } from './api'

export interface Section {
  id: string
  /** the text key of its row in the More tab */
  title: string
  /** the text key of a one-line hint under the title (optional) */
  hint?: string
  /** whether this customer sees it: usually a flag of me.features, which core's section sets */
  visible: (me: Me) => boolean
  /** the section's screen; it may emit "close" to go back to the list */
  component: Component
  /** lower comes first; sections with the same order keep the order they registered in */
  order?: number
}

const registry: Section[] = []

export function registerSection(s: Section): void {
  if (registry.some((r) => r.id === s.id)) throw new Error(`the section "${s.id}" is registered twice`)
  registry.push(s)
}

/** sectionsFor are the sections this customer sees, in order. */
export function sectionsFor(me: Me | null): Section[] {
  if (!me) return []
  return registry
    .filter((s) => s.visible(me))
    .map((s, i) => ({ s, i }))
    .sort((a, b) => (a.s.order ?? 100) - (b.s.order ?? 100) || a.i - b.i)
    .map(({ s }) => s)
}

/** allSections is every registered section (tests). */
export function allSections(): readonly Section[] {
  return registry
}
