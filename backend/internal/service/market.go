package service

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultMarketURL is the built-in plugin marketplace index. Overridable via
// Settings.PluginMarketURL. Served from the project's own OSS-backed edge CDN
// (sakimizuki.accr.cc) for fast downloads; the plugin .sekplugin files it lists
// live under the same CDN prefix.
const DefaultMarketURL = "https://sakimizuki.accr.cc/sekaitext-plugins/index.json"

const (
	OfficialPluginPublisher  = "sekaitext-official"
	pluginSignatureAlgorithm = "ed25519"
	pluginSignatureHeader    = "SekaiText-Plugin-Signature-V1\n"
	pluginMetadataHeader     = "SekaiText-Plugin-Metadata-Signature-V2\n"
	pluginSnapshotHeader     = "SekaiText-Plugin-Market-Snapshot-V1\n"
	// Current production is a signed v2 index while v3 is being activated.
	// New clients accept that bridge only until this date; afterwards they fail
	// closed unless they have received v3, preventing an indefinite v2 freeze.
	LegacyMarketV2Cutoff = "2026-10-01T00:00:00Z"
)

// OfficialPluginPublicKeysJSON is set at build time by scripts/build-go.mjs.
// Its JSON object maps keyId to a standard-base64 raw Ed25519 public key. It is
// intentionally empty in source: until an official key is provisioned, new
// marketplace installs and automatic updates fail closed while installed
// plugins remain usable.
var OfficialPluginPublicKeysJSON string

var (
	ErrPluginSignatureUnavailable = errors.New("官方插件签名公钥未配置，市场安装与自动更新已禁用")
	ErrUnknownPluginSigningKey    = errors.New("插件使用了客户端不信任的签名密钥")
	ErrInvalidPluginSignature     = errors.New("插件市场签名无效")
	ErrInvalidPluginKeyConfig     = errors.New("内置官方插件公钥配置无效")
	ErrLegacyMarketSchemaExpired  = errors.New("插件市场 v2 过渡期已结束，需要已签名的 v3 索引")
)

// GitHubProxyPrefix is the default prefix for wrapping GitHub download URLs through
// a mirror. It is now empty: the built-in market/app-update URLs resolve to the
// project's own edge CDN (see DefaultMarketURL / DefaultAppUpdateURL), so no GitHub
// proxy is needed by default. It still applies only if a user points
// PluginMarketURL/AppUpdateURL back at a github.com host. Override at launch via the
// SEKAITEXT_GH_PROXY env var to re-enable a mirror. Keep the trailing / if set.
const GitHubProxyPrefix = ""

// githubProxyPrefix is the effective mirror prefix, overridable at launch via the
// SEKAITEXT_GH_PROXY env var ("off"/"none"/"" disables the mirror and goes direct;
// any other value is used as the prefix). Defaults to GitHubProxyPrefix. Lets a user
// swap or disable a dead-but-resolving mirror without a rebuild.
var githubProxyPrefix = func() string {
	v, ok := os.LookupEnv("SEKAITEXT_GH_PROXY")
	if !ok {
		return GitHubProxyPrefix
	}
	if v == "" || strings.EqualFold(v, "off") || strings.EqualFold(v, "none") {
		return ""
	}
	if !strings.HasSuffix(v, "/") {
		v += "/"
	}
	return v
}()

// githubHosts are the GitHub-owned hosts worth routing through the mirror. Other
// hosts (a self-hosted index/CDN via Settings.PluginMarketURL) are left untouched.
var githubHosts = map[string]bool{
	"github.com":                    true,
	"raw.githubusercontent.com":     true,
	"objects.githubusercontent.com": true,
	"codeload.github.com":           true,
	"gist.githubusercontent.com":    true,
}

// mirrorCandidates returns the URLs to try in order: the configured mirror first
// (GitHub hosts only, when githubProxyPrefix is set), then the original. A
// non-GitHub or unparseable URL yields just itself, so the OSS/edge CDN (and any
// self-hosted index) is fetched directly, unaffected.
// 候选先经 routeDownloadURL 按「下载源」设置重排（CDN 加速 / GitHub 直连互为兜底）。
func mirrorCandidates(rawurl string) []string {
	var out []string
	for _, cand := range routeDownloadURL(rawurl) {
		u, err := url.Parse(cand)
		if err == nil && githubProxyPrefix != "" && githubHosts[strings.ToLower(u.Hostname())] {
			// The mirror expects the full original URL (scheme included) appended.
			out = append(out, githubProxyPrefix+cand)
		}
		out = append(out, cand)
	}
	return out
}

