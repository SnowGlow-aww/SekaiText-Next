// PJSK 角色代表色 — drives the runtime accent picker (see ThemePicker.vue and
// stores/app.ts applyAccent). Picking a swatch sets one CSS variable (--accent),
// which the theme's --color-primary references, so the whole app recolours live.
// Values are the official 公式 representative colours provided by the user.

export interface AccentSwatch {
  id: string // stable key
  name: string // 中文名
  color: string // '#rrggbb'
}

export interface AccentGroup {
  unit: string // unit display name
  unitId: string
  unitColor: string // '#rrggbb' — the unit's own representative colour
  members: AccentSwatch[]
}

export const ACCENT_GROUPS: AccentGroup[] = [
  {
    unit: 'VIRTUAL SINGER',
    unitId: 'vs',
    unitColor: '#33ccbb',
    members: [
      { id: 'miku', name: '初音未来', color: '#33ccbb' },
      { id: 'rin', name: '镜音铃', color: '#ffcc11' },
      { id: 'len', name: '镜音连', color: '#ffee11' },
      { id: 'luka', name: '巡音流歌', color: '#ffbbcc' },
      { id: 'meiko', name: 'MEIKO', color: '#dd4444' },
      { id: 'kaito', name: 'KAITO', color: '#3366cc' },
    ],
  },
  {
    unit: 'Leo/need',
    unitId: 'ln',
    unitColor: '#4455dd',
    members: [
      { id: 'ichika', name: '星乃一歌', color: '#33aaee' },
      { id: 'saki', name: '天马咲希', color: '#ffdd44' },
      { id: 'honami', name: '望月穗波', color: '#ee6666' },
      { id: 'shiho', name: '日野森志步', color: '#bbdd22' },
    ],
  },
  {
    unit: 'MORE MORE JUMP!',
    unitId: 'mmj',
    unitColor: '#88dd44',
    members: [
      { id: 'minori', name: '花里实乃理', color: '#ffccaa' },
      { id: 'haruka', name: '桐谷遥', color: '#99ccff' },
      { id: 'airi', name: '桃井爱莉', color: '#ffaacc' },
      { id: 'shizuku', name: '日野森雫', color: '#99eedd' },
    ],
  },
  {
    unit: 'Vivid BAD SQUAD',
    unitId: 'vbs',
    unitColor: '#ee1166',
    members: [
      { id: 'kohane', name: '小豆泽心羽', color: '#ff6699' },
      { id: 'an', name: '白石杏', color: '#00bbdd' },
      { id: 'akito', name: '东云彰人', color: '#ff7722' },
      { id: 'toya', name: '青柳冬弥', color: '#0077dd' },
    ],
  },
  {
    unit: 'Wonderlands×Showtime',
    unitId: 'ws',
    unitColor: '#ff9900',
    members: [
      { id: 'tsukasa', name: '天马司', color: '#ffbb00' },
      { id: 'emu', name: '凤笑梦', color: '#ff66bb' },
      { id: 'nene', name: '草薙宁宁', color: '#33dd99' },
      { id: 'rui', name: '神代类', color: '#bb88ee' },
    ],
  },
  {
    unit: '25時、ナイトコードで。',
    unitId: 'n25',
    unitColor: '#885599',
    members: [
      { id: 'kanade', name: '宵崎奏', color: '#bb6688' },
      { id: 'mafuyu', name: '朝比奈真冬', color: '#8888cc' },
      { id: 'ena', name: '东云绘名', color: '#ccaa88' },
      { id: 'mizuki', name: '晓山瑞希', color: '#ddaacc' },
    ],
  },
]

// 'rainbow' = the default PJSK multicolour gradient accent (no single character).
export const DEFAULT_ACCENT = 'rainbow'

// Flat lookup: hex -> display name (used to label the current selection).
export const ACCENT_NAME_BY_COLOR: Record<string, string> = (() => {
  const m: Record<string, string> = {}
  for (const g of ACCENT_GROUPS) {
    m[g.unitColor.toLowerCase()] = g.unit
    for (const c of g.members) m[c.color.toLowerCase()] = c.name
  }
  return m
})()
