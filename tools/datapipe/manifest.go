package datapipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Input is a file a generator read; its hash is part of the dataset identity.
type Input struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Manifest is written both next to the data and into the repository's datasets/stats.
// It deliberately has no timestamps: regenerating with the same inputs must reproduce it.
type Manifest struct {
	Dataset          string     `json:"dataset"`
	Source           string     `json:"source"`
	Generator        string     `json:"generator"`
	GeneratorCommit  string     `json:"generator_commit"`
	WorkingTreeDirty bool       `json:"working_tree_modified"`
	Seed             uint64     `json:"seed"`
	Inputs           []Input    `json:"inputs,omitempty"`
	Params           any        `json:"params"`
	Artifacts        []Artifact `json:"artifacts"`
	Stats            any        `json:"stats"`
	DataVersion      string     `json:"data_version"`
	Notes            []string   `json:"notes,omitempty"`
}

// Seal fills DataVersion = dataset/commit12/seed/hash of inputs+params+artifacts.
func (m *Manifest) Seal() error {
	b, err := json.Marshal(struct {
		Inputs    []Input
		Params    any
		Artifacts []Artifact
	}{m.Inputs, m.Params, m.Artifacts})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	commit := m.GeneratorCommit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	if m.WorkingTreeDirty {
		commit += "-dirty"
	}
	m.DataVersion = m.Dataset + "/" + commit + "/seed-" + jsonUint(m.Seed) + "/" + hex.EncodeToString(sum[:6])
	return nil
}

func jsonUint(v uint64) string { b, _ := json.Marshal(v); return string(b) }

func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func HashFile(path string) (Input, error) {
	f, err := os.Open(path)
	if err != nil {
		return Input{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return Input{}, err
	}
	return Input{Path: filepath.Base(path), SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// GitState returns HEAD and whether tracked or untracked files differ from it.
func GitState(repo string) (string, bool, error) {
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", false, errors.New("git rev-parse failed")
	}
	status, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		return "", false, errors.New("git status failed")
	}
	return strings.TrimSpace(string(head)), len(strings.TrimSpace(string(status))) > 0, nil
}
