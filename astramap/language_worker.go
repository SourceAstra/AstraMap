package astramap

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"astramap-standalone/languageprotocol"
)

const languageWorkerTimeout = 2 * time.Minute

type processLanguageModule struct {
	manifest   languageprotocol.Manifest
	executable string

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID uint64
}

func newProcessLanguageModule(manifest languageprotocol.Manifest, executable string) *processLanguageModule {
	return &processLanguageModule{manifest: manifest, executable: executable}
}

func (m *processLanguageModule) Manifest() languageprotocol.Manifest {
	return m.manifest
}

func (m *processLanguageModule) Probe() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked()
}

func (m *processLanguageModule) Parse(request languageprotocol.ParseRequest) (languageprotocol.FileFacts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startLocked(); err != nil {
		return languageprotocol.FileFacts{}, err
	}
	response, err := m.exchangeLocked(languageprotocol.Request{
		Version: languageprotocol.Version, Method: "parse", Parse: &request,
	})
	if err != nil {
		m.stopLocked()
		return languageprotocol.FileFacts{}, err
	}
	if response.Error != "" {
		return languageprotocol.FileFacts{}, fmt.Errorf("language worker %s: %s", m.manifest.ID, response.Error)
	}
	if response.Parse == nil {
		return languageprotocol.FileFacts{}, fmt.Errorf("language worker %s returned no parse facts", m.manifest.ID)
	}
	return *response.Parse, nil
}

func (m *processLanguageModule) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return nil
	}
	_, _ = m.exchangeLocked(languageprotocol.Request{Version: languageprotocol.Version, Method: "shutdown"})
	m.stopLocked()
	return nil
}

func (m *processLanguageModule) startLocked() error {
	if m.cmd != nil {
		return nil
	}
	command := exec.Command(m.executable)
	stdin, err := command.StdinPipe()
	if err != nil {
		return fmt.Errorf("open language worker stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("open language worker stdout: %w", err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("start language worker %s: %w", m.manifest.ID, err)
	}
	m.cmd, m.stdin, m.stdout = command, stdin, bufio.NewReader(stdout)
	response, err := m.exchangeLocked(languageprotocol.Request{
		Version: languageprotocol.Version, Method: "handshake",
		Handshake: &languageprotocol.Handshake{CoreMin: languageprotocol.Version, CoreMax: languageprotocol.Version},
	})
	if err != nil {
		m.stopLocked()
		return err
	}
	if response.Error != "" || response.Handshake == nil {
		m.stopLocked()
		return fmt.Errorf("language worker %s handshake failed: %s", m.manifest.ID, response.Error)
	}
	handshake := response.Handshake
	if handshake.ModuleID != m.manifest.ID || handshake.Version != m.manifest.Version ||
		handshake.Protocol != languageprotocol.Version || handshake.Capability != m.manifest.Capabilities {
		m.stopLocked()
		return fmt.Errorf("language worker identity mismatch: expected %s@%s", m.manifest.ID, m.manifest.Version)
	}
	return nil
}

func (m *processLanguageModule) exchangeLocked(request languageprotocol.Request) (languageprotocol.Response, error) {
	m.nextID++
	request.ID = m.nextID
	type result struct {
		response languageprotocol.Response
		err      error
	}
	done := make(chan result, 1)
	go func() {
		if err := languageprotocol.WriteFrame(m.stdin, request); err != nil {
			done <- result{err: err}
			return
		}
		var response languageprotocol.Response
		if err := languageprotocol.ReadFrame(m.stdout, &response); err != nil {
			done <- result{err: err}
			return
		}
		if response.ID != request.ID || response.Version != languageprotocol.Version {
			done <- result{err: fmt.Errorf("language worker protocol correlation mismatch")}
			return
		}
		done <- result{response: response}
	}()
	select {
	case value := <-done:
		return value.response, value.err
	case <-time.After(languageWorkerTimeout):
		if m.cmd != nil && m.cmd.Process != nil {
			_ = m.cmd.Process.Kill()
		}
		<-done
		return languageprotocol.Response{}, fmt.Errorf("language worker %s timed out", m.manifest.ID)
	}
}

func (m *processLanguageModule) stopLocked() {
	if m.stdin != nil {
		_ = m.stdin.Close()
	}
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		_ = m.cmd.Wait()
	}
	m.cmd, m.stdin, m.stdout = nil, nil, nil
}

