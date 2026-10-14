package harness

// Harness admission records (D-079). D-071 admitted an exact native version
// paired with an exact built-in Mod profile, "from the code's table": a Go
// switch of version strings. That rule held (exact versions, never a range
// or a capability probe), but the table made every new release a hand edit
// of literals scattered through claude.go, codex.go and diagnostic.go, and a
// version string named nothing about the bytes that ran: the updater keeps
// three Claude versions on disk and re-points the launcher symlink, so the
// file a record was measured on could be gone, or a different file could
// answer the same --version.
//
// Now each admitted artifact is a committed `buddy.admission.v1` record
// under admissions/<harness>/<version>-<sha8>/admission.json, compiled in
// with //go:embed. (They live beside this file rather than in docs/: embed
// cannot name a parent directory.) Authority is still reviewed, committed
// and gated by the hermetic tier; the adapter never reads it from a runtime,
// worktree or home-directory file, because a launch cannot tell a measured
// record from a typed one and a commit plus the tier can.
//
// A record binds the exact bytes (executable sha256 and size; for Codex the
// WHOLE package, because bin/codex execs bin/codex-code-mode-host and
// codex-path/rg later, and an executable-only hash admits a swapped sidecar),
// the exact --version text, OS/arch, the code-owned adapter contract it was
// qualified under, Claude's Mod profile and built-in inventory, and the
// capability tiers its evidence earned. Each granted tier must name every
// suite the code requires for it, with an independent checker's passing
// verdict; a tier with a missing suite is refused at parse, not at launch.
//
// What a record can NOT do, by construction: add a capability the code does
// not implement (the runtime capability is the intersection of the record's
// tiers and the code's support), revive Codex reconcile (withdrawn in code;
// a record naming it is rejected), admit a built-in plugin the code does not
// know the meaning of, change a parser, or make cross-version resume legal.
// Every launch still runs the live checks (get_settings, reload_plugins,
// hooks/list); a record never replaces them.
//
// Refused alternatives: a runtime registry outside the repo (Codex's
// proposal; the adapter could not tell a measured record from a typed one),
// invalidation on any adapter/buddy hash change (an embedded record cannot
// bind the binary it is embedded in without a cycle, so the adapter contract
// id is bumped instead and the hashes are recorded as evidence), and a
// (dev, inode, size, mtime) cache of the hash (it misses exactly the
// preserved-metadata rewrite executable.go already concedes).
//
// This guards against accidental substitution and stale evidence. It does
// not authenticate a publisher: the machine user can forge a record and
// rebuild (invariant 6).

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

const AdmissionSchema = "buddy.admission.v1"

// The adapter contracts this code implements. Bump one whenever its argv,
// native wire subset, settings grammar or gate contract changes: every
// record qualified under the old id then refuses (contract_mismatch) until a
// requalification writes a new one. The buddy and adapter hashes a suite ran
// under are recorded as evidence, not checked: the adapter cannot hash the
// binary its own records are embedded in.
const (
	claudeAdapterContract = "claude-stream-json.1"
	codexAdapterContract  = "codex-app-server-v2.1"
)

// Capability tiers a record may grant. Omitted means denied. There is no
// reconcile tier: Codex reconcile is withdrawn in code and a record cannot
// revive it.
const (
	TierStart      = "start"
	TierResume     = "resume"
	TierCompaction = "compaction_observation"
)

// claudeKnownBuiltins are the built-in plugin identities whose meaning this
// code has reviewed: agents-md is a context Mod, plugin-authoring a skill
// without Mod code. A record may list a subset; a native inventory must then
// equal the record's list exactly. A new built-in needs reviewed code here
// first, because its name says nothing about what it can override.
var claudeKnownBuiltins = []string{"cc-plugin-agents-md", "cc-plugin-plugin-authoring"}

type suiteRequirement struct{ Suite, Runner, Checker string }

