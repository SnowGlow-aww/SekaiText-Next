export interface GlossaryEntry {
  id: string
  source: string
  translation: string
  aliases?: string[]
  note?: string
  category: string
  subCategory?: string
  origin: 'import' | 'user' | 'remote'
  // Team-mode collaboration fields (populated from a remote sync).
  contributorName?: string
  updatedBy?: string
  version?: number
}

// --- Team mode (collaborative glossary server) ---

export interface TeamUser {
  id: string
  username: string
  displayName: string
  role: 'member' | 'reviewer' | 'admin' | 'superadmin'
  status: string
  // User-chosen avatar background colour ('#rrggbb'); empty/undefined → a
  // deterministic colour derived from the user id (see AccountPage).
  avatarColor?: string
}

export interface TeamStatus {
  loggedIn: boolean
  connected: boolean
  readonly: boolean
  serverUrl: string
  user: TeamUser | null
}

export interface Proposal {
  id: string
  kind: 'add' | 'edit' | 'delete'
  targetType: string
  targetId?: string
  category: string
  payload: string
  baseVersion?: number
  authorId: string
  authorName?: string
  status: 'pending' | 'approved' | 'rejected'
  reviewerId?: string
  reviewNote?: string
  createdAt: number
  reviewedAt?: number
}

export interface CategoryCount {
  category: string
  count: number
}

export interface Appellation {
  speaker: string
  target: string
  jp?: string
  cn?: string
}

export interface AppellationResult {
  found: boolean
  speaker?: string
  target?: string
  jp?: string
  cn?: string
}

// Per-sheet outcome of an Excel import.
export interface SheetReport {
  sheet: string
  kind: 'terms' | 'appellations' | 'grammar' | 'skipped'
  count: number
  skipped: string
}

export interface ImportReport {
  sheets: SheetReport[]
  totalEntries: number
  totalAppellations: number
  totalGrammar: number
}

// One grammar usage row (语法用例 sheet), surfaced on the dedicated Grammar page.
export interface GrammarUsage {
  id: string
  item: string
  location?: string
  index?: string
  connection?: string
  note?: string
  unit?: string
  example?: string
  reference?: string
}

// Full payload returned by /glossary/export.
export interface GlossaryData {
  entries: GlossaryEntry[]
  appellations: Appellation[]
  grammar?: GrammarUsage[]
}
