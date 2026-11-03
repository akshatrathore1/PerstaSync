package corpus

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManifest(t *testing.T) {
	input := `{"repositories":[{"url":"https://example.org/project.git","commit":"0123456789abcdef0123456789abcdef01234567","file_count":0,"selection_criterion":"explicit test selection"}]}`
	manifest, err := Read(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Repositories[0].FileCount == nil || *manifest.Repositories[0].FileCount != 0 {
		t.Fatal("explicit zero file count was not preserved")
	}
	for _, field := range []string{"url", "commit", "file_count", "selection_criterion"} {
		t.Run("missing "+field, func(t *testing.T) {
			var raw struct {
				Repositories []map[string]json.RawMessage `json:"repositories"`
			}
			if err := json.Unmarshal([]byte(input), &raw); err != nil {
				t.Fatal(err)
			}
			delete(raw.Repositories[0], field)
			encoded, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Read(strings.NewReader(string(encoded))); err == nil {
				t.Fatalf("accepted missing %s", field)
			}
		})
	}
	for _, invalid := range []string{"{}", "null", `{"repositories":[]}`, input + input, input + "trailing"} {
		if _, err := Read(strings.NewReader(invalid)); err == nil {
			t.Fatalf("accepted invalid manifest: %s", invalid)
		}
	}
	for _, mutation := range []func(*Repository){
		func(repository *Repository) { repository.Commit = "main" },
		func(repository *Repository) { repository.Commit = strings.Repeat("z", 40) },
		func(repository *Repository) { repository.URL = "../project" },
		func(repository *Repository) { repository.URL = "https://example.org/project.git?key=value" },
		func(repository *Repository) { repository.SelectionCriterion = " " },
	} {
		entry := manifest.Repositories[0]
		mutation(&entry)
		if err := (Manifest{Repositories: []Repository{entry}}).Validate(); err == nil {
			t.Fatalf("accepted invalid repository: %+v", entry)
		}
	}
	duplicate := Manifest{Repositories: []Repository{manifest.Repositories[0], manifest.Repositories[0]}}
	if _, err := duplicate.Digest(); err == nil {
		t.Fatal("duplicate repository selection has a valid digest")
	}
}

func TestManifestDigest(t *testing.T) {
	count := uint64(1)
	first := Repository{URL: "https://example.org/first.git", Commit: strings.Repeat("a", 40), FileCount: &count, SelectionCriterion: "test input"}
	second := Repository{URL: "https://example.org/second.git", Commit: strings.Repeat("b", 64), FileCount: &count, SelectionCriterion: "another test input"}
	manifest := Manifest{Repositories: []Repository{second, first}}
	original, err := manifest.Digest()
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := (Manifest{Repositories: []Repository{first, second}}).Digest()
	if err != nil || original != reordered || manifest.Repositories[0].URL != second.URL {
		t.Fatalf("ordering changed identity or input: %s %s err=%v", original, reordered, err)
	}
	for _, mutation := range []func(*Repository){
		func(repository *Repository) { repository.Commit = strings.Repeat("c", 40) },
		func(repository *Repository) { repository.SelectionCriterion = "different selection" },
		func(repository *Repository) { changedCount := uint64(2); repository.FileCount = &changedCount },
	} {
		changed := first
		mutation(&changed)
		digest, err := (Manifest{Repositories: []Repository{changed, second}}).Digest()
		if err != nil || digest == original {
			t.Fatalf("changed manifest kept identity: %+v err=%v", changed, err)
		}
	}
}
