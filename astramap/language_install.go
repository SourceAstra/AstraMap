// Copyright 2026 AstraMap Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the original license at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package astramap

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"astramap-standalone/languageprotocol"
)

const maxLanguagePackageSize = 512 << 20

var (
	languagePackageIDPattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	languagePackageVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
	languagePrefixPattern         = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)
	languageSHA256Pattern         = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

type LanguageInstallOptions struct {
	ProjectRoot   string
	ProjectScope  bool
	AllowUnsigned bool
	TrustKeyPath  string
	CatalogURL    string
}

type LanguagePackageInfo struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	DisplayName string `json:"displayName"`
	Enabled     bool   `json:"enabled"`
	Scope       string `json:"scope"`
	Source      string `json:"source"`
}

type languageCatalog struct {
	Schema    int                    `json:"schema"`
	Packages  map[string]catalogItem `json:"packages"`
	KeyID     string                 `json:"keyId"`
	Signature string                 `json:"signature"`
}

type catalogItem struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

type resolvedLanguagePackage struct {
	Location string
	SHA256   string
	ID       string
	Version  string
}

type trustedLanguageKeys struct {
	Schema int               `json:"schema"`
	Keys   map[string]string `json:"keys"`
}

type languageInstallReceipt struct {
	Schema         int    `json:"schema"`
	ManifestSHA256 string `json:"manifestSha256"`
	Unsigned       bool   `json:"unsigned,omitempty"`
	KeyID          string `json:"keyId,omitempty"`
	PublicKey      string `json:"publicKey,omitempty"`
}

