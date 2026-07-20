import { assertReleasePublicationMutable } from './release-manifest-lib.mjs'

const tag = required('RELEASE_TAG')
const manifestURL = process.env.APP_RELEASE_URL || 'https://sakimizuki.accr.cc/sekaitext-plugins/app-release.json'
const publicKeys = JSON.parse(required('SEKAITEXT_APP_UPDATE_PUBLIC_KEYS'))
const repository = required('GITHUB_REPOSITORY')
const githubToken = required('GH_TOKEN')
const response = await fetch(`${manifestURL}?immutability-check=${Date.now()}`, {
  cache: 'no-store',
  signal: AbortSignal.timeout(30_000),
})

if (response.status === 404) {
  console.log('[release] no published app manifest exists; release assets may be created')
} else {
  if (!response.ok) throw new Error(`could not read published app manifest (${response.status})`)
  const current = await response.json()
  assertReleasePublicationMutable(tag.replace(/^v/, ''), current, publicKeys)
  console.log(`[release] published app manifest ${current.version} is older than ${tag}; release assets may be created`)
}

const releaseResponse = await fetch(`https://api.github.com/repos/${repository}/releases/tags/${encodeURIComponent(tag)}`, {
  headers: {
    accept: 'application/vnd.github+json',
    authorization: `Bearer ${githubToken}`,
    'x-github-api-version': '2022-11-28',
  },
  signal: AbortSignal.timeout(30_000),
})
if (releaseResponse.status !== 404) {
  if (!releaseResponse.ok) throw new Error(`could not inspect the GitHub release (${releaseResponse.status})`)
  const release = await releaseResponse.json()
  if (release.assets?.some((asset) => asset.name === 'app-release.json')) {
    throw new Error(`refusing same-tag release ${tag} before mutating GitHub assets; app-release.json is already published`)
  }
}

function required(name) {
  const value = process.env[name]
  if (!value) throw new Error(`${name} is required`)
  return value
}
