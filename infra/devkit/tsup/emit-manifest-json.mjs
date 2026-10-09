#!/usr/bin/env node

import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const SUPPORTED_SCHEMAS = new Set(['kb.service/1', 'kb.adapter/1', 'kb.plugin/3'])

export async function emitManifestJson({ manifestModule, outputPath }) {
  if (!manifestModule || !outputPath) {
    throw new TypeError('manifestModule and outputPath are required')
  }
  const moduleUrl = pathToFileURL(resolve(manifestModule)).href
  const imported = await import(moduleUrl)
  const manifest = imported.manifest
  if (!manifest || typeof manifest !== 'object' || Array.isArray(manifest)) {
    throw new TypeError(`manifest module ${manifestModule} must export an object named manifest`)
  }
  if (!SUPPORTED_SCHEMAS.has(manifest.schema)) {
    throw new TypeError(`manifest module ${manifestModule} must declare schema kb.service/1, kb.adapter/1, or kb.plugin/3`)
  }
  const output = resolve(outputPath)
  mkdirSync(dirname(output), { recursive: true })
  writeFileSync(output, `${JSON.stringify(manifest, null, 2)}\n`)
  return manifest
}

async function main(args) {
  const moduleIndex = args.indexOf('--module')
  const outputIndex = args.indexOf('--output')
  if (moduleIndex < 0 || outputIndex < 0 || !args[moduleIndex + 1] || !args[outputIndex + 1]) {
    throw new TypeError('usage: emit-manifest-json.mjs --module <manifest-module> --output <manifest.json>')
  }
  await emitManifestJson({ manifestModule: args[moduleIndex + 1], outputPath: args[outputIndex + 1] })
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch(error => {
    process.stderr.write(`[kb-devkit] ${error.message}\n`)
    process.exitCode = 1
  })
}