func InstallLanguagePackage(source string, options LanguageInstallOptions) (LanguagePackageInfo, error) {
	root, err := languageRootForOptions(options)
	if err != nil {
		return LanguagePackageInfo{}, err
	}
	unlock, err := acquireLanguageLock(root)
	if err != nil {
		return LanguagePackageInfo{}, err
	}
	defer unlock()

	resolved, err := resolveLanguagePackageSource(source, options)
	if err != nil {
		return LanguagePackageInfo{}, err
	}
	tempFile, err := fetchLanguagePackage(resolved.Location, root)
	if err != nil {
		return LanguagePackageInfo{}, err
	}
	defer os.Remove(tempFile)
	if resolved.SHA256 != "" {
		if err := verifyFileSHA256(tempFile, resolved.SHA256); err != nil {
			return LanguagePackageInfo{}, err
		}
	}
	manifest, publicKey, err := inspectLanguageArchive(tempFile, root, options)
	if err != nil {
		return LanguagePackageInfo{}, err
	}
	if resolved.ID != "" && (manifest.ID != resolved.ID || manifest.Version != resolved.Version) {
		return LanguagePackageInfo{}, fmt.Errorf("catalog package identity mismatch: expected %s@%s, got %s@%s", resolved.ID, resolved.Version, manifest.ID, manifest.Version)
	}
	finalDir := filepath.Join(root, "packages", manifest.ID, manifest.Version)
	if _, err := os.Stat(finalDir); err == nil {
		return LanguagePackageInfo{}, fmt.Errorf("language package already installed: %s@%s", manifest.ID, manifest.Version)
	} else if !os.IsNotExist(err) {
		return LanguagePackageInfo{}, err
	}
	if err := os.MkdirAll(filepath.Join(root, "pending"), 0755); err != nil {
		return LanguagePackageInfo{}, err
	}
	stageDir, err := os.MkdirTemp(filepath.Join(root, "pending"), manifest.ID+"-")
	if err != nil {
		return LanguagePackageInfo{}, fmt.Errorf("create language package staging directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stageDir)
		}
	}()
	if err := extractLanguageArchive(tempFile, stageDir, manifest); err != nil {
		return LanguagePackageInfo{}, err
	}
	if err := writeLanguageInstallReceipt(stageDir, manifest, publicKey, manifest.Signature == ""); err != nil {
		return LanguagePackageInfo{}, err
	}
	installedManifest, executable, err := loadInstalledManifest(stageDir)
	if err != nil {
		return LanguagePackageInfo{}, err
	}
	if err := validateLanguageActivation(manifest); err != nil {
		return LanguagePackageInfo{}, err
	}
	if err := probeLanguageWorker(installedManifest, executable); err != nil {
		return LanguagePackageInfo{}, fmt.Errorf("validate language worker before activation: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(finalDir), 0755); err != nil {
		return LanguagePackageInfo{}, err
	}
	if err := os.Rename(stageDir, finalDir); err != nil {
		return LanguagePackageInfo{}, fmt.Errorf("commit language package: %w", err)
	}
	committed = true
	active, err := readOrCreateActiveLanguageSet(root)
	if err != nil {
		_ = os.RemoveAll(finalDir)
		return LanguagePackageInfo{}, err
	}
	previous, hadPrevious := active.Packages[manifest.ID]
	active.Packages[manifest.ID] = manifest.Version
	if err := writeActiveLanguageSet(root, active); err != nil {
		if hadPrevious {
			active.Packages[manifest.ID] = previous
		} else {
			delete(active.Packages, manifest.ID)
		}
		_ = os.RemoveAll(finalDir)
		return LanguagePackageInfo{}, err
	}
	return LanguagePackageInfo{
		ID: manifest.ID, Version: manifest.Version, DisplayName: manifest.DisplayName,
		Enabled: true, Scope: languageScopeName(options.ProjectScope), Source: resolved.Location,
	}, nil
}

func EnableLanguagePackage(id, version string, options LanguageInstallOptions) error {
	if err := validateLanguagePackageCoordinate(id, version); err != nil {
		return err
	}
	root, err := languageRootForOptions(options)
	if err != nil {
		return err
	}
	unlock, err := acquireLanguageLock(root)
	if err != nil {
		return err
	}
	defer unlock()
	packageDir := filepath.Join(root, "packages", id, version)
	manifest, executable, err := loadInstalledManifest(packageDir)
	if err != nil {
		return err
	}
	if manifest.ID != id || manifest.Version != version {
		return fmt.Errorf("language package coordinate mismatch: %s@%s", id, version)
	}
	if err := validateLanguageActivation(manifest); err != nil {
		return err
	}
	if err := probeLanguageWorker(manifest, executable); err != nil {
		return fmt.Errorf("validate language worker before activation: %w", err)
	}
	active, err := readOrCreateActiveLanguageSet(root)
	if err != nil {
		return err
	}
	active.Packages[id] = version
	return writeActiveLanguageSet(root, active)
}

func DisableLanguagePackage(id string, options LanguageInstallOptions) error {
	if err := validateLanguagePackageCoordinate(id, ""); err != nil {
		return err
	}
	root, err := languageRootForOptions(options)
	if err != nil {
		return err
	}
	unlock, err := acquireLanguageLock(root)
	if err != nil {
		return err
	}
	defer unlock()
	active, err := readOrCreateActiveLanguageSet(root)
	if err != nil {
		return err
	}
	if _, exists := active.Packages[id]; !exists {
		return fmt.Errorf("language package is not active: %s", id)
	}
	delete(active.Packages, id)
	return writeActiveLanguageSet(root, active)
}

func RemoveLanguagePackage(id, version string, options LanguageInstallOptions) error {
	if err := validateLanguagePackageCoordinate(id, version); err != nil {
		return err
	}
	root, err := languageRootForOptions(options)
	if err != nil {
		return err
	}
	unlock, err := acquireLanguageLock(root)
	if err != nil {
		return err
	}
	defer unlock()
	packageDir := filepath.Join(root, "packages", id, version)
	manifest, _, err := loadInstalledManifest(packageDir)
	if err != nil {
		return err
	}
	if manifest.ID != id || manifest.Version != version {
		return fmt.Errorf("language package coordinate mismatch: %s@%s", id, version)
	}
	active, err := readOrCreateActiveLanguageSet(root)
	if err != nil {
		return err
	}
	wasActive := active.Packages[id] == version
	trashDir := filepath.Join(root, "trash", fmt.Sprintf("%s-%s-%d", id, version, time.Now().UnixNano()))
	if err := os.MkdirAll(filepath.Dir(trashDir), 0755); err != nil {
		return err
	}
	if err := os.Rename(packageDir, trashDir); err != nil {
		return fmt.Errorf("deactivate language package files: %w", err)
	}
	rollback := func() {
		_ = os.Rename(trashDir, packageDir)
	}
	if wasActive {
		delete(active.Packages, id)
		if err := writeActiveLanguageSet(root, active); err != nil {
			rollback()
			return err
		}
	}
	if err := os.RemoveAll(trashDir); err != nil {
		rollback()
		if wasActive {
			active.Packages[id] = version
			_ = writeActiveLanguageSet(root, active)
		}
		return fmt.Errorf("remove deactivated language package: %w", err)
	}
	return nil
}

func ListLanguagePackages(options LanguageInstallOptions) ([]LanguagePackageInfo, error) {
	root, err := languageRootForOptions(options)
	if err != nil {
		return nil, err
	}
	active, err := readOrCreateActiveLanguageSet(root)
	if err != nil {
		return nil, err
	}
	base := filepath.Join(root, "packages")
	ids, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []LanguagePackageInfo
	for _, idEntry := range ids {
		if !idEntry.IsDir() {
			continue
		}
		versions, readErr := os.ReadDir(filepath.Join(base, idEntry.Name()))
		if readErr != nil {
			return nil, readErr
		}
		for _, versionEntry := range versions {
			if !versionEntry.IsDir() {
				continue
			}
			manifest, _, loadErr := loadInstalledManifest(filepath.Join(base, idEntry.Name(), versionEntry.Name()))
			if loadErr != nil {
				continue
			}
			result = append(result, LanguagePackageInfo{
				ID: manifest.ID, Version: manifest.Version, DisplayName: manifest.DisplayName,
				Enabled: active.Packages[manifest.ID] == manifest.Version,
				Scope:   languageScopeName(options.ProjectScope), Source: filepath.Join(base, idEntry.Name(), versionEntry.Name()),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID == result[j].ID {
			return result[i].Version < result[j].Version
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func DiagnoseLanguagePackage(id string, options LanguageInstallOptions) error {
	if err := validateLanguagePackageCoordinate(id, ""); err != nil {
		return err
	}
	root, err := languageRootForOptions(options)
	if err != nil {
		return err
	}
	active, err := readActiveLanguageSet(root)
	if err != nil {
		return err
	}
	version := active.Packages[id]
	if version == "" {
		return fmt.Errorf("language package is not active: %s", id)
	}
	manifest, executable, err := loadInstalledManifest(filepath.Join(root, "packages", id, version))
	if err != nil {
		return err
	}
	return pooledProcessLanguageModule(manifest, executable).Probe()
}

func validateLanguagePackageCoordinate(id, version string) error {
	if !languagePackageIDPattern.MatchString(id) {
		return fmt.Errorf("invalid language package id: %s", id)
	}
	if version != "" && !languagePackageVersionPattern.MatchString(version) {
		return fmt.Errorf("invalid language package version: %s", version)
	}
	return nil
}

func probeLanguageWorker(manifest languageprotocol.Manifest, executable string) error {
	module := newProcessLanguageModule(manifest, executable)
	if err := module.Probe(); err != nil {
		_ = module.Close()
		return err
	}
	return module.Close()
}

func writeLanguageInstallReceipt(packageDir string, manifest languageprotocol.Manifest, publicKey ed25519.PublicKey, unsigned bool) error {
	receipt := languageInstallReceipt{
		Schema: 1, ManifestSHA256: languageManifestSHA256(manifest), Unsigned: unsigned, KeyID: manifest.KeyID,
	}
	if len(publicKey) > 0 {
		receipt.PublicKey = base64.StdEncoding.EncodeToString(publicKey)
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(packageDir, languageInstallReceiptName), data, 0644)
}

func languageManifestSHA256(manifest languageprotocol.Manifest) string {
	data, _ := json.Marshal(manifest)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func inspectLanguageArchive(path, root string, options LanguageInstallOptions) (languageprotocol.Manifest, ed25519.PublicKey, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return languageprotocol.Manifest{}, nil, fmt.Errorf("open language package: %w", err)
	}
	defer reader.Close()
	manifestFile := archiveFile(reader.File, languageManifestName)
	if manifestFile == nil {
		return languageprotocol.Manifest{}, nil, fmt.Errorf("language package has no %s", languageManifestName)
	}
	data, err := readZipFile(manifestFile, 4<<20)
	if err != nil {
		return languageprotocol.Manifest{}, nil, err
	}
	var manifest languageprotocol.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, nil, fmt.Errorf("decode language package manifest: %w", err)
	}
	if err := validateLanguageManifest(manifest); err != nil {
		return manifest, nil, err
	}
	publicKey, err := verifyManifestSignature(manifest, root, options)
	if err != nil {
		return manifest, nil, err
	}
	seen := make(map[string]bool, len(reader.File))
	var totalSize uint64
	for _, file := range reader.File {
		name, err := cleanArchivePath(file.Name)
		if err != nil {
			return manifest, nil, err
		}
		if seen[name] {
			return manifest, nil, fmt.Errorf("language package contains duplicate path: %s", name)
		}
		seen[name] = true
		if file.FileInfo().Mode()&os.ModeSymlink != 0 {
			return manifest, nil, fmt.Errorf("language package contains symlink: %s", name)
		}
		if file.FileInfo().IsDir() || name == languageManifestName {
			continue
		}
		totalSize += file.UncompressedSize64
		if file.UncompressedSize64 > maxLanguagePackageSize || totalSize > maxLanguagePackageSize {
			return manifest, nil, fmt.Errorf("language package expanded size exceeds %d bytes", maxLanguagePackageSize)
		}
		expected, ok := manifest.Files[name]
		if !ok {
			return manifest, nil, fmt.Errorf("language package contains unsigned file: %s", name)
		}
		actual, err := zipFileSHA256(file)
		if err != nil {
			return manifest, nil, err
		}
		if !strings.EqualFold(actual, expected) {
			return manifest, nil, fmt.Errorf("language package file checksum mismatch: %s", name)
		}
	}
	for name := range manifest.Files {
		if !seen[name] {
			return manifest, nil, fmt.Errorf("language package signed file is missing: %s", name)
		}
	}
	return manifest, publicKey, nil
}

func extractLanguageArchive(path, target string, manifest languageprotocol.Manifest) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	var totalSize int64
	for _, file := range reader.File {
		name, err := cleanArchivePath(file.Name)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, filepath.FromSlash(name))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		source, err := file.Open()
		if err != nil {
			return err
		}
		mode := os.FileMode(0644)
		for _, artifact := range manifest.Artifacts {
			if artifact.Path == name {
				mode = 0755
				break
			}
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			_ = source.Close()
			return err
		}
		written, copyErr := io.Copy(output, io.LimitReader(source, maxLanguagePackageSize-totalSize+1))
		totalSize += written
		closeErr := output.Close()
		_ = source.Close()
		if copyErr != nil {
			return copyErr
		}
		if totalSize > maxLanguagePackageSize {
			return fmt.Errorf("language package expanded size exceeds %d bytes", maxLanguagePackageSize)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func verifyManifestSignature(manifest languageprotocol.Manifest, root string, options LanguageInstallOptions) (ed25519.PublicKey, error) {
	if manifest.Signature == "" {
		if options.AllowUnsigned {
			return nil, nil
		}
		return nil, fmt.Errorf("language package is unsigned; use an explicitly trusted signed package")
	}
	keys, err := loadTrustedLanguageKeys(root, options.TrustKeyPath)
	if err != nil {
		return nil, err
	}
	publicKey := keys[manifest.KeyID]
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("language package signing key is not trusted: %s", manifest.KeyID)
	}
	signature, err := base64.StdEncoding.DecodeString(manifest.Signature)
	if err != nil {
		return nil, fmt.Errorf("decode language package signature: %w", err)
	}
	manifest.Signature = ""
	payload, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return nil, fmt.Errorf("language package signature verification failed")
	}
	return append(ed25519.PublicKey(nil), publicKey...), nil
}

func loadTrustedLanguageKeys(root, extraPath string) (map[string]ed25519.PublicKey, error) {
	result := make(map[string]ed25519.PublicKey)
	paths := []string{filepath.Join(root, "trusted-keys.json")}
	if extraPath != "" {
		paths = append(paths, extraPath)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var store trustedLanguageKeys
		if err := json.Unmarshal(data, &store); err != nil {
			return nil, fmt.Errorf("decode trusted language keys %s: %w", path, err)
		}
		if store.Schema != 1 {
			return nil, fmt.Errorf("unsupported trusted language key schema: %d", store.Schema)
		}
		for id, encoded := range store.Keys {
			key, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || len(key) != ed25519.PublicKeySize {
				return nil, fmt.Errorf("invalid trusted language key: %s", id)
			}
			result[id] = ed25519.PublicKey(key)
		}
	}
	return result, nil
}

func validateLanguageActivation(manifest languageprotocol.Manifest) error {
	spec := languageByID[normalizeLanguageID(manifest.ID)]
	if spec == nil || spec.ID != manifest.ID {
		return fmt.Errorf("syntax overlay targets unsupported language: %s", manifest.ID)
	}
	if manifest.IDPrefix != spec.IDPrefix || manifest.QualifiedSeparator != spec.QualifiedSeparator {
		return fmt.Errorf("syntax overlay identity does not match supported language: %s", manifest.ID)
	}
	return nil
}

func resolveLanguagePackageSource(source string, options LanguageInstallOptions) (resolvedLanguagePackage, error) {
	if strings.Contains(source, "://") || strings.HasSuffix(strings.ToLower(source), ".amaplang") || strings.ContainsAny(source, `/\\`) {
		return resolvedLanguagePackage{Location: source}, nil
	}
	catalogURL := options.CatalogURL
	if catalogURL == "" {
		catalogURL = os.Getenv("ASTRAMAP_LANGUAGE_CATALOG")
	}
	if catalogURL == "" {
		return resolvedLanguagePackage{}, fmt.Errorf("language catalog is not configured; provide a .amaplang path or URL")
	}
	data, err := fetchURL(catalogURL, 8<<20)
	if err != nil {
		return resolvedLanguagePackage{}, err
	}
	var catalog languageCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return resolvedLanguagePackage{}, fmt.Errorf("decode language catalog: %w", err)
	}
	if catalog.Schema != 1 {
		return resolvedLanguagePackage{}, fmt.Errorf("unsupported language catalog schema: %d", catalog.Schema)
	}
	root, err := languageRootForOptions(options)
	if err != nil {
		return resolvedLanguagePackage{}, err
	}
	keys, err := loadTrustedLanguageKeys(root, options.TrustKeyPath)
	if err != nil {
		return resolvedLanguagePackage{}, err
	}
	key := keys[catalog.KeyID]
	signature, decodeErr := base64.StdEncoding.DecodeString(catalog.Signature)
	catalog.Signature = ""
	payload, marshalErr := json.Marshal(catalog)
	if decodeErr != nil || marshalErr != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, payload, signature) {
		return resolvedLanguagePackage{}, fmt.Errorf("language catalog signature verification failed")
	}
	item, ok := catalog.Packages[source]
	if !ok || item.URL == "" || item.SHA256 == "" || item.Version == "" {
		return resolvedLanguagePackage{}, fmt.Errorf("language package not found in catalog: %s", source)
	}
	return resolvedLanguagePackage{Location: item.URL, SHA256: item.SHA256, ID: source, Version: item.Version}, nil
}

func fetchLanguagePackage(source, root string) (string, error) {
	if err := os.MkdirAll(filepath.Join(root, "downloads"), 0755); err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(filepath.Join(root, "downloads"), "package-*.amaplang")
	if err != nil {
		return "", err
	}
	path := temp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()
	var sourceReader io.ReadCloser
	if strings.Contains(source, "://") {
		client := &http.Client{Timeout: 5 * time.Minute}
		response, requestErr := client.Get(source)
		if requestErr != nil {
			_ = temp.Close()
			return "", requestErr
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			_ = response.Body.Close()
			_ = temp.Close()
			return "", fmt.Errorf("download language package: HTTP %s", response.Status)
		}
		sourceReader = response.Body
	} else {
		file, openErr := os.Open(source)
		if openErr != nil {
			_ = temp.Close()
			return "", openErr
		}
		sourceReader = file
	}
	limited := io.LimitReader(sourceReader, maxLanguagePackageSize+1)
	written, copyErr := io.Copy(temp, limited)
	closeSourceErr := sourceReader.Close()
	closeTempErr := temp.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeSourceErr != nil {
		return "", closeSourceErr
	}
	if closeTempErr != nil {
		return "", closeTempErr
	}
	if written > maxLanguagePackageSize {
		return "", fmt.Errorf("language package exceeds %d bytes", maxLanguagePackageSize)
	}
	keep = true
	return path, nil
}

func fetchURL(url string, maxSize int64) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: HTTP %s", url, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("response exceeds %d bytes", maxSize)
	}
	return data, nil
}

func languageRootForOptions(options LanguageInstallOptions) (string, error) {
	if options.ProjectScope {
		if options.ProjectRoot == "" {
			return "", fmt.Errorf("project-scoped language package requires project root")
		}
		return filepath.Join(options.ProjectRoot, ".astramap", "languages"), nil
	}
	return userLanguageRoot(), nil
}

func languageScopeName(project bool) string {
	if project {
		return "project"
	}
	return "user"
}

func readOrCreateActiveLanguageSet(root string) (activeLanguageSet, error) {
	active, err := readActiveLanguageSet(root)
	if os.IsNotExist(err) {
		return activeLanguageSet{Schema: 1, Packages: make(map[string]string)}, nil
	}
	return active, err
}

func writeActiveLanguageSet(root string, active activeLanguageSet) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	active.Schema = 1
	if active.Packages == nil {
		active.Packages = make(map[string]string)
	}
	data, err := json.MarshalIndent(active, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(root, "active-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, filepath.Join(root, "active.json")); err != nil {
		return err
	}
	committed = true
	return nil
}

func acquireLanguageLock(root string) (func(), error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(root, ".lock")
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n%d\n", os.Getpid(), time.Now().Unix())
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		info, statErr := os.Stat(path)
		if statErr != nil || time.Since(info.ModTime()) <= 10*time.Minute {
			return nil, fmt.Errorf("language package store is locked: %s", root)
		}
		if removeErr := os.Remove(path); removeErr != nil {
			return nil, fmt.Errorf("remove stale language package lock: %w", removeErr)
		}
	}
	return nil, fmt.Errorf("cannot acquire language package store lock: %s", root)
}

