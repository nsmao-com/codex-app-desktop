package codex

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Official Astra entry exported with `codex debug models --bundled` from
// @openai/codex 0.153.4 on 2026-09-05. Keep its instructions and tool metadata
// together: inventing a context size or copying another model's tool settings
// can disable native patch reporting even when API requests still succeed.
// The original package is distributed under Apache-2.0 by OpenAI.
//
//go:embed astra_model.json
var astraModelMetadata json.RawMessage

type modelCatalogBuffer struct{ data bytes.Buffer }

func (b *modelCatalogBuffer) Write(data []byte) (int, error) {
	if b.data.Len()+len(data) > 8*1024*1024 {
		return 0, fmt.Errorf("Codex model catalog exceeds 8 MiB")
	}
	return b.data.Write(data)
}

// prepareModelCatalog supplements older CLIs, without changing CODEX_HOME or
// the user's config/cache. Native and user-provided Astra entries always win.
// The temporary catalog must live until app-server and its children exit.
func prepareModelCatalog(ctx context.Context, spec commandSpec, workspace string, env []string) (string, func(), error) {
	cleanup := func() {}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	args := append(append([]string{}, spec.prefixArgs...), "debug", "models")
	command := exec.CommandContext(ctx, spec.path, args...)
	command.Dir = workspace
	command.Env = env
	configureProcess(command)
	var output modelCatalogBuffer
	command.Stdout = &output
	err := command.Run()
	if err != nil {
		return "", cleanup, fmt.Errorf("read model catalog: %w; update Codex CLI if Astra metadata is unavailable", err)
	}
	catalog, changed, err := supplementAstraCatalog(output.data.Bytes())
	if err != nil || !changed {
		return "", cleanup, err
	}
	dir, err := os.MkdirTemp("", "nice-codex-models-")
	if err != nil {
		return "", cleanup, err
	}
	path := filepath.Join(dir, "models.json")
	cleanup = func() {
		_ = os.Remove(path)
		_ = os.Remove(dir)
	}
	if err := os.WriteFile(path, catalog, 0o600); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func supplementAstraCatalog(data []byte) ([]byte, bool, error) {
	var catalog map[string]json.RawMessage
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, false, fmt.Errorf("decode Codex model catalog: %w", err)
	}
	var models []json.RawMessage
	if err := json.Unmarshal(catalog["models"], &models); err != nil || len(models) == 0 {
		return nil, false, fmt.Errorf("Codex returned an empty or invalid model catalog")
	}
	for _, model := range models {
		var entry struct {
			Slug string `json:"slug"`
		}
		if err := json.Unmarshal(model, &entry); err != nil {
			return nil, false, err
		}
		if entry.Slug == "gpt-6-astra" {
			return data, false, nil
		}
	}
	models = append(models, astraModelMetadata)
	encoded, err := json.Marshal(models)
	if err != nil {
		return nil, false, err
	}
	catalog["models"] = encoded
	encoded, err = json.Marshal(catalog)
	return encoded, err == nil, err
}
