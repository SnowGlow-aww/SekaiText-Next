package mobilecore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sekaitext/backend/internal/service"
)

func writeMobileCatalogGeneration(t *testing.T, root string, generation uint64, files map[string]string) {
	t.Helper()
	catalogDir := filepath.Join(root, "catalog")
	generationName := fmt.Sprintf("generation-%020d-1", generation)
	generationDir := filepath.Join(catalogDir, ".catalog-generations", generationName)
	if err := os.MkdirAll(generationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(generationDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := fmt.Sprintf(`{"version":1,"generation":%d,"dir":%q}`, generation, generationName)
	if err := os.WriteFile(filepath.Join(catalogDir, "catalog-current.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitForMobileUpdateState(t *testing.T, want string) storyUpdateProgressResponse {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		encoded, err := StoryUpdateProgress()
		if err != nil {
			t.Fatal(err)
		}
		var progress storyUpdateProgressResponse
		if err := json.Unmarshal([]byte(encoded), &progress); err != nil {
			t.Fatal(err)
		}
		if progress.Status == want {
			return progress
		}
		if time.Now().After(deadline) {
			t.Fatalf("update status remained %q, want %q: %s", progress.Status, want, encoded)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTranslationRoundTrip(t *testing.T) {
	createdJSON, err := CreateTranslation(`{"sourceTalks":[{"speaker":"ミク","text":"こんにちは","charIndex":0}],"jp":false}`)
	if err != nil {
		t.Fatal(err)
	}
	var created []map[string]any
	if err := json.Unmarshal([]byte(createdJSON), &created); err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0]["speaker"] != "MIKU" {
		t.Fatalf("unexpected template: %s", createdJSON)
	}

	created[0]["text"] = "你好"
	talks, _ := json.Marshal(created)
	serialized, err := SerializeTranslation(`{"talks":` + string(talks) + `,"saveN":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(serialized, "你好") {
		t.Fatalf("serialized content missing translation: %s", serialized)
	}
}

func TestLoadTranslation(t *testing.T) {
	got, err := LoadTranslation("ミク：你好\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"speaker":"ミク"`) {
		t.Fatalf("unexpected load result: %s", got)
	}
}

func TestLegacyStoryCatalogIsNotReady(t *testing.T) {
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"events.json":    `[{"id":1,"kdyicr_id":1,"title":"活动","name":"event","chapters":[{"title":"第1话","assetName":"event_1_01"}],"cards":[1]}]`,
		"festivals.json": `[{"id":1,"isBirthday":false,"cards":[1]}]`,
		"cards.json":     `[{"id":1,"characterId":1,"cardNo":"001","birthday":false}]`,
		"mainStory.json": `[{"unit":"light_sound","assetName":"unit","chapters":[{"title":"第1话","assetName":"main_01"}]}]`,
		"areatalks.json": `[{"id":1,"talkid":"0001","areaId":1,"characterIds":[1],"scenarioId":"area_1","type":"normal"}]`,
		"greets.json":    `[{"theme":{"ch":"问候","en":"greet"},"year":2026,"greets":[]}]`,
		"specials.json":  `[{"title":"特殊","dirName":"special","fileName":"special_1"}]`,
	} {
		if err := os.WriteFile(filepath.Join(catalogDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Initialize(root, filepath.Join(root, "cache-root")); err != nil {
		t.Fatal(err)
	}
	status, err := StoryCatalogStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, `"ready":false`) || !strings.Contains(status, `"generation":0`) {
		t.Fatalf("legacy catalog must not be ready: %s", status)
	}
}

func TestStoryNavigationAndLocalLoad(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"events.json":    `[{"id":1,"kdyicr_id":1,"title":"活动","name":"event","chapters":[{"title":"第1话","assetName":"event_1_01"}],"cards":[1]}]`,
		"festivals.json": `[{"id":1,"isBirthday":false,"cards":[1]}]`,
		"cards.json":     `[{"id":1,"characterId":1,"cardNo":"001","birthday":false}]`,
		"mainStory.json": `[{"unit":"light_sound","assetName":"unit_light_sound","chapters":[{"title":"第1话","assetName":"unit_light_sound_01"}]}]`,
		"areatalks.json": `[{"id":1,"talkid":"0001","areaId":1,"characterIds":[1],"scenarioId":"area_1","type":"normal","addEventId":1,"releaseEventId":1}]`,
		"greets.json":    `[]`,
		"specials.json":  `[{"title":"特殊","dirName":"special","fileName":"special_1"}]`,
	}
	writeMobileCatalogGeneration(t, root, 7, files)
	if err := Initialize(root, filepath.Join(root, "cache-root")); err != nil {
		t.Fatal(err)
	}

	types, err := StoryTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(types, `"主线剧情"`) {
		t.Fatalf("story types missing main story: %s", types)
	}
	indices, err := StoryIndex(`{"type":"主线剧情","sort":""}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indices, `"label"`) {
		t.Fatalf("story index missing catalog entry: %s", indices)
	}
	chapters, err := StoryChapters(`{"type":"主线剧情","sort":"","index":"0"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chapters, `第1话`) {
		t.Fatalf("story chapters missing catalog entry: %s", chapters)
	}
	status, err := StoryCatalogStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, `"ready":true`) || !strings.Contains(status, `"generation":7`) {
		t.Fatalf("catalog should be ready at generation 7: %s", status)
	}

	loaded, err := StoryLoadLocal(`{
		"ScenarioId":"unit_test_01",
		"Snippets":[{"Action":1,"ReferenceIndex":0}],
		"TalkData":[{"WindowDisplayName":"初音ミク","Body":"こんにちは","Voices":[]}]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded, `"scenarioId":"unit_test_01"`) || !strings.Contains(loaded, `"text":"こんにちは"`) {
		t.Fatalf("unexpected local story result: %s", loaded)
	}
}

func TestIdleStoryUpdateProgressIsNotRunning(t *testing.T) {
	root := t.TempDir()
	if err := Initialize(root, filepath.Join(root, "cache-root")); err != nil {
		t.Fatal(err)
	}
	encoded, err := StoryUpdateProgress()
	if err != nil {
		t.Fatal(err)
	}
	var progress storyUpdateProgressResponse
	if err := json.Unmarshal([]byte(encoded), &progress); err != nil {
		t.Fatal(err)
	}
	if progress.Status != "idle" || !progress.Done {
		t.Fatalf("idle progress = %+v, want idle and done", progress)
	}
}

func TestInitializePreservesInFlightUpdateForSameRuntime(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache-root")
	if err := Initialize(root, cacheDir); err != nil {
		t.Fatal(err)
	}

	oldRunner := runStoryCatalogUpdate
	started := make(chan struct{})
	release := make(chan struct{})
	runnerReturned := make(chan struct{})
	runStoryCatalogUpdate = func(_ *service.ListManager, _ string, progress *service.ProgressTracker) error {
		close(started)
		<-release
		progress.SetTotal(1)
		progress.Advance("published")
		progress.Done()
		close(runnerReturned)
		return nil
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		runStoryCatalogUpdate = oldRunner
	}()

	if response, err := UpdateStoryCatalog(); err != nil || !strings.Contains(response, `"status":"started"`) {
		t.Fatalf("start update response=%q err=%v", response, err)
	}
	<-started
	runtimeState.mu.RLock()
	beforeManager := runtimeState.listManager
	beforeProgress := runtimeState.updateProgress
	runtimeState.mu.RUnlock()

	if err := Initialize(root, cacheDir); err != nil {
		t.Fatal(err)
	}
	runtimeState.mu.RLock()
	afterManager := runtimeState.listManager
	afterProgress := runtimeState.updateProgress
	stillUpdating := runtimeState.updateInFlight
	runtimeState.mu.RUnlock()
	if afterManager != beforeManager || afterProgress != beforeProgress || !stillUpdating {
		t.Fatalf("same-runtime initialization replaced in-flight state")
	}
	if progress := waitForMobileUpdateState(t, "running"); progress.Done {
		t.Fatalf("running update was marked done: %+v", progress)
	}

	close(release)
	<-runnerReturned
	if progress := waitForMobileUpdateState(t, "done"); !progress.Done {
		t.Fatalf("completed update was not marked done: %+v", progress)
	}
}

func TestOldUpdateCompletionCannotOverwriteReinitializedRuntime(t *testing.T) {
	oldRoot := t.TempDir()
	if err := Initialize(oldRoot, filepath.Join(oldRoot, "cache-root")); err != nil {
		t.Fatal(err)
	}

	oldRunner := runStoryCatalogUpdate
	started := make(chan struct{})
	release := make(chan struct{})
	runnerReturned := make(chan struct{})
	oldErr := errors.New("old runtime update failed")
	var derivedState atomic.Int32
	runStoryCatalogUpdate = func(lm *service.ListManager, _ string, _ *service.ProgressTracker) error {
		close(started)
		<-release
		mobileCharacter2DPublishGuard(lm)(func() { derivedState.Store(1) })
		close(runnerReturned)
		return oldErr
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		runStoryCatalogUpdate = oldRunner
	}()

	if _, err := UpdateStoryCatalog(); err != nil {
		t.Fatal(err)
	}
	<-started
	newRoot := t.TempDir()
	if err := Initialize(newRoot, filepath.Join(newRoot, "cache-root")); err != nil {
		t.Fatal(err)
	}
	runtimeState.mu.RLock()
	newManager := runtimeState.listManager
	runtimeState.mu.RUnlock()
	mobileCharacter2DPublishGuard(newManager)(func() { derivedState.Store(2) })
	if progress := waitForMobileUpdateState(t, "idle"); progress.Error != "" {
		t.Fatalf("new runtime inherited old update error before completion: %+v", progress)
	}

	close(release)
	<-runnerReturned
	if got := derivedState.Load(); got != 2 {
		t.Fatalf("old runtime completion overwrote current character2d derived state: got marker %d", got)
	}
	time.Sleep(20 * time.Millisecond)
	encoded, err := StoryUpdateProgress()
	if err != nil {
		t.Fatal(err)
	}
	var progress storyUpdateProgressResponse
	if err := json.Unmarshal([]byte(encoded), &progress); err != nil {
		t.Fatal(err)
	}
	if progress.Status != "idle" || progress.Error != "" || strings.Contains(progress.Message, oldErr.Error()) {
		t.Fatalf("old update completion overwrote new runtime: %+v", progress)
	}
}

func TestScenarioAssetName(t *testing.T) {
	got := scenarioAssetName("https://example.test/path/012043_touya01.asset?v=1#fragment")
	if got != "012043_touya01" {
		t.Fatalf("scenarioAssetName = %q", got)
	}
}

func TestVoiceURL(t *testing.T) {
	if err := Initialize(t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	got, err := VoiceURL(`{"scenarioId":"event_001_01","voiceId":"voice_001","source":"haruki"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `https://storage.exmeaning.com/sekai-jp-assets/sound/scenario/voice/event_001_01/voice_001.mp3`) {
		t.Fatalf("unexpected voice URL: %s", got)
	}
}

func TestEditorOperations(t *testing.T) {
	seed := `[{
		"idx":1,"speaker":"MIKU","text":"旧文本","start":true,"end":true,
		"checked":true,"save":true,"dstidx":0
	}]`

	changed, err := ChangeText(`{
		"row":0,"text":"新文本","editorMode":0,
		"talks":` + seed + `,"dstTalks":` + seed + `,"referTalks":[]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(changed, `"text":"新文本"`) {
		t.Fatalf("change text did not update rows: %s", changed)
	}

	added, err := AddLine(`{
		"row":0,"talks":` + seed + `,"dstTalks":` + seed + `,"isProofreading":false
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var addResult editorMutationResponse
	if err := json.Unmarshal([]byte(added), &addResult); err != nil {
		t.Fatal(err)
	}
	if len(addResult.Talks) != 2 || len(addResult.DstTalks) != 2 {
		t.Fatalf("add line returned unexpected rows: %s", added)
	}

	replaced, err := ReplaceBrackets(`{
		"row":0,"brackets":"【】","talks":[{
			"idx":1,"speaker":"MIKU","text":"「测试」","start":true,"end":true,
			"checked":true,"save":true,"dstidx":0
		}],"dstTalks":[{
			"idx":1,"speaker":"MIKU","text":"「测试」","start":true,"end":true,
			"checked":true,"save":true,"dstidx":0
		}]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replaced, `【测试】`) {
		t.Fatalf("replace brackets did not update rows: %s", replaced)
	}

	compared, err := CompareText(`{
		"referTalks":` + seed + `,
		"checkTalks":[{
			"idx":1,"speaker":"MIKU","text":"新文本","start":true,"end":true,
			"checked":true,"save":true,"dstidx":0
		}],"editorMode":1
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compared, `"dstTalks"`) || !strings.Contains(compared, `"diff"`) {
		t.Fatalf("compare text returned incomplete result: %s", compared)
	}

	counted, err := SpeakerCount(`{
		"talks":` + seed + `,
		"sourceTalks":[{"speaker":"ミク","text":"原文","charIndex":0}]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(counted, `"japanese":"ミク"`) || !strings.Contains(counted, `"count":1`) {
		t.Fatalf("speaker count returned unexpected result: %s", counted)
	}
}