func cleanArchivePath(name string) (string, error) {
	name = strings.ReplaceAll(strings.TrimSpace(name), `\`, "/")
	clean := path.Clean(name)
	if name == "" || clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || strings.Contains(clean, ":") {
		return "", fmt.Errorf("unsafe language package path: %s", name)
	}
	return clean, nil
}

func archiveFile(files []*zip.File, name string) *zip.File {
	for _, file := range files {
		if filepath.ToSlash(file.Name) == name {
			return file
		}
	}
	return nil
}

func readZipFile(file *zip.File, maxSize int64) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("language package file exceeds %d bytes: %s", maxSize, file.Name)
	}
	return data, nil
}

func zipFileSHA256(file *zip.File) (string, error) {
	reader, err := file.Open()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func CanonicalLanguageManifest(manifest languageprotocol.Manifest) ([]byte, error) {
	manifest.Signature = ""
	return json.Marshal(manifest)
}

func SignLanguageManifest(manifest languageprotocol.Manifest, privateKey ed25519.PrivateKey) (languageprotocol.Manifest, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return manifest, fmt.Errorf("invalid Ed25519 private key")
	}
	payload, err := CanonicalLanguageManifest(manifest)
	if err != nil {
		return manifest, err
	}
	manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return manifest, nil
}

func VerifyLanguageManifestSignature(manifest languageprotocol.Manifest, publicKey ed25519.PublicKey) bool {
	signature, err := base64.StdEncoding.DecodeString(manifest.Signature)
	if err != nil {
		return false
	}
	payload, err := CanonicalLanguageManifest(manifest)
	return err == nil && ed25519.Verify(publicKey, payload, signature)
}
