import { describe, expect, it } from 'vitest'
import { formatDebugLogLines, redactDebugText } from './debugLogs'

describe('debug log export', () => {
  it('formats both frontend and backend entries into the exported log', () => {
    expect(formatDebugLogLines([
      { ts: '10:00:00', msg: 'front message', type: 'info' },
      { ts: '10:00:01', msg: 'server message', type: 'server' },
    ])).toEqual([
      '[10:00:00] [front] front message',
      '[10:00:01] [server] server message',
    ])
  })

  it('redacts credentials and local user directories', () => {
    const source = 'password="p@ss" token=abc Authorization: Bearer secret.jwt /Users/amia/project C:\\Users\\Mizuki\\project'
    const redacted = redactDebugText(source)

    expect(redacted).not.toContain('p@ss')
    expect(redacted).not.toContain('abc')
    expect(redacted).not.toContain('secret.jwt')
    expect(redacted).not.toContain('amia')
    expect(redacted).not.toContain('Mizuki')
    expect(redacted).toContain('[REDACTED]')
    expect(redacted).toContain('/Users/[USER]/project')
    expect(redacted).toContain('C:\\Users\\[USER]\\project')
  })
})
