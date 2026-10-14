package harness

// The qualification candidate route (D-080).
//
// D-079 made every launch hash its executable and require a committed record
// for those bytes. That closed the hand-run version loop, and it also closed
// the only way to produce a record: a new version's suites must run BEFORE
// its record exists, and the suites themselves launch a metadata observer (a
// short wrapper that forwards stdio unchanged and logs frame shapes) rather
// than the native file, so even a recorded build's suites were refused
// (measured 2026-10-13: qualify-claude.py's managed-policy case on the
// retained 2.1.291 copy came back artifact_mismatch, because the bytes hashed
// were the observer's).
//
// The route is one explicit pair of adapter flags, --qualify-candidate <path>
// --qualify-sha256 <hex>, and it is deliberately narrow:
//
//   - It is bound to the candidate's sha256 TYPED on the command line. The
//     file is hashed when the flags are read and again at every launch, so a
//     candidate replaced after the operator named it refuses, as a swapped
//     production binary does.
//   - In that process the embedded records are REPLACED, not supplemented, by
//     one synthesized record for exactly those bytes: a candidate adapter
//     cannot launch any other build, by record or otherwise.
//   - Every operation (start and resume) must be inside a qualification
//     fixture: a deterministic provider on a loopback IP. Claude's
//     ANTHROPIC_BASE_URL, and the model provider in the config.toml Codex
//     reads (CODEX_HOME is the hook config's directory), must name
//     127.0.0.1 or ::1. A real model never runs on an unadmitted build, so
//     the route cannot do a normal worker's job even when typed by mistake.
//   - It is visible: every control receipt carries qualification_candidate,
//     and the captured lifecycle receipt records the candidate. A resume of a
//     candidate receipt without the same candidate refuses, and a candidate
//     resume of a production receipt refuses (candidate_mismatch), so neither
//     mode inherits the other's worker.
//   - A launched observer (an --executable other than the candidate) is hashed
//     when the flags are read and must be unchanged at launch. It is evidence
//     the runner records; the adapter does not claim to know what it runs.
//
// Refused alternatives: a build tag (buddy_admission_fixture already exists
// for hermetic tests). Codex's migration plan asked for final adapter bytes,
// and a tagged build is not the binary install.sh ships. A candidate record
// file on disk is refused for the reason D-079 gives against a runtime
// registry. A worktree or path marker as the fixture test is refused because
// it says nothing about whether a real provider is reachable. The loopback
// test is cooperative like everything else here (invariant 6). The Claude
// built-in inventory is the code's whole known list: a candidate that ships a
// different set refuses at reload_plugins, and that refusal is the finding,
// because a new built-in needs reviewed code first (D-079).

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// QualificationCandidate is the operator's typed binding, as the captured
// lifecycle receipt records it.
type QualificationCandidate struct {
	Harness          string `json:"harness"`
	Version          string `json:"version"`
	Artifact         string `json:"artifact"`
	SHA256           string `json:"sha256"`
	Executable       string `json:"executable,omitempty"`
	ExecutableSHA256 string `json:"executable_sha256,omitempty"`
}

var candidateState struct {
	mu     sync.Mutex
	active *QualificationCandidate
}

// candidateGetenv is the environment the fixture test reads: the adapter's
// own, which the native child inherits.
var candidateGetenv = os.Getenv

