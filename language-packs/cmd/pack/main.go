package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"astramap-standalone/languageprotocol"
)

type artifactFlags []string

func (f *artifactFlags) String() string { return strings.Join(*f, ",") }
func (f *artifactFlags) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type payloadFlags []string

func (f *payloadFlags) String() string { return strings.Join(*f, ",") }
func (f *payloadFlags) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type catalog struct {
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

type trustedKeys struct {
	Schema int               `json:"schema"`
	Keys   map[string]string `json:"keys"`
}

func main() {
	var artifacts artifactFlags
	var payloads payloadFlags
	var packages payloadFlags
	manifestPath := flag.String("manifest", "", "Source language manifest")
	outputPath := flag.String("output", "", "Output .amaplang path")
	privateKeyPath := flag.String("private-key", "", "Base64 Ed25519 private key file")
	trustedKeysOutput := flag.String("trusted-keys-output", "", "Write the matching public trusted-keys.json")
	catalogOutput := flag.String("catalog-output", "", "Write a signed package catalog instead of a package")
	catalogBaseURL := flag.String("catalog-base-url", "", "Base URL used by signed catalog package entries")
	catalogKeyID := flag.String("catalog-key-id", "", "Signing key ID for catalog mode")
	unsigned := flag.Bool("unsigned", false, "Create an explicitly unsigned local-development package")
	flag.Var(&artifacts, "artifact", "Worker artifact in os/arch=path form; repeat for each platform")
	flag.Var(&payloads, "file", "Signed payload in archive/path=source form; repeat as needed")
	flag.Var(&packages, "package", "Catalog package path; repeat in catalog mode")
	flag.Parse()
	if *catalogOutput != "" {
		if *privateKeyPath == "" || *catalogBaseURL == "" || *catalogKeyID == "" || len(packages) == 0 {
			flag.Usage()
			os.Exit(2)
		}
		if err := writeCatalog(*catalogOutput, *catalogBaseURL, *catalogKeyID, *privateKeyPath, *trustedKeysOutput, packages); err != nil {
			fatal(err)
		}
		return
	}
	if *manifestPath == "" || *outputPath == "" || len(artifacts) == 0 || (*privateKeyPath == "" && !*unsigned) {
		flag.Usage()
		os.Exit(2)
	}
	manifest, files, err := prepareManifest(*manifestPath, artifacts, payloads)
	if err != nil {
		fatal(err)
	}
	if !*unsigned {
		var publicKey ed25519.PublicKey
		manifest, publicKey, err = signManifest(manifest, *privateKeyPath)
		if err != nil {
			fatal(err)
		}
		if *trustedKeysOutput != "" {
			if err := writeTrustedKeys(*trustedKeysOutput, manifest.KeyID, publicKey); err != nil {
				fatal(err)
			}
		}
	}
	if err := writePackage(*outputPath, manifest, files); err != nil {
		fatal(err)
	}
}

func prepareManifest(path string, values []string, payloads []string) (languageprotocol.Manifest, map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return languageprotocol.Manifest{}, nil, err
	}
	var manifest languageprotocol.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, nil, err
	}
	manifest.Artifacts = nil
	manifest.Files = make(map[string]string)
	manifest.Signature = ""
	files := make(map[string]string)
	for _, value := range values {
		platform, source, ok := strings.Cut(value, "=")
		if !ok {
			return manifest, nil, fmt.Errorf("invalid artifact: %s", value)
		}
		goos, goarch, ok := strings.Cut(platform, "/")
		if !ok || goos == "" || goarch == "" {
			return manifest, nil, fmt.Errorf("invalid artifact platform: %s", platform)
		}
		name := manifest.ID
		if goos == "windows" {
			name += ".exe"
		}
		target := filepath.ToSlash(filepath.Join("bin", goos+"-"+goarch, name))
		hash, err := fileSHA256(source)
		if err != nil {
			return manifest, nil, err
		}
		manifest.Artifacts = append(manifest.Artifacts, languageprotocol.Artifact{OS: goos, Arch: goarch, Path: target, SHA256: hash})
		manifest.Files[target] = hash
		files[target] = source
	}
	for _, value := range payloads {
		target, source, ok := strings.Cut(value, "=")
		target = strings.ReplaceAll(strings.TrimSpace(target), `\`, "/")
		target = filepath.ToSlash(filepath.Clean(target))
		if !ok || target == "." || strings.HasPrefix(target, "../") || strings.HasPrefix(target, "/") || strings.Contains(target, ":") {
			return manifest, nil, fmt.Errorf("invalid package payload: %s", value)
		}
		if _, exists := files[target]; exists || target == "language.json" {
			return manifest, nil, fmt.Errorf("duplicate package payload: %s", target)
		}
		hash, err := fileSHA256(source)
		if err != nil {
			return manifest, nil, err
		}
		manifest.Files[target] = hash
		files[target] = source
	}
	sort.Slice(manifest.Artifacts, func(i, j int) bool {
		if manifest.Artifacts[i].OS == manifest.Artifacts[j].OS {
			return manifest.Artifacts[i].Arch < manifest.Artifacts[j].Arch
		}
		return manifest.Artifacts[i].OS < manifest.Artifacts[j].OS
	})
	return manifest, files, nil
}

func signManifest(manifest languageprotocol.Manifest, path string) (languageprotocol.Manifest, ed25519.PublicKey, error) {
	privateKey, err := loadPrivateKey(path)
	if err != nil {
		return manifest, nil, err
	}
	manifest.Signature = ""
	payload, err := json.Marshal(manifest)
	if err != nil {
		return manifest, nil, err
	}
	manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return manifest, append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...), nil
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid Ed25519 private key")
	}
	return ed25519.PrivateKey(decoded), nil
}

func writeCatalog(output, baseURL, keyID, privateKeyPath, trustedKeysOutput string, packages []string) error {
	privateKey, err := loadPrivateKey(privateKeyPath)
	if err != nil {
		return err
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	document := catalog{Schema: 1, Packages: make(map[string]catalogItem), KeyID: keyID}
	for _, packagePath := range packages {
		manifest, err := readPackageManifest(packagePath, keyID, publicKey)
		if err != nil {
			return err
		}
		if _, exists := document.Packages[manifest.ID]; exists {
			return fmt.Errorf("duplicate catalog language package: %s", manifest.ID)
		}
		hash, err := fileSHA256(packagePath)
		if err != nil {
			return err
		}
		document.Packages[manifest.ID] = catalogItem{
			Version: manifest.Version,
			URL:     strings.TrimRight(baseURL, "/") + "/" + filepath.Base(packagePath),
			SHA256:  hash,
		}
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return err
	}
	document.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	if err := writeJSONAtomic(output, document); err != nil {
		return err
	}
	if trustedKeysOutput != "" {
		return writeTrustedKeys(trustedKeysOutput, keyID, publicKey)
	}
	return nil
}

func readPackageManifest(packagePath, keyID string, publicKey ed25519.PublicKey) (languageprotocol.Manifest, error) {
	reader, err := zip.OpenReader(packagePath)
	if err != nil {
		return languageprotocol.Manifest{}, err
	}
	defer reader.Close()
	for _, file := range reader.File {
		if filepath.ToSlash(file.Name) != "language.json" {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			return languageprotocol.Manifest{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, 4<<20))
		_ = stream.Close()
		if readErr != nil {
			return languageprotocol.Manifest{}, readErr
		}
		var manifest languageprotocol.Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return manifest, err
		}
		if manifest.ID == "" || manifest.Version == "" || manifest.Signature == "" || manifest.KeyID != keyID {
			return manifest, fmt.Errorf("catalog package must be signed and have identity: %s", packagePath)
		}
		if !verifyManifestSignature(manifest, publicKey) {
			return manifest, fmt.Errorf("catalog package signature does not match catalog key: %s", packagePath)
		}
		return manifest, nil
	}
	return languageprotocol.Manifest{}, fmt.Errorf("package has no language.json: %s", packagePath)
}

func verifyManifestSignature(manifest languageprotocol.Manifest, publicKey ed25519.PublicKey) bool {
	signature, err := base64.StdEncoding.DecodeString(manifest.Signature)
	if err != nil {
		return false
	}
	manifest.Signature = ""
	payload, err := json.Marshal(manifest)
	return err == nil && ed25519.Verify(publicKey, payload, signature)
}

func writeTrustedKeys(output, keyID string, publicKey ed25519.PublicKey) error {
	if keyID == "" || len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("trusted key identity is incomplete")
	}
	return writeJSONAtomic(output, trustedKeys{
		Schema: 1, Keys: map[string]string{keyID: base64.StdEncoding.EncodeToString(publicKey)},
	})
}

func writeJSONAtomic(output string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(output), ".publish-*.pending")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, output); err != nil {
		return err
	}
	committed = true
	return nil
}

func writePackage(path string, manifest languageprotocol.Manifest, files map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".amaplang-*.pending")
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
	archive := zip.NewWriter(temp)
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeZipEntry(archive, "language.json", manifestData, 0644); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data, err := os.ReadFile(files[name])
		if err != nil {
			return err
		}
		if err := writeZipEntry(archive, name, data, 0755); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func writeZipEntry(archive *zip.Writer, name string, data []byte, mode os.FileMode) error {
	header := &zip.FileHeader{Name: filepath.ToSlash(name), Method: zip.Deflate}
	header.SetMode(mode)
	header.Modified = time.Unix(0, 0).UTC()
	writer, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