func languageFactsToGraph(spec LanguageSelection, facts languageprotocol.FileFacts, relPath string, now int64) ([]*AstraMapNode, []*AstraMapEdge, error) {
	if facts.Language != "" && facts.Language != spec.ID {
		return nil, nil, fmt.Errorf("language worker returned %s facts for %s", facts.Language, spec.ID)
	}
	nodes := make([]*AstraMapNode, 0, len(facts.Definitions))
	edges := make([]*AstraMapEdge, 0, len(facts.Definitions)+len(facts.Calls)+len(facts.Imports))
	byLocalID := make(map[string]*AstraMapNode, len(facts.Definitions))
	byName := make(map[string][]*AstraMapNode, len(facts.Definitions))
	usedNodeIDs := make(map[string]bool, len(facts.Definitions))
	for _, fact := range facts.Definitions {
		if fact.LocalID == "" || fact.Name == "" || fact.Kind == "" || fact.StartLine < 1 || fact.EndLine < fact.StartLine {
			return nil, nil, fmt.Errorf("language worker %s returned invalid definition fact", spec.ID)
		}
		identity := fact.QualifiedName
		if identity == "" {
			identity = fact.Name
		}
		identity = selectionIdentity(spec, identity)
		baseID := fmt.Sprintf("%s:%s::%s%s", selectionPrefix(spec), relPath, identity, fact.IdentitySuffix)
		id := baseID
		if usedNodeIDs[id] {
			id = fmt.Sprintf("%s@%d", baseID, fact.StartLine)
			for sequence := 2; usedNodeIDs[id]; sequence++ {
				id = fmt.Sprintf("%s@%d#%d", baseID, fact.StartLine, sequence)
			}
		}
		if _, exists := byLocalID[fact.LocalID]; exists {
			return nil, nil, fmt.Errorf("language worker %s returned duplicate local id %s", spec.ID, fact.LocalID)
		}
		node := &AstraMapNode{
			ID: id, Kind: fact.Kind, Name: fact.Name, QualifiedName: fact.QualifiedName,
			FilePath: relPath, Language: spec.ID, StartLine: fact.StartLine, EndLine: fact.EndLine,
			Signature: fact.Signature, Docstring: fact.Docstring, Provenance: "language-package", UpdatedAt: now,
		}
		if node.QualifiedName == "" {
			node.QualifiedName = node.Name
		}
		nodes = append(nodes, node)
		usedNodeIDs[id] = true
		byLocalID[fact.LocalID] = node
		key := selectionIdentity(spec, fact.Name)
		byName[key] = append(byName[key], node)
	}
	for _, fact := range facts.Definitions {
		node := byLocalID[fact.LocalID]
		parentID := "file:" + relPath
		if fact.ParentLocalID != "" {
			parent := byLocalID[fact.ParentLocalID]
			if parent == nil {
				return nil, nil, fmt.Errorf("language worker %s returned unknown parent %s", spec.ID, fact.ParentLocalID)
			}
			parentID = parent.ID
		}
		edges = append(edges, &AstraMapEdge{Source: parentID, Target: node.ID, Kind: "contains", Provenance: "language-package"})
	}
	for _, fact := range facts.Calls {
		caller := byLocalID[fact.CallerLocalID]
		if caller == nil || fact.CalleeName == "" {
			continue
		}
		targetID := ""
		if target := byLocalID[fact.CalleeLocalID]; target != nil {
			targetID = target.ID
		} else if matches := byName[selectionIdentity(spec, fact.CalleeName)]; len(matches) == 1 {
			targetID = matches[0].ID
		} else {
			targetID = fmt.Sprintf("external:%s . . $ %s.", selectionPrefix(spec), fact.CalleeName)
		}
		if targetID == caller.ID {
			continue
		}
		edges = append(edges, &AstraMapEdge{
			Source: caller.ID, Target: targetID, Kind: "calls", Provenance: "language-package",
			Line: fact.Line, Col: fact.Column, Metadata: strings.TrimSpace(fact.Metadata),
		})
	}
	for _, fact := range facts.Imports {
		if strings.TrimSpace(fact.Path) == "" {
			continue
		}
		edges = append(edges, &AstraMapEdge{
			Source: "file:" + relPath, Target: "import:" + fact.Path, Kind: "imports", Provenance: "language-package", Line: fact.Line,
		})
	}
	return nodes, edges, nil
}

func selectionIdentity(selection LanguageSelection, value string) string {
	if selection.Spec != nil && selection.Spec.NormalizeIdentity != nil {
		return selection.Spec.NormalizeIdentity(value)
	}
	return value
}

func selectionPrefix(selection LanguageSelection) string {
	if selection.Spec != nil && selection.Spec.IDPrefix != "" {
		return selection.Spec.IDPrefix
	}
	return "unknown"
}
