package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tailscale/hujson"
)

type providerConfigSnapshot struct {
	path    string
	target  string
	payload []byte
	mode    os.FileMode
	exists  bool
}

// Resolve existing parent links even when the final file has not been created.
// This also lets the replacement check detect a redirected parent directory.
func providerConfigTarget(path string) (string, error) {
	target, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Abs(target)
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		return "", err // Existing broken links must never be replaced as new files.
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	target, err = providerConfigTarget(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(target, filepath.Base(path)), nil
}

// Caller holds providerFileMu across the whole read/modify/replace operation.
func readProviderConfigLocked(path string) (providerConfigSnapshot, error) {
	absolute, err := filepath.Abs(path)
	snapshot := providerConfigSnapshot{path: absolute, mode: 0o600}
	if err != nil || strings.TrimSpace(path) == "" {
		return snapshot, errors.New("provider configuration path is unavailable")
	}
	if err := recoverInstructionBackupLocked(absolute); err != nil {
		return snapshot, err
	}
	snapshot.target, err = providerConfigTarget(absolute)
	if err != nil {
		return snapshot, err
	}
	file, err := os.Open(snapshot.target)
	if os.IsNotExist(err) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return snapshot, err
	}
	if !stat.Mode().IsRegular() {
		return snapshot, errors.New("provider configuration is not a regular file")
	}
	snapshot.payload, err = io.ReadAll(io.LimitReader(file, providerConfigurationMaxBytes+1))
	if err != nil {
		return snapshot, err
	}
	if len(snapshot.payload) > providerConfigurationMaxBytes {
		return snapshot, errors.New("provider configuration exceeds the 4 MiB editing limit")
	}
	if !utf8.Valid(snapshot.payload) {
		return snapshot, errors.New("provider configuration must be valid UTF-8")
	}
	snapshot.exists, snapshot.mode = true, stat.Mode().Perm()
	target, err := providerConfigTarget(absolute)
	if err != nil || target != snapshot.target {
		return snapshot, errors.New("provider configuration path changed while reading; reload before saving")
	}
	return snapshot, nil
}

func (snapshot providerConfigSnapshot) checkLocked() error {
	latest, err := readProviderConfigLocked(snapshot.path)
	if err != nil {
		return err
	}
	if latest.target != snapshot.target || latest.exists != snapshot.exists || latest.mode != snapshot.mode || !bytes.Equal(latest.payload, snapshot.payload) {
		return errors.New("provider configuration changed while saving; reload before saving again")
	}
	return nil
}

// Keep numbers as JSON literals; decoding via float64 would round native IDs
// and other integers above 2^53 when an unrelated setting is saved.
func parseProviderJSON(payload []byte) (hujson.Value, map[string]any, error) {
	if len(payload) > providerConfigurationMaxBytes || !utf8.Valid(payload) {
		return hujson.Value{}, nil, errors.New("provider JSON must be valid UTF-8 and at most 4 MiB")
	}
	document, err := hujson.Parse(payload)
	if err != nil {
		return document, nil, fmt.Errorf("invalid provider JSON: %w", err)
	}
	if _, ok := document.Value.(*hujson.Object); !ok {
		return document, nil, errors.New("provider JSON must contain an object")
	}
	for value := range document.All() {
		if object, ok := value.Value.(*hujson.Object); ok {
			seen := make(map[string]bool, len(object.Members))
			for _, member := range object.Members {
				name := member.Name.Value.(hujson.Literal).String()
				if seen[name] {
					return document, nil, errors.New("provider JSON contains duplicate keys; resolve them before saving")
				}
				seen[name] = true
			}
		}
	}
	standard := document.Clone()
	standard.Standardize()
	decoder := json.NewDecoder(bytes.NewReader(standard.Pack()))
	decoder.UseNumber()
	var config map[string]any
	if err := decoder.Decode(&config); err != nil {
		return document, nil, err
	}
	return document, config, nil
}

func readProviderJSONConfig(path string) (map[string]any, error) {
	providerFileMu.Lock()
	defer providerFileMu.Unlock()
	snapshot, err := readProviderConfigLocked(path)
	if err != nil {
		return nil, err
	}
	if !snapshot.exists {
		return map[string]any{}, nil
	}
	_, config, err := parseProviderJSON(snapshot.payload)
	return config, err
}

