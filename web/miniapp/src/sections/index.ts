// Loads every section: each src/sections/<name>/index.ts registers itself.
import.meta.glob('./*/index.ts', { eager: true })
