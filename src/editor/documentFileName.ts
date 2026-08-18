export const DOCUMENT_TITLE_MARKER = '【标题】'

export interface ParsedDocumentFileName {
  rawName: string
  body: string
  canonical: string
  titlePart: string
  hasExplicitTitle: boolean
}

export interface ManagedDocumentFileNameOptions {
  modeLabel: string
  saveTitle: string
  chapterTitle: string
  titleOverride?: string
}

function basename(pathOrName: string): string {
  return pathOrName.split(/[/\\]/).pop() || ''
}

export function canonicalStoryIdentity(saveTitle: string, chapterTitle = ''): string {
  const save = saveTitle.trim()
  const chapter = chapterTitle.trim()
  return chapter ? `${save} ${chapter}` : save
}

export function parseDocumentFileName(pathOrName: string): ParsedDocumentFileName {
  const rawName = basename(pathOrName)
  const body = rawName.replace(/\.txt$/i, '').replace(/^【[^】]*】/, '').trim()
  const markerIndex = body.indexOf(DOCUMENT_TITLE_MARKER)
  if (markerIndex >= 0) {
    return {
      rawName,
      body,
      canonical: body.slice(0, markerIndex).trim(),
      titlePart: body.slice(markerIndex + DOCUMENT_TITLE_MARKER.length).trim(),
      hasExplicitTitle: true,
    }
  }
  return {
    rawName,
    body,
    canonical: body,
    titlePart: '',
    hasExplicitTitle: false,
  }
}

export function formatManagedDocumentFileName(options: ManagedDocumentFileNameOptions): string {
  const saveTitle = options.saveTitle.trim()
  const chapterTitle = options.chapterTitle.trim()
  const titleOverride = (options.titleOverride || '').trim()
  const hasCustomTitle = titleOverride !== '' && titleOverride !== chapterTitle
  const effectiveChapter = hasCustomTitle ? titleOverride : chapterTitle
  const canonical = canonicalStoryIdentity(saveTitle || 'untitled', effectiveChapter)
  return `【${options.modeLabel}】${canonical}.txt`
}

export function legacyTitlePartForStory(
  identity: string,
  story: { saveTitle: string; chapterTitle: string },
): string {
  const canonical = identity.trim()
  const saveTitle = story.saveTitle.trim()
  const chapterTitle = story.chapterTitle.trim()
  if (!canonical || !saveTitle) return ''
  const canonicalIdentity = canonicalStoryIdentity(saveTitle, chapterTitle)
  if (canonical === canonicalIdentity || (chapterTitle === '特殊篇' && canonical === `${saveTitle} 其他`)) {
    return chapterTitle
  }
  const prefix = `${saveTitle} `
  return canonical.startsWith(prefix) ? canonical.slice(prefix.length).trim() : ''
}

export function titlePartForParsedFile(
  parsed: Pick<ParsedDocumentFileName, 'canonical' | 'titlePart' | 'hasExplicitTitle'>,
  story: { saveTitle: string; chapterTitle: string },
): string {
  if (parsed.hasExplicitTitle) return parsed.titlePart
  return legacyTitlePartForStory(parsed.canonical, story)
}