// admissionSuites is the code's minimum evidence per tier (consensus
// "Harness version tracking"). A record granting a tier must carry each of
// these with its independent checker's passing verdict. Deferred suites
// (endpoints, peers, history, mixed fleet, jobs) are not required here.
var admissionSuites = map[string]map[string][]suiteRequirement{
	"claude": {
		TierStart: {{"claude", "qualify-claude.py", "check-claude-gate.py"},
			{"claude-mods", "qualify-claude-mods.py", "check-claude-mods.py"}},
		TierResume: {{"claude-resume", "qualify-claude-resume.py", "check-claude-resume.py"},
			{"claude-interrupted", "qualify-claude-interrupted.py", "check-claude-interrupted.py"}},
		TierCompaction: {{"claude-compaction", "qualify-claude-compaction.py", "check-claude-compaction.py"},
			{"claude-interrupted-compaction", "qualify-claude-interrupted-compaction.py", "check-claude-interrupted-compaction.py"}},
	},
	"codex": {
		TierStart: {{"codex-shell-lifecycle-edge", "qualify-codex.py", "check-codex-gate.py"},
			{"standalone-adapters", "qualify-adapters.py", "check-adapters.py"}},
		TierResume: {{"recovery", "qualify-recovery.py", "check-recovery.py"},
			{"interrupted", "qualify-interrupted.py", "check-interrupted.py"},
			{"uncertain", "qualify-uncertain.py", "check-uncertain.py"}},
		TierCompaction: {{"compaction", "qualify-compaction.py", "check-compaction.py"},
			{"interrupted-compaction", "qualify-interrupted-compaction.py", "check-interrupted-compaction.py"}},
	},
}

// AdmissionRecord is one admitted native artifact. Every field without
// omitempty is required; omitempty fields are required or forbidden by
// harness and entry type (validate). Unknown and duplicate keys refuse.
type AdmissionRecord struct {
	Schema          string            `json:"schema"`
	Harness         string            `json:"harness"`
	Version         string            `json:"version"`
	VersionOutput   string            `json:"version_output"`
	OS              string            `json:"os"`
	Arch            string            `json:"arch"`
	AdapterContract string            `json:"adapter_contract"`
	Tiers           []string          `json:"tiers"`
	Executable      AdmissionFile     `json:"executable"`
	Package         []PackageEntry    `json:"package,omitempty"`
	Claude          *ClaudeAdmission  `json:"claude,omitempty"`
	Gate            AdmissionGate     `json:"gate"`
	Suites          []AdmissionSuite  `json:"suites"`
	Evidence        AdmissionEvidence `json:"evidence"`
}

