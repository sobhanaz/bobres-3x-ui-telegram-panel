import { afterEach, describe, expect, it, vi } from 'vitest'

afterEach(() => vi.restoreAllMocks())

// './sections' is the registry file and './sections/index' loads the folders:
// the app's own entry must load them, or the More tab is empty in the real app
// while every unit test of a section still passes.
describe('the app entry', () => {
  it('registers every section folder when it starts', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('offline'))
    document.body.innerHTML = '<div id="app"></div>'
    await import('../main')
    const { allSections } = await import('../sections')
    const folders = Object.keys(import.meta.glob('../sections/*/index.ts'))
    expect(folders.length).toBeGreaterThan(0)
    expect(allSections()).toHaveLength(folders.length)
  })
})
