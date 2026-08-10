import { describe, expect, it } from 'vitest'
import source from './JsonDownloadPage.vue?raw'

describe('JSON download directory binding', () => {
  it('uses the persisted settings field as the single live source of truth', () => {
    expect(source).toContain("get: () => settings.settings.jsonDownloadDir || ''")
    expect(source).toContain('settings.settings.jsonDownloadDir = value')
    expect(source).not.toContain('onActivated')
  })

  it('flushes the directory setting and snapshots it before downloads and exports', () => {
    expect(source).toContain('await persistOutputDirNow()')
    expect(source).toContain('downloadOne(coord, c.number, targetDir)')
    expect(source).toContain('exportTxtOne(coord, c.number, targetDir)')
    expect(source).toContain('outputDir: targetDir')
  })
})
