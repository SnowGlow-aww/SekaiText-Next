import {
  fetchPluginMarketIndex,
  parsePluginPublicKeys,
  verifyPluginMarketIndex,
  verifyPluginPackages,
} from './plugin-market-lib.mjs'

const marketURL = process.env.PLUGIN_MARKET_URL || 'https://sakimizuki.accr.cc/sekaitext-plugins/index.json'
const publicKeys = parsePluginPublicKeys(required('SEKAITEXT_PLUGIN_PUBLIC_KEYS'))
const { index } = await fetchPluginMarketIndex(marketURL)
// Runtime keeps the temporary v2 bridge, but a new official app release must
// not ship until the default CDN has advanced to the complete v3 snapshot.
const verified = verifyPluginMarketIndex(index, publicKeys, { requireV3: true })
await verifyPluginPackages(index)

console.log(`[plugin-market] verified ${index.plugins.length} packages at sequence ${verified.sequence} with ${verified.keyId}`)

function required(name) {
  const value = process.env[name]
  if (!value) throw new Error(`${name} is required`)
  return value
}
