package languageprotocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const (
	Version      = 1
	MaxFrameSize = 64 << 20
)

type Capabilities struct {
	Definitions       bool `json:"definitions"`
	Containers        bool `json:"containers"`
	LocalCalls        bool `json:"localCalls"`
	Imports           bool `json:"imports"`
	CrossFileCalls    bool `json:"crossFileCalls"`
	OverloadResolve   bool `json:"overloadResolve"`
	Implementations   bool `json:"implementations"`
	IncrementalSyntax bool `json:"incrementalSyntax"`
}

type Detection struct {
	Extensions map[string]string `json:"extensions,omitempty"`
	Filenames  map[string]string `json:"filenames,omitempty"`
	Shebangs   []Shebang         `json:"shebangs,omitempty"`
}

type Shebang struct {
	Contains []string `json:"contains"`
	Dialect  string   `json:"dialect,omitempty"`
}

type Toolchain struct {
	Label        string   `json:"label"`
	Commands     []string `json:"commands,omitempty"`
	InstallHint  string   `json:"installHint,omitempty"`
	WhenAnyFiles []string `json:"whenAnyFiles,omitempty"`
}

type Semantic struct {
	ProviderID  string   `json:"providerId"`
	Mode        string   `json:"mode,omitempty"`
	Tool        string   `json:"tool,omitempty"`
	InstallHint string   `json:"installHint,omitempty"`
	Args        []string `json:"args,omitempty"`
	Artifact    string   `json:"artifact,omitempty"`
}

type Project struct {
	Manifests []string `json:"manifests,omitempty"`
}

type Filter struct {
	Directories []string `json:"directories,omitempty"`
	Files       []string `json:"files,omitempty"`
}

type Artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Schema             int               `json:"schema"`
	ID                 string            `json:"id"`
	Version            string            `json:"version"`
	ProtocolMin        int               `json:"protocolMin"`
	ProtocolMax        int               `json:"protocolMax"`
	DisplayName        string            `json:"displayName"`
	Aliases            []string          `json:"aliases,omitempty"`
	IDPrefix           string            `json:"idPrefix"`
	QualifiedSeparator string            `json:"qualifiedSeparator,omitempty"`
	CaseInsensitive    bool              `json:"caseInsensitive,omitempty"`
	Detection          Detection         `json:"detection"`
	Capabilities       Capabilities      `json:"capabilities"`
	Toolchain          []Toolchain       `json:"toolchain,omitempty"`
	Semantic           *Semantic         `json:"semantic,omitempty"`
	Project            Project           `json:"project,omitempty"`
	Filter             Filter            `json:"filter,omitempty"`
	Artifacts          []Artifact        `json:"artifacts"`
	Publisher          string            `json:"publisher"`
	KeyID              string            `json:"keyId"`
	Files              map[string]string `json:"files"`
	Signature          string            `json:"signature"`
}

type Request struct {
	ID        uint64        `json:"id"`
	Version   int           `json:"version"`
	Method    string        `json:"method"`
	Handshake *Handshake    `json:"handshake,omitempty"`
	Parse     *ParseRequest `json:"parse,omitempty"`
}

type Response struct {
	ID        uint64             `json:"id"`
	Version   int                `json:"version"`
	Error     string             `json:"error,omitempty"`
	Handshake *HandshakeResponse `json:"handshake,omitempty"`
	Parse     *FileFacts         `json:"parse,omitempty"`
}

type Handshake struct {
	CoreMin int `json:"coreMin"`
	CoreMax int `json:"coreMax"`
}

type HandshakeResponse struct {
	ModuleID   string       `json:"moduleId"`
	Version    string       `json:"version"`
	Protocol   int          `json:"protocol"`
	Capability Capabilities `json:"capabilities"`
}

type ParseRequest struct {
	Language     string `json:"language"`
	Dialect      string `json:"dialect,omitempty"`
	ProjectRoot  string `json:"projectRoot"`
	RelativePath string `json:"relativePath"`
	ContentHash  string `json:"contentHash"`
	Source       []byte `json:"source"`
}

type FileFacts struct {
	Language    string           `json:"language"`
	Dialect     string           `json:"dialect,omitempty"`
	Definitions []DefinitionFact `json:"definitions,omitempty"`
	Calls       []CallFact       `json:"calls,omitempty"`
	Imports     []ImportFact     `json:"imports,omitempty"`
	Diagnostics []Diagnostic     `json:"diagnostics,omitempty"`
}

type DefinitionFact struct {
	LocalID        string `json:"localId"`
	ParentLocalID  string `json:"parentLocalId,omitempty"`
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	QualifiedName  string `json:"qualifiedName"`
	StartLine      int    `json:"startLine"`
	EndLine        int    `json:"endLine"`
	Signature      string `json:"signature,omitempty"`
	Docstring      string `json:"docstring,omitempty"`
	IdentitySuffix string `json:"identitySuffix,omitempty"`
	Callable       bool   `json:"callable,omitempty"`
}

type CallFact struct {
	CallerLocalID       string `json:"callerLocalId"`
	CalleeLocalID       string `json:"calleeLocalId,omitempty"`
	CalleeName          string `json:"calleeName"`
	CalleeQualifiedName string `json:"calleeQualifiedName,omitempty"`
	Line                int    `json:"line"`
	Column              int    `json:"column"`
	Metadata            string `json:"metadata,omitempty"`
}

type ImportFact struct {
	Path  string `json:"path"`
	Alias string `json:"alias,omitempty"`
	Line  int    `json:"line,omitempty"`
}

type Diagnostic struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Line     int    `json:"line,omitempty"`
}

func ReadFrame(r io.Reader, value any) error {
	var size uint32
	if err := binary.Read(r, binary.BigEndian, &size); err != nil {
		return err
	}
	if size == 0 || size > MaxFrameSize {
		return fmt.Errorf("invalid language protocol frame size: %d", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}
	if err := json.Unmarshal(payload, value); err != nil {
		return fmt.Errorf("decode language protocol frame: %w", err)
	}
	return nil
}

func WriteFrame(w io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode language protocol frame: %w", err)
	}
	if len(payload) == 0 || len(payload) > MaxFrameSize {
		return fmt.Errorf("invalid language protocol frame size: %d", len(payload))
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(payload))); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}
