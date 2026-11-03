import { readdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { gzipSync } from 'node:zlib'

const root = path.resolve(import.meta.dirname, '../internal/webui/dist/assets')
const compressible = new Set(['.js', '.css', '.svg', '.json', '.woff', '.woff2'])

async function visit(dir) {
  let entries
  try {
    entries = await readdir(dir, { withFileTypes: true })
  } catch (error) {
    if (error.code === 'ENOENT') return
    throw error
  }
  for (const entry of entries) {
    const file = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      await visit(file)
    } else if (compressible.has(path.extname(entry.name))) {
      const input = await readFile(file)
      if (input.length >= 256) await writeFile(`${file}.gz`, gzipSync(input, { level: 9, mtime: 0 }))
    }
  }
}

await visit(root)