// AdmissionFile is the executable: its path inside a retained directory
// (also its path inside a Codex package), its bytes' sha256 and size.
type AdmissionFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// PackageEntry is one entry of a Codex release tree, relative to its root.
// file: mode, sha256, size. dir: mode. symlink: target, inside the package.
type PackageEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Mode   string `json:"mode,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Size   *int64 `json:"size,omitempty"`
	Target string `json:"target,omitempty"`
}

type ClaudeAdmission struct {
	ModProfile string   `json:"mod_profile"`
	Builtins   []string `json:"builtins"`
}

// AdmissionGate names the gate contract the evidence ran under. It must
// equal the code's own (gateContract); a changed gate grammar changes the
// constant and refuses old records. The gate's native hash is per launch (it
// hashes a receipt-bound command), so no record can pin its value.
type AdmissionGate struct {
	Command string `json:"command"`
	Hash    string `json:"hash"`
}

// AdmissionSuite is one suite's evidence: the runner and checker by name and
// git blob digest, the receipt digest, the runner's and the checker's counts
// and the checker's verdict, the buddy/adapter digests and source commit it
// ran from, and the committed summary that records it (repo-relative).
type AdmissionSuite struct {
	Tier                string `json:"tier"`
	Suite               string `json:"suite"`
	Runner              string `json:"runner"`
	RunnerSHA256        string `json:"runner_sha256"`
	ReceiptSHA256       string `json:"receipt_sha256"`
	ChecksPassed        int    `json:"checks_passed"`
	ChecksTotal         int    `json:"checks_total"`
	Checker             string `json:"checker"`
	CheckerSHA256       string `json:"checker_sha256"`
	CheckerLogSHA256    string `json:"checker_log_sha256"`
	CheckerPassed       bool   `json:"checker_passed"`
	CheckerChecksPassed int    `json:"checker_checks_passed"`
	CheckerChecksTotal  int    `json:"checker_checks_total"`
	BuddySHA256         string `json:"buddy_sha256"`
	AdapterSHA256       string `json:"adapter_sha256"`
	SourceHead          string `json:"source_head"`
	Summary             string `json:"summary"`
}

// AdmissionEvidence is provenance, never permission: a date is not leave to
// extrapolate to later releases.
type AdmissionEvidence struct {
	QualifiedOn string `json:"qualified_on"`
	Source      string `json:"source"`
	Note        string `json:"note"`
}

// AdmissionError is a refusal with a stable code, so a missing record, a
// swapped binary and an unearned tier read differently in a receipt.
type AdmissionError struct {
	Code   string
	Detail string
}

func (e *AdmissionError) Error() string { return "admission refused (" + e.Code + "): " + e.Detail }

func refuse(code, format string, args ...any) *AdmissionError {
	return &AdmissionError{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// AdmissionCode returns the refusal code of an admission error, or "".
func AdmissionCode(err error) string {
	var a *AdmissionError
	if errors.As(err, &a) {
		return a.Code
	}
	return ""
}

func adapterContract(harness string) string {
	switch harness {
	case "claude":
		return claudeAdapterContract
	case "codex":
		return codexAdapterContract
	}
	return ""
}

// gateContract is the gate each harness's adapter installs: the receipt-
// bound hook command (cmd/buddy-adapter builds it) and how its native
// identity is checked.
func gateContract(harness string) AdmissionGate {
	command := "'<adapter>' hook --receipt '<receipt>' --buddy '<buddy>'"
	switch harness {
	case "claude":
		return AdmissionGate{Command: command, Hash: "none: get_settings must show exactly this command for Edit|Write|NotebookEdit"}
	case "codex":
		return AdmissionGate{Command: command, Hash: "per launch: hooks/list currentHash equals the launch gate_hash, trusted"}
	}
	return AdmissionGate{}
}

func versionOutput(harness, version string) string {
	switch harness {
	case "claude":
		return version + " (Claude Code)"
	case "codex":
		return "codex-cli " + version
	}
	return ""
}

func (r *AdmissionRecord) grants(tier string) bool { return r != nil && slices.Contains(r.Tiers, tier) }

//go:embed admissions/*/*/admission.json
var embeddedAdmissionFS embed.FS

var embedded struct {
	once    sync.Once
	records []AdmissionRecord
	err     error
}

func embeddedAdmissions() ([]AdmissionRecord, error) {
	embedded.once.Do(func() { embedded.records, embedded.err = loadAdmissionFS(embeddedAdmissionFS, "admissions") })
	return embedded.records, embedded.err
}

// admissionRecords is the one source a launch learns admission from. The
// arguments exist for the hermetic fixture registry (admission_fixture_test.go),
// which synthesizes records for the fakes the tests write; the embedded source
// ignores them. A build tagged buddy_admission_fixture (admission_fixture.go)
// may add records from a test-named file; no installed build carries the tag.
var admissionRecords = func(harness, executable string) ([]AdmissionRecord, error) { return embeddedAdmissions() }

// loadAdmissionFS parses every record under root. One malformed record makes
// the whole set unreadable: a committed record that does not parse is a
// broken build, and failing closed on all of them is how that is noticed.
func loadAdmissionFS(fsys fs.FS, root string) ([]AdmissionRecord, error) {
	paths, err := fs.Glob(fsys, root+"/*/*/admission.json")
	if err != nil {
		return nil, err
	}
	var records []AdmissionRecord
	seen := map[string]string{}
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, refuse("admission_unreadable", "%s: %v", p, err)
		}
		r, err := ParseAdmission(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		want := path.Join(root, r.Harness, r.Version+"-"+r.Executable.SHA256[:8], "admission.json")
		if p != want {
			return nil, refuse("record_invalid", "%s: a record must live at %s", p, want)
		}
		key := r.Harness + " " + r.Executable.SHA256
		if other, ok := seen[key]; ok {
			return nil, refuse("record_invalid", "%s and %s admit the same %s bytes", other, p, r.Harness)
		}
		seen[key] = p
		records = append(records, r)
	}
	return records, nil
}

// ParseAdmission parses one record strictly: unknown, duplicate and missing
// keys, malformed hashes, an unearned tier and a reconcile grant each refuse
// with their own code.
func ParseAdmission(data []byte) (AdmissionRecord, error) {
	var r AdmissionRecord
	if len(data) > 1<<20 {
		return r, refuse("record_invalid", "record exceeds 1 MiB")
	}
	if err := checkKeys(data, reflect.TypeOf(r), "record"); err != nil {
		return r, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, refuse("record_invalid", "%v", err)
	}
	// More() reports false in front of a stray `}` or `]`, so it cannot
	// say the document ended (Codex review): require the next token to be EOF.
	if _, err := dec.Token(); err != io.EOF {
		return r, refuse("record_invalid", "trailing data after the record")
	}
	return r, r.validate()
}

// checkKeys walks raw against t: every key without omitempty must be
// present, no key may repeat (encoding/json keeps the last one silently, so
// a repeated "sha256" could smuggle a second value past a reviewer), and no
// key may be unknown (also refused by the decoder; checked here first so the
// diagnostic is specific).
func checkKeys(raw []byte, t reflect.Type, at string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		fields, err := objectFields(raw, at)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			known[name] = true
			value, ok := fields[name]
			if !ok {
				if opts != "omitempty" {
					return refuse("record_missing_field", "%s.%s is required", at, name)
				}
				continue
			}
			if err := checkKeys(value, f.Type, at+"."+name); err != nil {
				return err
			}
		}
		for name := range fields {
			if !known[name] {
				return refuse("record_unknown_field", "%s.%s is not a record field", at, name)
			}
		}
	case reflect.Slice:
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return refuse("record_invalid", "%s must be an array", at)
		}
		for i, item := range items {
			if err := checkKeys(item, t.Elem(), fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func objectFields(raw []byte, at string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, refuse("record_invalid", "%s must be an object", at)
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, refuse("record_invalid", "%s: %v", at, err)
		}
		key, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, refuse("record_invalid", "%s.%s: %v", at, key, err)
		}
		if _, dup := fields[key]; dup {
			return nil, refuse("record_duplicate_field", "%s.%s appears twice", at, key)
		}
		fields[key] = value
	}
	return fields, nil
}

func validHash(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func hashes(at string, values map[string]string, n int) error {
	for name, value := range values {
		if !validHash(value, n) {
			return refuse("record_malformed_hash", "%s.%s must be %d lowercase hex digits", at, name, n)
		}
	}
	return nil
}

// cleanRelative accepts a clean, slash-separated relative path that stays
// inside its root.
func cleanRelative(p string) bool {
	return p != "" && p != "." && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../") && !strings.Contains(p, "\\")
}

func plainToken(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if c <= ' ' || c > '~' {
			return false
		}
	}
	return true
}

func (r *AdmissionRecord) validate() error {
	if r.Schema != AdmissionSchema {
		return refuse("record_invalid", "schema must be %s", AdmissionSchema)
	}
	if adapterContract(r.Harness) == "" {
		return refuse("record_invalid", "unknown harness %q", r.Harness)
	}
	if !plainToken(r.Version) || r.VersionOutput != versionOutput(r.Harness, r.Version) {
		return refuse("record_invalid", "version_output must be the exact native text for version %q", r.Version)
	}
	if !plainToken(r.OS) || !plainToken(r.Arch) || !plainToken(r.AdapterContract) {
		return refuse("record_invalid", "os, arch and adapter_contract must be plain tokens")
	}
	if err := hashes("executable", map[string]string{"sha256": r.Executable.SHA256}, 64); err != nil {
		return err
	}
	if !cleanRelative(r.Executable.Path) || r.Executable.Size <= 0 {
		return refuse("record_invalid", "executable needs a clean relative path and a positive size")
	}
	if err := r.validateTiers(); err != nil {
		return err
	}
	switch r.Harness {
	case "claude":
		if r.Package != nil || r.Claude == nil {
			return refuse("record_invalid", "a Claude record names its Mod profile and no package (a single Mach-O)")
		}
		if !plainToken(r.Claude.ModProfile) || len(r.Claude.Builtins) == 0 {
			return refuse("record_invalid", "claude needs a mod_profile and its built-in inventory")
		}
		seen := map[string]bool{}
		for _, b := range r.Claude.Builtins {
			if !slices.Contains(claudeKnownBuiltins, b) || seen[b] {
				return refuse("record_invalid", "built-in %q is unknown to this code or repeated; a new built-in needs reviewed code first", b)
			}
			seen[b] = true
		}
	case "codex":
		if r.Claude != nil || len(r.Package) == 0 {
			return refuse("record_invalid", "a Codex record carries its whole package manifest and no Claude profile")
		}
		if err := r.validatePackage(); err != nil {
			return err
		}
	}
	if r.Gate != gateContract(r.Harness) {
		return refuse("record_invalid", "gate must state this code's gate contract")
	}
	if _, err := time.Parse("2006-01-02", r.Evidence.QualifiedOn); err != nil || r.Evidence.Source == "" || r.Evidence.Note == "" {
		return refuse("record_invalid", "evidence needs qualified_on (YYYY-MM-DD), a source and a note")
	}
	return nil
}

// validateTiers refuses reconcile, an unknown or repeated tier, a grant
// without start, and a grant whose required suites are not all present with
// a passing independent verdict.
func (r *AdmissionRecord) validateTiers() error {
	required := admissionSuites[r.Harness]
	seen := map[string]bool{}
	for _, tier := range r.Tiers {
		if tier == "reconcile" {
			return refuse("record_reconcile_withdrawn", "reconcile is withdrawn in code; no record can grant it")
		}
		if _, ok := required[tier]; !ok || seen[tier] {
			return refuse("record_invalid", "tier %q is unknown or repeated", tier)
		}
		seen[tier] = true
	}
	if !seen[TierStart] {
		return refuse("record_invalid", "every record grants start; resume and compaction_observation build on it")
	}
	bySuite := map[string]AdmissionSuite{}
	for i, s := range r.Suites {
		at := fmt.Sprintf("suites[%d]", i)
		if err := hashes(at, map[string]string{"runner_sha256": s.RunnerSHA256, "receipt_sha256": s.ReceiptSHA256, "checker_sha256": s.CheckerSHA256,
			"checker_log_sha256": s.CheckerLogSHA256, "buddy_sha256": s.BuddySHA256, "adapter_sha256": s.AdapterSHA256}, 64); err != nil {
			return err
		}
		if err := hashes(at, map[string]string{"source_head": s.SourceHead}, 40); err != nil {
			return err
		}
		var want *suiteRequirement
		for _, req := range required[s.Tier] {
			if req.Suite == s.Suite {
				want = &req
			}
		}
		if want == nil || s.Runner != want.Runner || s.Checker != want.Checker {
			return refuse("record_invalid", "%s: suite %q is not this code's %q suite, or names another runner or checker", at, s.Suite, s.Tier)
		}
		if _, dup := bySuite[s.Suite]; dup {
			return refuse("record_invalid", "%s: suite %q repeated", at, s.Suite)
		}
		if !cleanRelative(s.Summary) {
			return refuse("record_invalid", "%s: summary must be a repo-relative path", at)
		}
		bySuite[s.Suite] = s
	}
	for _, tier := range r.Tiers {
		for _, req := range required[tier] {
			s, ok := bySuite[req.Suite]
			if !ok || !s.CheckerPassed || s.ChecksTotal <= 0 || s.ChecksPassed != s.ChecksTotal ||
				s.CheckerChecksTotal <= 0 || s.CheckerChecksPassed != s.CheckerChecksTotal {
				return refuse("record_tier_unearned", "tier %s needs suite %s with a passing %s verdict", tier, req.Suite, req.Checker)
			}
		}
	}
	return nil
}

func (r *AdmissionRecord) validatePackage() error {
	seen := map[string]bool{}
	exe := false
	for i, e := range r.Package {
		at := fmt.Sprintf("package[%d]", i)
		if !cleanRelative(e.Path) || seen[e.Path] {
			return refuse("record_invalid", "%s: path %q must be clean, relative and unique", at, e.Path)
		}
		seen[e.Path] = true
		switch e.Type {
		case "file":
			if err := hashes(at, map[string]string{"sha256": e.SHA256}, 64); err != nil {
				return err
			}
			if !validMode(e.Mode) || e.Size == nil || *e.Size < 0 || e.Target != "" {
				return refuse("record_invalid", "%s: a file has mode, sha256 and size only", at)
			}
			if e.Path == r.Executable.Path {
				exe = e.SHA256 == r.Executable.SHA256 && *e.Size == r.Executable.Size
			}
		case "dir":
			if !validMode(e.Mode) || e.SHA256 != "" || e.Size != nil || e.Target != "" {
				return refuse("record_invalid", "%s: a dir has mode only", at)
			}
		case "symlink":
			// A link is followed by whatever the package runs, so its target
			// must resolve inside the retained tree, never to the updater's.
			if e.Target == "" || e.Mode != "" || e.SHA256 != "" || e.Size != nil || strings.HasPrefix(e.Target, "/") ||
				!cleanRelative(path.Join(path.Dir(e.Path), e.Target)) {
				return refuse("record_invalid", "%s: a symlink has a relative target inside the package only", at)
			}
		default:
			return refuse("record_invalid", "%s: type %q is not file, dir or symlink", at, e.Type)
		}
	}
	if !exe {
		return refuse("record_invalid", "package must list the executable %s with the executable's own hash and size", r.Executable.Path)
	}
	entries := map[string]PackageEntry{}
	for _, e := range r.Package {
		entries[e.Path] = e
	}
	for _, e := range r.Package {
		if e.Type == "symlink" {
			if err := resolvePackageLink(entries, e.Path); err != nil {
				return refuse("record_invalid", "%v", err)
			}
		}
	}
	return nil
}

// resolvePackageLink follows a link the way the filesystem does, component
// by component through the manifest, and requires every step and the end to
// be a declared entry inside the package. A lexical check of each target
// alone is not enough (Codex review): with a/b -> ../d, the target
// b/../../external of a/out cleans to "external" but resolves through d to
// the package root's parent. verifyPackage requires the tree to be exactly
// the manifest, so resolving against the manifest is resolving the tree.
func resolvePackageLink(entries map[string]PackageEntry, link string) error {
	var done []string
	todo := strings.Split(link, "/")
	for hops := 0; len(todo) > 0; {
		c := todo[0]
		todo = todo[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			if len(done) == 0 {
				return fmt.Errorf("link %s resolves outside the package", link)
			}
			done = done[:len(done)-1]
			continue
		}
		at := strings.Join(append(done[:len(done):len(done)], c), "/")
		e, ok := entries[at]
		if !ok {
			return fmt.Errorf("link %s resolves through %s, which the package does not declare", link, at)
		}
		if e.Type == "symlink" {
			if hops++; hops > 40 || strings.HasPrefix(e.Target, "/") {
				return fmt.Errorf("link %s loops or leaves the package at %s", link, at)
			}
			todo = append(strings.Split(e.Target, "/"), todo...)
			continue
		}
		if e.Type != "dir" && len(todo) > 0 {
			return fmt.Errorf("link %s resolves through the non-directory %s", link, at)
		}
		done = append(done, c)
	}
	if len(done) == 0 {
		return fmt.Errorf("link %s resolves to the package root", link)
	}
	return nil
}

func validMode(m string) bool {
	if len(m) != 4 || m[0] != '0' {
		return false
	}
	for _, c := range m[1:] {
		if c < '0' || c > '7' {
			return false
		}
	}
	return true
}

// admitExecutable decides, before the file is executed at all (not even for
// --version), whether this exact file may run as op under the declared
// launch. Order: a record must exist for the declared harness and version
// (else nothing is hashed); the file's bytes must be a record's; that record
// must be qualified under this code's contract, on this platform, for op;
// a Codex package must equal its manifest; Claude's declared Mod profile
// must be the record's. The caller then requires the exact --version text
// and runs the live checks.
func admitExecutable(harness, executable string, observed os.FileInfo, launch Launch, op string) (*AdmissionRecord, error) {
	records, err := admissionRecords(harness, executable)
	if err != nil {
		if AdmissionCode(err) == "" {
			err = refuse("admission_unreadable", "%v", err)
		}
		return nil, err
	}
	var candidates []AdmissionRecord
	for _, r := range records {
		if r.Harness == harness && r.Version == launch.HarnessVersion {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		return nil, refuse("admission_missing", "no admission record for %s %q; qualify it and commit its record first", harness, launch.HarnessVersion)
	}
	// A bound qualification candidate (D-080) names the file the record
	// binds; the launched executable may be its bound observer. Without one
	// the executable is the artifact.
	executable, observed, err = candidateArtifact(harness, executable, observed, launch)
	if err != nil {
		return nil, err
	}
	sum, size, err := hashFile(executable)
	if err != nil {
		return nil, refuse("artifact_unreadable", "%v", err)
	}
	if admissionHashed != nil {
		admissionHashed()
	}
	// The bytes just hashed must still be the file at that path before
	// anything runs it (Codex review): an updater's rename between the hash
	// and the --version probe would otherwise run unadmitted bytes. This sees
	// a replacement (a new inode); an in-place rewrite of the same inode in
	// the remaining window is the race executable.go states.
	if err := sameExecutable(executable, observed); err != nil {
		return nil, refuse("artifact_mismatch", "%v", err)
	}
	var rec *AdmissionRecord
	for i := range candidates {
		if candidates[i].Executable.SHA256 == sum && candidates[i].Executable.Size == size {
			rec = &candidates[i]
		}
	}
	if rec == nil {
		return nil, refuse("artifact_mismatch", "%s %s bytes (sha256 %s) are not the admitted artifact", harness, launch.HarnessVersion, sum)
	}
	if rec.AdapterContract != adapterContract(harness) {
		return nil, refuse("contract_mismatch", "record was qualified under %q; this adapter implements %q", rec.AdapterContract, adapterContract(harness))
	}
	if rec.OS != runtime.GOOS || rec.Arch != runtime.GOARCH {
		return nil, refuse("platform_mismatch", "record is for %s/%s", rec.OS, rec.Arch)
	}
	if !rec.grants(op) {
		return nil, refuse("tier_not_granted", "the %s %s record does not grant %s", harness, rec.Version, op)
	}
	switch harness {
	case "codex":
		root, ok := strings.CutSuffix(executable, string(filepath.Separator)+filepath.FromSlash(rec.Executable.Path))
		if !ok || root == "" {
			return nil, refuse("package_mismatch", "executable is not at %s inside a package root", rec.Executable.Path)
		}
		if err := verifyPackage(root, rec.Package); err != nil {
			return nil, refuse("package_mismatch", "%v", err)
		}
	case "claude":
		if launch.Hooks.Mods != rec.Claude.ModProfile {
			return nil, refuse("profile_mismatch", "unsupported Claude Mod profile: require %s", rec.Claude.ModProfile)
		}
	}
	return rec, nil
}

// admissionHashed is a test seam (nil in every build): it runs between the
// hash and the rest of admission, where a replacement must still be caught.
var admissionHashed func()

// admissionVersionGrants is the tier check for decisions made after launch
// (or before resume, from a receipt), keyed by the captured version: it holds
// only when a record exists for that version and EVERY record of it grants
// the tier, so a second artifact of the same version cannot widen the first.
// The launch itself binds the exact record (admitExecutable).
func admissionVersionGrants(harness, version, tier string) bool {
	records, err := admissionRecords(harness, "")
	if err != nil {
		return false
	}
	found := false
	for _, r := range records {
		if r.Harness == harness && r.Version == version {
			if !r.grants(tier) {
				return false
			}
			found = true
		}
	}
	return found
}

// admissionVersionKnown reports whether any record admits this version; it
// is the diagnostic's recognition, never a launch decision.
func admissionVersionKnown(harness, version string) bool {
	return admissionVersionGrants(harness, version, TierStart)
}

// admissionPrecheck is the controller's view of admission before any side
// effect (the `.start` intent, a ledger refresh, a launch authorization). Only
// the native backends implement it; a test double has no records to consult.
// It is keyed by the declared or captured version and hashes nothing; the
// backend's launch binds the exact artifact.
type admissionPrecheck interface {
	precheck(launch Launch, op string) error
}

func precheckVersion(harness string, launch Launch, op string) error {
	if !admissionVersionGrants(harness, launch.HarnessVersion, TierStart) {
		return refuse("admission_missing", "no admission record for %s %q", harness, launch.HarnessVersion)
	}
	if !admissionVersionGrants(harness, launch.HarnessVersion, op) {
		return refuse("tier_not_granted", "no admission record grants %s to %s %q", op, harness, launch.HarnessVersion)
	}
	return nil
}

func (c *claudeBackend) precheck(launch Launch, op string) error {
	return precheckVersion("claude", launch, op)
}
func (c *codexBackend) precheck(launch Launch, op string) error {
	return precheckVersion("codex", launch, op)
}

// admissionOutputKnown is the same recognition for a captured --version text.
func admissionOutputKnown(harness, output string) bool {
	records, err := admissionRecords(harness, "")
	if err != nil {
		return false
	}
	for _, r := range records {
		if r.Harness == harness && r.VersionOutput == output {
			return true
		}
	}
	return false
}

func hashFile(name string) (string, int64, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// buildPackageManifest lists every entry under root without following a
// link. A socket, device or other special file refuses: a release tree has
// none, and one appearing is a reason to stop, not to skip.
func buildPackageManifest(root string) ([]PackageEntry, error) {
	var out []PackageEntry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			if !d.IsDir() {
				return errors.New("package root is not a directory")
			}
			// The root has no manifest entry of its own, so its special
			// bits must refuse here or nowhere (second review, P3).
			info, err := os.Lstat(p)
			if err != nil {
				return err
			}
			if info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
				return errors.New("package root carries a setuid, setgid or sticky bit")
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		e := PackageEntry{Path: filepath.ToSlash(rel)}
		// The manifest mode holds permission bits only, so a setuid, setgid
		// or sticky bit would hash as its plain mode (Codex review). A release
		// tree has none; one appearing refuses.
		if info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
			return fmt.Errorf("%s carries a setuid, setgid or sticky bit", e.Path)
		}
		mode := fmt.Sprintf("%04o", info.Mode().Perm())
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			e.Type, e.Target = "symlink", filepath.ToSlash(target)
		case info.IsDir():
			e.Type, e.Mode = "dir", mode
		case info.Mode().IsRegular():
			sum, size, err := hashFile(p)
			if err != nil {
				return err
			}
			e.Type, e.Mode, e.SHA256, e.Size = "file", mode, sum, &size
		default:
			return fmt.Errorf("%s is not a file, directory or symlink", e.Path)
		}
		out = append(out, e)
		return nil
	})
	return out, err
}

// verifyPackage requires root to hold exactly the manifest: an undeclared
// addition refuses as surely as a changed or missing entry, because a new
// file in a release tree is a file the package may load.
func verifyPackage(root string, want []PackageEntry) error {
	have, err := buildPackageManifest(root)
	if err != nil {
		return err
	}
	expected := map[string]PackageEntry{}
	for _, e := range want {
		expected[e.Path] = e
	}
	for _, e := range have {
		w, ok := expected[e.Path]
		if !ok {
			return fmt.Errorf("undeclared package entry %s", e.Path)
		}
		if w.Type != e.Type || w.Mode != e.Mode || w.SHA256 != e.SHA256 || w.Target != e.Target || (w.Size == nil) != (e.Size == nil) || w.Size != nil && *w.Size != *e.Size {
			return fmt.Errorf("package entry %s differs from its record", e.Path)
		}
		delete(expected, e.Path)
	}
	for p := range expected {
		return fmt.Errorf("package entry %s is missing", p)
	}
	return nil
}

// RetainedExecutable is where a worker's pinned copy of the one admitted
// artifact for (harness, version) lives: ~/.buddy/harness/<harness>/
// <version>-<sha8>/<executable path>. A copy outside the updater's storage
// survives the updater deleting old versions and re-pointing its launcher.
// This only names the path; admission still hashes whatever is there, and
// the adapter never copies or fetches a binary itself (retention is a
// separate, operator-run step).
func RetainedExecutable(harness, version string) (string, error) {
	records, err := admissionRecords(harness, "")
	if err != nil {
		return "", err
	}
	var match []AdmissionRecord
	for _, r := range records {
		if r.Harness == harness && r.Version == version {
			match = append(match, r)
		}
	}
	switch len(match) {
	case 0:
		return "", refuse("admission_missing", "no admission record for %s %q", harness, version)
	case 1:
	default:
		return "", refuse("record_invalid", "%d records admit %s %q; name the executable explicitly", len(match), harness, version)
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", errors.New("retained executable needs an absolute home directory")
	}
	r := match[0]
	return filepath.Join(home, ".buddy", "harness", harness, r.Version+"-"+r.Executable.SHA256[:8], filepath.FromSlash(r.Executable.Path)), nil
}