// EnableQualificationCandidate binds this process to one candidate artifact.
// It runs before any child, hashes the artifact (and a distinct observer),
// and replaces the record set with one synthesized record. It may run once.
func EnableQualificationCandidate(c QualificationCandidate) (*QualificationCandidate, error) {
	candidateState.mu.Lock()
	defer candidateState.mu.Unlock()
	if candidateState.active != nil {
		return nil, refuse("candidate_mismatch", "a qualification candidate is already bound in this process")
	}
	if c.Harness != "claude" && c.Harness != "codex" {
		return nil, refuse("candidate_mismatch", "unsupported harness %q", c.Harness)
	}
	if !plainToken(c.Version) || !validHash(c.SHA256, 64) {
		return nil, refuse("candidate_mismatch", "a candidate needs a plain declared version and a lowercase 64-hex sha256")
	}
	if !filepath.IsAbs(c.Artifact) || filepath.Clean(c.Artifact) != c.Artifact {
		return nil, refuse("candidate_mismatch", "candidate artifact must be a clean absolute path")
	}
	// The artifact itself, never a link to one: the typed digest names a
	// file, and a link could be re-pointed between flags and launch.
	info, err := os.Lstat(c.Artifact)
	if err != nil || !info.Mode().IsRegular() {
		return nil, refuse("candidate_mismatch", "candidate artifact must be a regular file, not a link")
	}
	execPath := "claude"
	var pkg []PackageEntry
	if c.Harness == "codex" {
		execPath = "bin/codex"
		root, ok := strings.CutSuffix(c.Artifact, string(filepath.Separator)+filepath.FromSlash(execPath))
		if !ok || root == "" {
			return nil, refuse("candidate_mismatch", "a Codex candidate is bin/codex inside its release tree")
		}
		if pkg, err = buildPackageManifest(root); err != nil {
			return nil, refuse("package_mismatch", "%v", err)
		}
	}
	sum, size, err := hashFile(c.Artifact)
	if err != nil {
		return nil, refuse("artifact_unreadable", "%v", err)
	}
	if sum != c.SHA256 {
		return nil, refuse("candidate_mismatch", "candidate %s has sha256 %s, not the typed %s", c.Artifact, sum, c.SHA256)
	}
	bound := c
	if c.Executable != "" && c.Executable != c.Artifact {
		if bound.ExecutableSHA256, _, err = hashFile(c.Executable); err != nil {
			return nil, refuse("artifact_unreadable", "observer: %v", err)
		}
	} else {
		bound.Executable, bound.ExecutableSHA256 = "", ""
	}
	rec := AdmissionRecord{Schema: AdmissionSchema, Harness: c.Harness, Version: c.Version, VersionOutput: versionOutput(c.Harness, c.Version),
		OS: runtime.GOOS, Arch: runtime.GOARCH, AdapterContract: adapterContract(c.Harness),
		Tiers:      []string{TierStart, TierResume, TierCompaction},
		Executable: AdmissionFile{Path: execPath, SHA256: sum, Size: size}, Package: pkg, Gate: gateContract(c.Harness),
		Evidence: AdmissionEvidence{Source: "qualification candidate typed on the adapter command line (D-080)", Note: "grants nothing outside a loopback qualification fixture"}}
	if c.Harness == "claude" {
		rec.Claude = &ClaudeAdmission{ModProfile: "builtin-only-" + c.Version, Builtins: append([]string(nil), claudeKnownBuiltins...)}
	}
	admissionRecords = func(string, string) ([]AdmissionRecord, error) { return []AdmissionRecord{rec}, nil }
	candidateState.active = &bound
	out := bound
	return &out, nil
}

// ActiveQualificationCandidate is the process's binding, or nil.
func ActiveQualificationCandidate() *QualificationCandidate {
	candidateState.mu.Lock()
	defer candidateState.mu.Unlock()
	if candidateState.active == nil {
		return nil
	}
	c := *candidateState.active
	return &c
}

// sameCandidate is the resume rule: a captured receipt and this process must
// agree on candidate or production, and on the exact candidate bytes.
func sameCandidate(saved, active *QualificationCandidate) error {
	switch {
	case saved == nil && active == nil:
		return nil
	case saved == nil:
		return refuse("candidate_mismatch", "this receipt was captured by a production launch; a qualification candidate cannot resume it")
	case active == nil:
		return refuse("candidate_mismatch", "this receipt was captured by qualification candidate %s; only that candidate can resume it", saved.SHA256)
	case saved.Harness != active.Harness || saved.Version != active.Version || saved.SHA256 != active.SHA256:
		return refuse("candidate_mismatch", "this receipt was captured by candidate %s, not %s", saved.SHA256, active.SHA256)
	}
	return nil
}

// candidateArtifact is admitExecutable's view: which file the record binds
// and must be hashed, and the fixture and observer checks for this launch.
// With no candidate, the launched executable is the artifact.
func candidateArtifact(harness, executable string, observed os.FileInfo, launch Launch) (string, os.FileInfo, error) {
	c := ActiveQualificationCandidate()
	if c == nil {
		return executable, observed, nil
	}
	// Another harness or declared version never reaches here: the record set
	// holds only the candidate, so admitExecutable found no record for it
	// (admission_missing, measured by the other-version test).
	if err := qualificationFixture(harness, launch); err != nil {
		return "", nil, err
	}
	// An executable bound as no observer has an empty digest, which no
	// file hashes to, so it refuses here like a changed observer.
	if executable != c.Artifact {
		sum, _, err := hashFile(executable)
		if err != nil || sum != c.ExecutableSHA256 {
			return "", nil, refuse("candidate_mismatch", "launched executable %s is not the candidate or its observer as bound", executable)
		}
	}
	info, err := observeExecutable(c.Artifact)
	if err != nil {
		return "", nil, refuse("artifact_unreadable", "%v", err)
	}
	return c.Artifact, info, nil
}