// mirrorFetch GETs rawurl, trying each mirrorCandidates URL in order until one
// returns HTTP 200. firstClient (shorter timeout) is used for every candidate
// except the last so a dead mirror fails over fast; lastClient (full timeout) is
// used for the final official attempt. The response Body is the caller's to close.
func mirrorFetch(firstClient, lastClient *http.Client, rawurl string) (*http.Response, error) {
	candidates := mirrorCandidates(rawurl)
	var lastErr error
	for i, cand := range candidates {
		client := lastClient
		if i < len(candidates)-1 {
			client = firstClient // a fallback remains → don't wait the full timeout
		}
		req, err := http.NewRequest(http.MethodGet, cand, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "SekaiText-PluginMarket")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no url candidates")
	}
	return nil, lastErr
}

// MarketEntry is one plugin listing in the remote index.
type MarketEntry struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Version            string `json:"version"`
	Description        string `json:"description,omitempty"`
	Author             string `json:"author,omitempty"`
	Icon               string `json:"icon,omitempty"`
	MinHostVersion     string `json:"minHostVersion,omitempty"`
	Download           string `json:"download"` // URL to the .sekplugin
	SHA256             string `json:"sha256"`
	Publisher          string `json:"publisher,omitempty"`
	KeyID              string `json:"keyId,omitempty"`
	SignatureAlgorithm string `json:"signatureAlgorithm,omitempty"`
	PackageSignature   string `json:"packageSignature,omitempty"`
	Homepage           string `json:"homepage,omitempty"`
	Sequence           uint64 `json:"sequence,omitempty"`
	ExpiresAt          string `json:"expiresAt,omitempty"`
	MetadataSignature  string `json:"metadataSignature,omitempty"`
}

// MarketIndex is the remote registry document.
type MarketIndex struct {
	Version            int           `json:"version"`
	Plugins            []MarketEntry `json:"plugins"`
	Publisher          string        `json:"publisher,omitempty"`
	KeyID              string        `json:"keyId,omitempty"`
	SignatureAlgorithm string        `json:"signatureAlgorithm,omitempty"`
	Sequence           uint64        `json:"sequence,omitempty"`
	ExpiresAt          string        `json:"expiresAt,omitempty"`
	SnapshotSignature  string        `json:"snapshotSignature,omitempty"`
}

// MarketListing augments a MarketEntry with local state relative to the
// installed plugins, for the frontend to render install/update/installed.
type MarketListing struct {
	MarketEntry
	Installed          bool   `json:"installed"`
	InstalledVersion   string `json:"installedVersion,omitempty"`
	UpdateAvailable    bool   `json:"updateAvailable"`
	ReinstallAvailable bool   `json:"reinstallAvailable"`
	SignatureVerified  bool   `json:"signatureVerified"`
	SignatureError     string `json:"signatureError,omitempty"`
}

// MarketService fetches the remote plugin index and installs plugins only after
// authenticating the signed entry and downloaded package digest.
type MarketService struct {
	client       *http.Client // full timeout — used for the official-source attempt
	fastClient   *http.Client // shorter timeout — fail over fast when the mirror is dead
	store        *PluginStore
	trustedKeys  map[string]ed25519.PublicKey
	keyConfigErr error
}

func NewMarketService(store *PluginStore) *MarketService {
	keys, keyConfigErr := parseOfficialPluginPublicKeys(OfficialPluginPublicKeysJSON)
	// TLS certs are verified (default transport): both the index and the downloaded
	// .sekplugin feed dynamically imported JS, so an unverified connection is a
	// MITM→RCE vector. The embedded Ed25519 keys authenticate each package digest;
	// SHA-256 then binds the downloaded bytes to that authenticated digest.
	return &MarketService{
		client:       &http.Client{Timeout: 60 * time.Second},
		fastClient:   &http.Client{Timeout: 25 * time.Second},
		store:        store,
		trustedKeys:  keys,
		keyConfigErr: keyConfigErr,
	}
}