// Apply semantic changes as JSON patches so comments, property order and
// untouched values retain their source representation. No-op saves do no I/O.
func updateProviderJSONConfig(path string, change func(map[string]any) error) error {
	providerFileMu.Lock()
	defer providerFileMu.Unlock()
	snapshot, err := readProviderConfigLocked(path)
	if err != nil {
		return fmt.Errorf("cannot read provider configuration %s: %w", path, err)
	}
	payload := snapshot.payload
	if !snapshot.exists {
		payload = []byte("{}\n")
	}
	document, before, err := parseProviderJSON(payload)
	if err != nil {
		return fmt.Errorf("cannot edit provider configuration %s: %w", path, err)
	}
	_, config, err := parseProviderJSON(payload)
	if err != nil {
		return err
	}
	if err := change(config); err != nil {
		return err
	}
	// Normalize newly assigned Go numbers/maps/arrays to the same JSON types.
	desired, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, after, err := parseProviderJSON(desired)
	if err != nil {
		return err
	}
	patches := providerJSONChanges("", before, after)
	if len(patches) == 0 {
		return nil
	}
	if !snapshot.exists {
		payload, err = json.MarshalIndent(after, "", "  ")
		payload = append(payload, '\n')
	} else {
		patch, marshalErr := json.Marshal(patches)
		if marshalErr != nil {
			return marshalErr
		}
		err = document.Patch(patch)
		payload = document.Pack()
	}
	if err != nil {
		return err
	}
	_, written, err := parseProviderJSON(payload)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(written, after) {
		return errors.New("provider configuration patch did not match the requested changes")
	}
	return writeProviderFileAtomicLocked(snapshot.target, payload, snapshot.mode, snapshot.checkLocked)
}

// TOML is edited as source text to retain comments and formatting, but the
// read, transform and replacement still happen under the same file lock and
// external-edit check as JSON configuration.
func updateProviderTextConfig(path string, change func(string) (string, error)) error {
	providerFileMu.Lock()
	defer providerFileMu.Unlock()
	snapshot, err := readProviderConfigLocked(path)
	if err != nil {
		return err
	}
	next, err := change(string(snapshot.payload))
	if err != nil {
		return err
	}
	if next == string(snapshot.payload) {
		return nil
	}
	return writeProviderFileAtomicLocked(snapshot.target, []byte(next), snapshot.mode, snapshot.checkLocked)
}

func transformProviderTextConfig(path string, change func(string) (string, error)) (providerConfigSnapshot, []byte, bool, error) {
	providerFileMu.Lock()
	defer providerFileMu.Unlock()
	snapshot, err := readProviderConfigLocked(path)
	if err != nil {
		return snapshot, nil, false, err
	}
	next, err := change(string(snapshot.payload))
	if err != nil {
		return snapshot, nil, false, err
	}
	if next == string(snapshot.payload) {
		return snapshot, append([]byte(nil), snapshot.payload...), snapshot.exists, nil
	}
	if err := writeProviderFileAtomicLocked(snapshot.target, []byte(next), snapshot.mode, snapshot.checkLocked); err != nil {
		return snapshot, nil, false, err
	}
	return snapshot, []byte(next), snapshot.exists, nil
}

func restoreProviderFileIfUnchanged(path string, expected, original []byte, existed bool) error {
	providerFileMu.Lock()
	defer providerFileMu.Unlock()
	snapshot, err := readProviderConfigLocked(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(snapshot.payload, expected) {
		return errors.New("provider configuration changed after the route update; metadata was not rolled back over the external edit")
	}
	if existed {
		return writeProviderFileAtomicLocked(snapshot.target, original, snapshot.mode, snapshot.checkLocked)
	}
	if err := os.Remove(snapshot.target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func providerJSONChanges(path string, before, after map[string]any) []map[string]any {
	keys := make([]string, 0, len(before)+len(after))
	for key := range before {
		keys = append(keys, key)
	}
	for key := range after {
		if _, exists := before[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var patches []map[string]any
	for _, key := range keys {
		pointer := path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
		oldValue, oldExists := before[key]
		newValue, newExists := after[key]
		if !newExists {
			patches = append(patches, map[string]any{"op": "remove", "path": pointer})
		} else if !oldExists {
			patches = append(patches, map[string]any{"op": "add", "path": pointer, "value": newValue})
		} else if !reflect.DeepEqual(oldValue, newValue) {
			oldObject, oldOK := oldValue.(map[string]any)
			newObject, newOK := newValue.(map[string]any)
			if oldOK && newOK {
				patches = append(patches, providerJSONChanges(pointer, oldObject, newObject)...)
			} else {
				patches = append(patches, map[string]any{"op": "replace", "path": pointer, "value": newValue})
			}
		}
	}
	return patches
}

// Missing sections can be created. Existing non-object sections need an
// explicit repair; silently replacing them would discard native settings.
func providerJSONObject(config map[string]any, key string) (map[string]any, error) {
	value, exists := config[key]
	if !exists {
		object := map[string]any{}
		config[key] = object
		return object, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("provider configuration %q must be an object", key)
	}
	return object, nil
}
