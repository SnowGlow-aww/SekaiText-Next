// Live2D scenario data types, ported from sekai-viewer's story-scenerio.d.ts.
// Describes the raw Unity scenario JSON (fetched via the backend proxy) that
// drives the Live2D player timeline.
// NOTE: this project enables `erasableSyntaxOnly`, so we use `const` objects +
// union types instead of `enum`.

export interface AppearCharacter {
  Character2dId: number
  CostumeType: string
}

export const SnippetAction = {
  None: 0,
  Talk: 1,
  CharacterLayout: 2,
  InputName: 3,
  CharacterMotion: 4,
  Selectable: 5,
  SpecialEffect: 6,
  Sound: 7,
  CharacterLayoutMode: 8,
} as const
export type SnippetAction = (typeof SnippetAction)[keyof typeof SnippetAction]

export const SnippetProgressBehavior = {
  Now: 0,
  WaitUntilFinished: 1,
} as const
export type SnippetProgressBehavior =
  (typeof SnippetProgressBehavior)[keyof typeof SnippetProgressBehavior]

export interface Snippet {
  Action: number
  ProgressBehavior: number
  ReferenceIndex: number
  Delay: number
}

export interface TalkCharacter {
  Character2dId: number
}

export interface Motion {
  Character2dId: number
  MotionName: string
  FacialName: string
  TimingSyncValue: number
}

export interface Voice {
  Character2dId: number
  VoiceId: string
  Volume: number
}

export interface TalkData {
  TalkCharacters: TalkCharacter[]
  WindowDisplayName: string
  Body: string
  TalkTention: number
  LipSync: number // 0=none, 1=lipsync with voice, 2=voice no lipsync (monologue)
  MotionChangeFrom: number
  Motions: Motion[]
  Voices: Voice[]
  Speed: number
  FontSize: number
  WhenFinishCloseWindow: number
  RequirePlayEffect: number
  EffectReferenceIdx: number
  RequirePlaySound: number
  SoundReferenceIdx: number
}

export const CharacterLayoutType = {
  CharacterMotion: 0, // motion/expression only, no position change
  Motion: 1, // motion + move from current to SideTo
  Appear: 2, // appear: motion + show + move SideFrom->SideTo
  Clear: 3, // disappear: move + stay 2s + hide
  ChangeDepth: 6,
} as const
export type CharacterLayoutType = (typeof CharacterLayoutType)[keyof typeof CharacterLayoutType]

export const CharacterLayoutPosition = {
  Unspecified: 0,
  LeftEdge: 2, // off-screen left  (x = -0.5)
  Left: 3, // x = 0.3 (0.25 in three_models)
  Center: 4, // x = 0.5
  RightEdge: 6, // off-screen right (x = 1.5)
  Right: 7, // x = 0.7 (0.75 in three_models)
  BottomLeftEdge: 9,
  BottomEdge: 10,
  BottomRightEdge: 12,
} as const
export type CharacterLayoutPosition =
  (typeof CharacterLayoutPosition)[keyof typeof CharacterLayoutPosition]

export const CharacterLayoutMoveSpeedType = {
  Slow: 0, // 700ms
  Normal: 1, // 500ms
  Fast: 2, // 300ms
} as const
export type CharacterLayoutMoveSpeedType =
  (typeof CharacterLayoutMoveSpeedType)[keyof typeof CharacterLayoutMoveSpeedType]

export interface LayoutData {
  Type: number
  SideFrom: number
  SideFromOffsetX: number
  SideTo: number
  SideToOffsetX: number
  DepthType: number
  Character2dId: number
  CostumeType: string
  MotionName: string
  FacialName: string
  MoveSpeedType: number
}

export interface FirstLayoutData {
  Character2dId: number
  CostumeType: string
  MotionName: string
  FacialName: string
  PositionString: string
  SideFrom: number
  SideTo: number
}

export const SpecialEffectType = {
  None: 0,
  BlackIn: 1,
  BlackOut: 2,
  WhiteIn: 3,
  WhiteOut: 4,
  ShakeScreen: 5,
  ShakeWindow: 6,
  ChangeBackground: 7,
  Telop: 8, // stop point (overlay text band)
  FlashbackIn: 9,
  FlashbackOut: 10,
  AmbientColorNormal: 12,
  AmbientColorEvening: 13,
  AmbientColorNight: 14,
  ChangeBackgroundStill: 17,
  Movie: 19,
  AttachCharacterShader: 22,
  FullScreenText: 24, // stop point
  StopShakeScreen: 25,
  StopShakeWindow: 26,
  MemoryIn: 27,
  MemoryOut: 28,
  BlackWipeInLeft: 29,
  BlackWipeOutLeft: 30,
  BlackWipeInRight: 31,
  BlackWipeOutRight: 32,
  BlackWipeInTop: 33,
  BlackWipeOutTop: 34,
  BlackWipeInBottom: 35,
  BlackWipeOutBottom: 36,
  FullScreenTextShow: 38,
  FullScreenTextHide: 39,
  Blur: 44,
} as const
export type SpecialEffectType = (typeof SpecialEffectType)[keyof typeof SpecialEffectType]

export interface SpecialEffectData {
  EffectType: number
  StringVal: string
  StringValSub: string
  Duration: number
  IntVal: number
}

export const SoundPlayMode = {
  CrossFade: 0,
  Stack: 1,
  LoopSe: 2,
  StopSe: 3,
  SetBgmVolume: 4,
} as const
export type SoundPlayMode = (typeof SoundPlayMode)[keyof typeof SoundPlayMode]

export interface SoundData {
  PlayMode: number
  Bgm: string
  Se: string
  Volume: number
  SeBundleName: string
  Duration: number
}

export const CharacterLayoutMode = {
  Normal: 0,
  ThreeModels: 3,
} as const
export type CharacterLayoutMode = (typeof CharacterLayoutMode)[keyof typeof CharacterLayoutMode]

export interface ScenarioSnippetCharacterLayoutMode {
  CharacterLayoutMode: number
}

export interface IScenarioData {
  ScenarioId: string
  AppearCharacters: AppearCharacter[]
  FirstLayout: FirstLayoutData[]
  FirstBgm: string
  FirstBackground: string
  FirstCharacterLayoutMode: number
  Snippets: Snippet[]
  TalkData: TalkData[]
  LayoutData: LayoutData[]
  SpecialEffectData: SpecialEffectData[]
  SoundData: SoundData[]
  NeedBundleNames: string[]
  IncludeSoundDataBundleNames: string[]
  ScenarioSnippetCharacterLayoutModes: ScenarioSnippetCharacterLayoutMode[]
}
