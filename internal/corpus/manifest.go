package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
)

type Repository struct {
	URL                string  `json:"url"`
	Commit             string  `json:"commit"`
	FileCount          *uint64 `json:"file_count"`
	SelectionCriterion string  `json:"selection_criterion"`
}

type Manifest struct {
	Repositories []Repository `json:"repositories"`
}

func Read(reader io.Reader) (Manifest, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Manifest{}, fmt.Errorf("manifest must contain exactly one JSON document")
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (manifest Manifest) Validate() error {
	if len(manifest.Repositories) == 0 {
		return fmt.Errorf("manifest requires an explicit repository selection")
	}
	seen := make(map[string]bool)
	for _, repository := range manifest.Repositories {
		if err := validateRepositoryURL(repository.URL); err != nil {
			return err
		}
		commit, err := hex.DecodeString(repository.Commit)
		if err != nil || (len(commit) != 20 && len(commit) != 32) {
			return fmt.Errorf("repository requires a full hexadecimal commit ID")
		}
		if repository.FileCount == nil {
			return fmt.Errorf("repository file count is missing")
		}
		if strings.TrimSpace(repository.SelectionCriterion) == "" {
			return fmt.Errorf("repository selection criterion is missing")
		}
		if seen[repository.URL] {
			return fmt.Errorf("repository URL occurs more than once")
		}
		seen[repository.URL] = true
	}
	return nil
}

func validateRepositoryURL(raw string) error {
	location, err := url.Parse(raw)
	if err != nil || location.Scheme != "https" || location.Hostname() == "" || location.Path == "" || location.User != nil || location.RawQuery != "" || location.Fragment != "" {
		return fmt.Errorf("repository requires an HTTPS URL without credentials, query, or fragment")
	}
	return nil
}

func (manifest Manifest) Digest() (string, error) {
	if err := manifest.Validate(); err != nil {
		return "", err
	}
	ordered := append([]Repository(nil), manifest.Repositories...)
	for index := range ordered {
		ordered[index].Commit = strings.ToLower(ordered[index].Commit)
	}
	sort.Slice(ordered, func(first, second int) bool { return ordered[first].URL < ordered[second].URL })
	encoded, err := json.Marshal(Manifest{Repositories: ordered})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