// qualificationFixture refuses a candidate launch whose provider is not a
// loopback IP: the definition of "inside a qualification fixture".
func qualificationFixture(harness string, launch Launch) error {
	switch harness {
	case "claude":
		if !loopbackURL(candidateGetenv("ANTHROPIC_BASE_URL")) {
			return refuse("candidate_outside_fixture", "a candidate launch needs ANTHROPIC_BASE_URL on a loopback IP (a deterministic fixture provider)")
		}
	case "codex":
		base, err := codexFixtureProvider(filepath.Join(filepath.Dir(launch.HookConfig), "config.toml"))
		if err != nil {
			return refuse("candidate_outside_fixture", "%v", err)
		}
		if !loopbackURL(base) {
			return refuse("candidate_outside_fixture", "a candidate launch needs the Codex model provider's base_url on a loopback IP")
		}
	}
	return nil
}

func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

// codexFixtureProvider reads the base_url of the provider config.toml selects.
// It understands only the flat shape the fixtures write (tables, `key =
// "string"` and other scalar lines) and refuses what could select a provider
// another way: a profile, an array table, a multi-line value, a provider
// missing its table. Refusing an unfamiliar config is the safe direction:
// the cost is a fixture rewritten, never a candidate reaching a real model.
func codexFixtureProvider(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", fmt.Errorf("a Codex candidate launch needs a fixture config.toml: %v", err)
	}
	defer f.Close()
	table, provider := "", ""
	bases := map[string]string{}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, `"""`) || strings.Contains(line, "'''") {
			return "", errors.New("Codex fixture config has a multi-line string")
		}
		// An array table is how every fixture writes its hooks
		// ([[hooks.PreToolUse]], [[hooks.PreToolUse.hooks]]) into this same
		// file, so refusing them whole refused every Codex adapter suite
		// (first native release run, 0.162.1, 2026-10-13). Its keys belong to
		// an array element, never to the top level or to a provider table, so
		// it is read as a table no case below matches. One under
		// model_providers or profiles still refuses: those are the names a
		// provider could be selected or defined through.
		if strings.HasPrefix(line, "[[") {
			if !strings.HasSuffix(line, "]]") {
				return "", errors.New("Codex fixture config has a malformed array table header")
			}
			name := strings.TrimSpace(line[2 : len(line)-2])
			// A quoted or spaced segment could spell model_providers
			// another way; the fixtures use bare dotted names only.
			if name == "" || strings.ContainsAny(name, "\"' \t") {
				return "", errors.New("Codex fixture config has an array table name that is not bare and dotted")
			}
			for _, p := range []string{"model_providers", "profiles"} {
				if name == p || strings.HasPrefix(name, p+".") {
					return "", fmt.Errorf("Codex fixture config has an array table under %s", p)
				}
			}
			table = "[[" + name + "]]"
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return "", errors.New("Codex fixture config has a malformed table header")
			}
			table = strings.TrimSpace(line[1 : len(line)-1])
			if table == "profiles" || strings.HasPrefix(table, "profiles.") {
				return "", errors.New("Codex fixture config defines profiles")
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return "", errors.New("Codex fixture config has a line that is not key = value")
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		str, quoted := strings.CutPrefix(value, `"`)
		if quoted {
			if str, quoted = strings.CutSuffix(str, `"`); !quoted || strings.ContainsAny(str, `"\`) {
				return "", fmt.Errorf("Codex fixture config value for %s is not a plain string", key)
			}
		}
		switch {
		case table == "" && (key == "profile" || strings.Contains(key, ".")):
			return "", fmt.Errorf("Codex fixture config sets %s at top level", key)
		case table == "" && key == "model_provider":
			if !quoted {
				return "", errors.New("Codex fixture model_provider is not a plain string")
			}
			provider = str
		case strings.HasPrefix(table, "model_providers.") && key == "base_url":
			if !quoted {
				return "", errors.New("Codex fixture base_url is not a plain string")
			}
			bases[strings.TrimPrefix(table, "model_providers.")] = str
		}
	}
	if err := scan.Err(); err != nil {
		return "", err
	}
	if provider == "" || bases[provider] == "" {
		return "", errors.New("Codex fixture config names no model_provider with a base_url")
	}
	return bases[provider], nil
}