func parseOfficialPluginPublicKeys(raw string) (map[string]ed25519.PublicKey, error) {
	keys := map[string]ed25519.PublicKey{}
	if strings.TrimSpace(raw) == "" {
		return keys, nil
	}
	var encoded map[string]string
	if err := json.Unmarshal([]byte(raw), &encoded); err != nil {
		return nil, fmt.Errorf("%w: JSON 解析失败", ErrInvalidPluginKeyConfig)
	}
	for keyID, value := range encoded {
		if !validSigningKeyID(keyID) {
			return nil, fmt.Errorf("%w: 非法 keyId %q", ErrInvalidPluginKeyConfig, keyID)
		}
		decoded, err := decodeCanonicalBase64(value)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: keyId %q 不是 32 字节标准 Base64 Ed25519 公钥", ErrInvalidPluginKeyConfig, keyID)
		}
		keys[keyID] = ed25519.PublicKey(append([]byte(nil), decoded...))
	}
	return keys, nil
}

func validSigningKeyID(keyID string) bool {
	if keyID == "" || len(keyID) > 64 {
		return false
	}
	for _, c := range keyID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func decodeCanonicalBase64(value string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != value {
		return nil, errors.New("invalid standard base64")
	}
	return decoded, nil
}

// canonicalMarketEntryPayload is the exact UTF-8 byte sequence signed by the
// official release workflow. Length prefixes make embedded delimiters and JSON
// encoder differences unambiguous. Field order and the trailing newline are
// part of the v1 format and must not change.
func canonicalMarketEntryPayload(entry *MarketEntry) []byte {
	var b strings.Builder
	b.WriteString(pluginSignatureHeader)
	canonicalField(&b, "publisher", entry.Publisher)
	canonicalField(&b, "keyId", entry.KeyID)
	canonicalField(&b, "algorithm", entry.SignatureAlgorithm)
	canonicalField(&b, "id", entry.ID)
	canonicalField(&b, "version", entry.Version)
	canonicalField(&b, "download", entry.Download)
	canonicalField(&b, "sha256", entry.SHA256)
	return []byte(b.String())
}

func canonicalMarketMetadataPayload(entry *MarketEntry) []byte {
	var b strings.Builder
	b.WriteString(pluginMetadataHeader)
	canonicalField(&b, "publisher", entry.Publisher)
	canonicalField(&b, "keyId", entry.KeyID)
	canonicalField(&b, "algorithm", entry.SignatureAlgorithm)
	canonicalField(&b, "id", entry.ID)
	canonicalField(&b, "name", entry.Name)
	canonicalField(&b, "version", entry.Version)
	canonicalField(&b, "description", entry.Description)
	canonicalField(&b, "author", entry.Author)
	canonicalField(&b, "icon", entry.Icon)
	canonicalField(&b, "minHostVersion", entry.MinHostVersion)
	canonicalField(&b, "download", entry.Download)
	canonicalField(&b, "sha256", entry.SHA256)
	canonicalField(&b, "homepage", entry.Homepage)
	canonicalField(&b, "sequence", strconv.FormatUint(entry.Sequence, 10))
	canonicalField(&b, "expiresAt", entry.ExpiresAt)
	return []byte(b.String())
}

// canonicalMarketSnapshotPayload authenticates the complete ordered member set.
// Each metadata signature already binds its entry's complete visible metadata,
// package digest, sequence, and expiry.
func canonicalMarketSnapshotPayload(index *MarketIndex) []byte {
	var b strings.Builder
	b.WriteString(pluginSnapshotHeader)
	canonicalField(&b, "publisher", index.Publisher)
	canonicalField(&b, "keyId", index.KeyID)
	canonicalField(&b, "algorithm", index.SignatureAlgorithm)
	canonicalField(&b, "version", strconv.Itoa(index.Version))
	canonicalField(&b, "sequence", strconv.FormatUint(index.Sequence, 10))
	canonicalField(&b, "expiresAt", index.ExpiresAt)
	canonicalField(&b, "pluginCount", strconv.Itoa(len(index.Plugins)))
	for i := range index.Plugins {
		canonicalField(&b, "pluginId", index.Plugins[i].ID)
		canonicalField(&b, "metadataSignature", index.Plugins[i].MetadataSignature)
	}
	return []byte(b.String())
}

func canonicalField(b *strings.Builder, name, value string) {
	b.WriteString(name)
	b.WriteByte(':')
	b.WriteString(strconv.Itoa(len(value)))
	b.WriteByte(':')
	b.WriteString(value)
	b.WriteByte('\n')
}

func (m *MarketService) verifyEntrySignature(entry *MarketEntry, indexVersion int) error {
	if m.keyConfigErr != nil {
		return m.keyConfigErr
	}
	if len(m.trustedKeys) == 0 {
		return ErrPluginSignatureUnavailable
	}
	if entry.Publisher != OfficialPluginPublisher {
		return fmt.Errorf("%w: publisher 不是官方发布者", ErrInvalidPluginSignature)
	}
	if entry.SignatureAlgorithm != pluginSignatureAlgorithm {
		return fmt.Errorf("%w: 不支持的签名算法", ErrInvalidPluginSignature)
	}
	key, ok := m.trustedKeys[entry.KeyID]
	if !ok {
		return fmt.Errorf("%w: keyId=%s", ErrUnknownPluginSigningKey, entry.KeyID)
	}
	if decoded, err := hex.DecodeString(entry.SHA256); err != nil || len(decoded) != sha256.Size || strings.ToLower(entry.SHA256) != entry.SHA256 {
		return fmt.Errorf("%w: sha256 必须是 64 位小写十六进制", ErrInvalidPluginSignature)
	}
	signature, err := decodeCanonicalBase64(entry.PackageSignature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: packageSignature 不是标准 Base64 Ed25519 签名", ErrInvalidPluginSignature)
	}
	if !ed25519.Verify(key, canonicalMarketEntryPayload(entry), signature) {
		return fmt.Errorf("%w: 签名与市场条目不匹配", ErrInvalidPluginSignature)
	}
	if indexVersion == 3 {
		if entry.Sequence == 0 || entry.Sequence > maxSafeSequence {
			return fmt.Errorf("%w: sequence 必须为正整数", ErrInvalidPluginSignature)
		}
		expiresAt, ok := parseCanonicalMarketExpiry(entry.ExpiresAt)
		if !ok {
			return fmt.Errorf("%w: expiresAt 必须是规范 RFC3339 时间", ErrInvalidPluginSignature)
		}
		if !expiresAt.After(time.Now().UTC()) {
			return fmt.Errorf("%w: 市场条目已过期", ErrInvalidPluginSignature)
		}
		metadataSignature, err := decodeCanonicalBase64(entry.MetadataSignature)
		if err != nil || len(metadataSignature) != ed25519.SignatureSize {
			return fmt.Errorf("%w: metadataSignature 不是标准 Base64 Ed25519 签名", ErrInvalidPluginSignature)
		}
		if !ed25519.Verify(key, canonicalMarketMetadataPayload(entry), metadataSignature) {
			return fmt.Errorf("%w: 用户可见元数据签名无效", ErrInvalidPluginSignature)
		}
	}
	return nil
}

func (m *MarketService) verifySnapshotSignature(index *MarketIndex) error {
	if m.keyConfigErr != nil {
		return m.keyConfigErr
	}
	if len(m.trustedKeys) == 0 {
		return ErrPluginSignatureUnavailable
	}
	key, ok := m.trustedKeys[index.KeyID]
	if !ok {
		return fmt.Errorf("%w: keyId=%s", ErrUnknownPluginSigningKey, index.KeyID)
	}
	signature, err := decodeCanonicalBase64(index.SnapshotSignature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: snapshotSignature 不是标准 Base64 Ed25519 签名", ErrInvalidPluginSignature)
	}
	if !ed25519.Verify(key, canonicalMarketSnapshotPayload(index), signature) {
		return fmt.Errorf("%w: 完整市场快照签名无效", ErrInvalidPluginSignature)
	}
	return nil
}

// fetch GETs rawurl through the GitHub mirror (then official) — see mirrorFetch.
func (m *MarketService) fetch(rawurl string) (*http.Response, error) {
	return mirrorFetch(m.fastClient, m.client, rawurl)
}

// FetchIndex retrieves + parses the remote index. url empty → DefaultMarketURL.
func (m *MarketService) FetchIndex(url string) (MarketIndex, error) {
	var idx MarketIndex
	if strings.TrimSpace(url) == "" {
		url = DefaultMarketURL
	}
	resp, err := m.fetch(url)
	if err != nil {
		return idx, fmt.Errorf("index fetch failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return idx, err
	}
	if len(data) > 4<<20 {
		return idx, errors.New("market index exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&idx); err != nil {
		return idx, errors.New("invalid market index JSON")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return idx, errors.New("market index contains trailing JSON")
	}
	if err := validateMarketIndex(&idx); err != nil {
		return idx, err
	}
	if idx.Version == 2 && !legacyMarketV2Allowed(time.Now().UTC()) {
		return idx, ErrLegacyMarketSchemaExpired
	}
	if idx.Version == 3 {
		for i := range idx.Plugins {
			entry := &idx.Plugins[i]
			if err := m.verifyEntrySignature(entry, idx.Version); err != nil {
				return idx, err
			}
		}
		if err := m.verifySnapshotSignature(&idx); err != nil {
			return idx, err
		}
		if err := m.store.AcceptMarketIndex(idx.Version, idx.KeyID, idx.Sequence, idx.SnapshotSignature); err != nil {
			return idx, err
		}
	} else if err := m.store.AcceptMarketIndex(idx.Version, "", 0, ""); err != nil {
		return idx, err
	}
	return idx, nil
}

func legacyMarketV2Allowed(now time.Time) bool {
	cutoff, err := time.Parse(time.RFC3339, LegacyMarketV2Cutoff)
	return err == nil && now.Before(cutoff)
}

func validateMarketIndex(index *MarketIndex) error {
	if index.Version != 2 && index.Version != 3 {
		return errors.New("unsupported market index version")
	}
	if len(index.Plugins) == 0 || len(index.Plugins) > 1000 {
		return errors.New("market index plugins must be a non-empty bounded array")
	}
	if index.Version == 2 {
		if index.Publisher != "" || index.KeyID != "" || index.SignatureAlgorithm != "" ||
			index.Sequence != 0 || index.ExpiresAt != "" || index.SnapshotSignature != "" {
			return errors.New("market v2 index contains v3 snapshot fields")
		}
	} else {
		if index.Publisher != OfficialPluginPublisher || !validSigningKeyID(index.KeyID) ||
			index.SignatureAlgorithm != pluginSignatureAlgorithm || index.Sequence == 0 ||
			index.Sequence > maxSafeSequence || index.SnapshotSignature == "" {
			return errors.New("market v3 index has invalid snapshot signing metadata")
		}
		expiresAt, ok := parseCanonicalMarketExpiry(index.ExpiresAt)
		if !ok || !expiresAt.After(time.Now().UTC()) {
			return errors.New("market v3 snapshot is expired or has an invalid expiresAt")
		}
	}
	seen := make(map[string]bool, len(index.Plugins))
	for i := range index.Plugins {
		entry := &index.Plugins[i]
		if !validPluginID(entry.ID) || seen[entry.ID] {
			return errors.New("market index contains an invalid or duplicate plugin id")
		}
		seen[entry.ID] = true
		if strings.TrimSpace(entry.Name) == "" || len(entry.Name) > 200 || len(entry.Description) > 4000 ||
			len(entry.Author) > 200 || len(entry.Icon) > 100 {
			return fmt.Errorf("market entry %s has invalid display metadata", entry.ID)
		}
		if !validStableMarketSemver(entry.Version) {
			return fmt.Errorf("market entry %s version is not strict semver", entry.ID)
		}
		if entry.MinHostVersion != "" {
			if !validStableMarketSemver(entry.MinHostVersion) {
				return fmt.Errorf("market entry %s minHostVersion is not strict semver", entry.ID)
			}
		}
		if !validMarketURL(entry.Download, true) {
			return fmt.Errorf("market entry %s download must use HTTPS", entry.ID)
		}
		if entry.Homepage != "" && !validMarketURL(entry.Homepage, false) {
			return fmt.Errorf("market entry %s homepage must use HTTPS", entry.ID)
		}
		if !validSHA256(entry.SHA256) || entry.Publisher != OfficialPluginPublisher ||
			!validSigningKeyID(entry.KeyID) || entry.SignatureAlgorithm != pluginSignatureAlgorithm ||
			entry.PackageSignature == "" {
			return fmt.Errorf("market entry %s has invalid signing metadata", entry.ID)
		}
		if index.Version == 2 {
			if entry.Sequence != 0 || entry.ExpiresAt != "" || entry.MetadataSignature != "" {
				return fmt.Errorf("market entry %s uses v3 fields in a v2 index", entry.ID)
			}
		} else if entry.Sequence == 0 || entry.Sequence > maxSafeSequence || entry.ExpiresAt == "" || entry.MetadataSignature == "" {
			return fmt.Errorf("market entry %s is missing v3 replay protection", entry.ID)
		} else if entry.Publisher != index.Publisher || entry.KeyID != index.KeyID ||
			entry.SignatureAlgorithm != index.SignatureAlgorithm || entry.Sequence != index.Sequence ||
			entry.ExpiresAt != index.ExpiresAt {
			return fmt.Errorf("market entry %s signing metadata does not match the snapshot", entry.ID)
		}
	}
	return nil
}

func parseCanonicalMarketExpiry(value string) (time.Time, bool) {
	if len(value) != len("2006-01-02T15:04:05Z") || !strings.HasSuffix(value, "Z") {
		return time.Time{}, false
	}
	parsed, err := time.Parse("2006-01-02T15:04:05Z", value)
	return parsed, err == nil && parsed.UTC().Format("2006-01-02T15:04:05Z") == value
}

func validStableMarketSemver(value string) bool {
	if strings.ContainsAny(value, "-+") {
		return false
	}
	_, ok := parseSemver(value)
	return ok
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}

func validMarketURL(value string, allowLoopbackHTTP bool) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	host := strings.ToLower(parsed.Hostname())
	return allowLoopbackHTTP && parsed.Scheme == "http" && (host == "127.0.0.1" || host == "localhost" || host == "::1")
}

// Listings fetches the index and annotates each entry with installed/update state.
func (m *MarketService) Listings(url string) ([]MarketListing, error) {
	idx, err := m.FetchIndex(url)
	if err != nil {
		return nil, err
	}
	installed, err := m.store.List()
	if err != nil {
		return nil, err
	}
	byID := map[string]PluginInfo{}
	for _, p := range installed {
		byID[p.ID] = p
	}
	out := make([]MarketListing, 0, len(idx.Plugins))
	for _, e := range idx.Plugins {
		l := MarketListing{MarketEntry: e}
		if err := m.verifyEntrySignature(&e, idx.Version); err != nil {
			l.SignatureError = err.Error()
		} else {
			l.SignatureVerified = true
		}
		if p, ok := byID[e.ID]; ok {
			l.Installed = true
			l.InstalledVersion = p.Version
			l.UpdateAvailable = updateVersionAllowed(e.Version, p.Version)
			l.ReinstallAvailable = p.Local && compareSemverStrings(e.Version, p.Version) >= 0
		}
		out = append(out, l)
	}
	return out, nil
}

// Install finds a signed entry by id and installs its verified package.
// hostVersion gates minHostVersion inside the package manifest.
func (m *MarketService) Install(url, id, hostVersion string) (PluginManifest, error) {
	var zero PluginManifest
	idx, err := m.FetchIndex(url)
	if err != nil {
		return zero, err
	}
	var entry *MarketEntry
	for i := range idx.Plugins {
		if idx.Plugins[i].ID == id {
			entry = &idx.Plugins[i]
			break
		}
	}
	if entry == nil {
		return zero, errors.New("plugin not found in market: " + id)
	}
	installed, err := m.store.List()
	if err != nil {
		return zero, err
	}
	var expectedCurrent *PluginInfo
	for i := range installed {
		if installed[i].ID == id {
			expectedCurrent = &installed[i]
			if compareSemverStrings(entry.Version, installed[i].Version) < 0 {
				return zero, ErrPluginDowngrade
			}
			break
		}
	}
	return m.installEntry(entry, idx.Version, hostVersion, expectedCurrent, true)
}

// installEntry authenticates the signed digest before making a network request,
// downloads the .sekplugin, verifies its bytes, then uses the verified-market
// install contract. Local-file installs deliberately use a separate path.
func (m *MarketService) installEntry(entry *MarketEntry, indexVersion int, hostVersion string, expectedCurrent *PluginInfo, requireUnchanged bool) (PluginManifest, error) {
	var zero PluginManifest
	if err := m.verifyEntrySignature(entry, indexVersion); err != nil {
		return zero, err
	}
	if strings.TrimSpace(entry.Download) == "" {
		return zero, errors.New("market entry missing download url")
	}
	tmp, err := m.downloadToTemp(entry.Download)
	if err != nil {
		return zero, err
	}
	defer os.Remove(tmp)

	sum, err := fileSHA256(tmp)
	if err != nil {
		return zero, err
	}
	if !strings.EqualFold(sum, entry.SHA256) {
		return zero, errors.New("下载校验失败（sha256 不匹配）")
	}
	provenance := PluginProvenance{
		Source:       "verified-market",
		Publisher:    entry.Publisher,
		KeyID:        entry.KeyID,
		SHA256:       entry.SHA256,
		IndexVersion: indexVersion,
		Sequence:     entry.Sequence,
	}
	if requireUnchanged {
		return m.store.installVerifiedMarketCAS(tmp, hostVersion, entry.ID, entry.Version, provenance, expectedCurrent)
	}
	return m.store.installVerifiedMarket(tmp, hostVersion, entry.ID, entry.Version, provenance, expectedCurrent)
}

// PluginUpdateResult is one plugin's outcome in an AutoUpdate sweep.
type PluginUpdateResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	FromVersion string `json:"fromVersion,omitempty"`
	ToVersion   string `json:"toVersion,omitempty"`
	Error       string `json:"error,omitempty"`
}

// AutoUpdateSummary reports an AutoUpdate sweep: which installed plugins were
// upgraded and which failed (per-plugin failures are non-fatal).
type AutoUpdateSummary struct {
	Updated []PluginUpdateResult `json:"updated"`
	Failed  []PluginUpdateResult `json:"failed"`
}

// AutoUpdate only replaces non-local plugins with a newer package whose official
// signature and downloaded digest both verify. Per-plugin verification failures
// are reported without affecting the currently installed payload.
func (m *MarketService) AutoUpdate(url, hostVersion string) (AutoUpdateSummary, error) {
	// Initialize as empty (non-nil) slices so the JSON response is always
	// {"updated":[],"failed":[]} and never null — the frontend reads
	// sum.updated.length / sum.failed unguarded.
	sum := AutoUpdateSummary{Updated: []PluginUpdateResult{}, Failed: []PluginUpdateResult{}}
	idx, err := m.FetchIndex(url)
	if err != nil {
		return sum, err
	}
	installed, err := m.store.List()
	if err != nil {
		return sum, err
	}
	have := map[string]PluginInfo{}
	for _, p := range installed {
		have[p.ID] = p
	}
	for i := range idx.Plugins {
		e := idx.Plugins[i]
		current, ok := have[e.ID]
		if !ok || current.Local || !updateVersionAllowed(e.Version, current.Version) {
			continue
		}
		if _, err := m.installEntry(&e, idx.Version, hostVersion, &current, true); err != nil {
			sum.Failed = append(sum.Failed, PluginUpdateResult{ID: e.ID, Name: e.Name, FromVersion: current.Version, ToVersion: e.Version, Error: err.Error()})
			continue
		}
		sum.Updated = append(sum.Updated, PluginUpdateResult{ID: e.ID, Name: e.Name, FromVersion: current.Version, ToVersion: e.Version})
	}
	return sum, nil
}

func (m *MarketService) downloadToTemp(url string) (string, error) {
	resp, err := m.fetch(url)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	f, err := os.CreateTemp("", "sekplugin-*.sekplugin")
	if err != nil {
		return "", err
	}
	// Read one byte beyond the cap so oversized packages are rejected rather than
	// silently truncated and reported as a misleading digest mismatch.
	written, err := io.Copy(f, io.LimitReader(resp.Body, (128<<20)+1))
	if err == nil && written > 128<<20 {
		err = errors.New("plugin package exceeds download size limit")
	}
	// A final-flush failure on Close() (e.g. disk full) must not be swallowed,
	// or a truncated temp file is returned as success and only surfaces later
	// as a misleading sha256 mismatch.
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
