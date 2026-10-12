package harness

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const settingsGate = "/tmp/buddy-adapter hook --receipt /tmp/receipt --buddy /tmp/buddy"

// settingsWith renders one PreToolUse group around the exact gate hook. The
// group and hook members are raw JSON text so a test can write what Go's
// encoder never would: duplicate and case-variant keys.
func settingsWith(group, hook string) string {
	if hook == "" {
		hook = `"type":"command","command":"` + settingsGate + `"`
	}
	if group == "" {
		group = `"matcher":"Edit|Write|NotebookEdit"`
	}
	return `{"hooks":{"PreToolUse":[{` + group + `,"hooks":[{` + hook + `}]}]}}`
}

// F-09: native Claude reads a plain matcher as an EXACT '|'/',' list and
// anything else as a JS RegExp; the adapter read every matcher as an
// unanchored Go regexp. Admitted forms are the ones both read the same way.
func TestClaudeSettingsMatcherSemantics(t *testing.T) {
	for _, test := range []struct {
		matcher string
		admit   bool
	}{
		{`"matcher":""`, true},
		{`"matcher":"*"`, true},
		{`"matcher":".*"`, true},
		{`"matcher":"Edit|Write|NotebookEdit"`, true},
		{`"matcher":"NotebookEdit,Write,Edit"`, true},
		{`"matcher":"Edit|Write|NotebookEdit|Bash"`, true},
		{`"matcher":"^(Edit|Write|NotebookEdit)$"`, true}, // every native qualification fixture
		{`"matcher":"Edit|Write"`, false},                 // Go found Edit inside NotebookEdit
		{`"matcher":"dit|rite"`, false},                   // natively matches nothing
		{`"matcher":"edit|write|notebookedit"`, false},
		{`"matcher":"(?i)edit|write|notebookedit"`, false},
		{`"matcher":"Edit|Write|NotebookEdit.*"`, false},
		{`"matcher":"Edit | Write | NotebookEdit"`, false},
		{`"matcher":"Edit||Write|NotebookEdit"`, false},
		{`"matcher":"|Edit|Write|NotebookEdit"`, false},
		{`"matcher":"Edit|Write|NotebookEdit,"`, false},
		{`"matcher":"^Edit|Write|NotebookEdit$"`, false},
		{`"matcher":"^(Edit|Write|NotebookEdit)"`, false},
		{`"matcher":"^(Edit,Write,NotebookEdit)$"`, false},
		{`"matcher":"^(Edit|Write|Notebook.*)$"`, false},
		{`"matcher":".*Edit"`, false},
		{`"matcher":null`, false},
		{`"matcher":7`, false},
	} {
		t.Run(test.matcher, func(t *testing.T) {
			err := claudeSettingsCompatibility([]byte(settingsWith(test.matcher, "")), settingsGate)
			if (err == nil) != test.admit {
				t.Fatalf("admit=%v, got %v", test.admit, err)
			}
		})
	}
	t.Run("absent matcher", func(t *testing.T) {
		raw := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"` + settingsGate + `"}]}]}}`
		if err := claudeSettingsCompatibility([]byte(raw), settingsGate); err != nil {
			t.Fatalf("absent matcher (every tool) refused: %v", err)
		}
	})
	t.Run("coverage split across groups", func(t *testing.T) {
		hook := `"hooks":[{"type":"command","command":"` + settingsGate + `"}]`
		raw := `{"hooks":{"PreToolUse":[{"matcher":"Edit|Write",` + hook + `},{"matcher":"NotebookEdit",` + hook + `}]}}`
		if err := claudeSettingsCompatibility([]byte(raw), settingsGate); err == nil {
			t.Fatal("a matcher naming only some gated tools was admitted")
		}
	})
}

// F-21: Go folded key case and kept the last duplicate, while JSON.parse reads
// the exact key. {"command":"true","Command":"<gate>"} natively runs true.
func TestClaudeSettingsExactKeysAndBounds(t *testing.T) {
	gateHook := `"type":"command","command":"` + settingsGate + `"`
	for _, test := range []struct {
		name, raw string
		admit     bool
	}{
		{"positive control", settingsWith("", ""), true},
		{"timeout at floor", settingsWith("", gateHook+`,"timeout":10`), true},
		{"timeout at ceiling", settingsWith("", gateHook+`,"timeout":600`), true},
		{"explicit sync", settingsWith("", gateHook+`,"async":false`), true},
		{"plugins all off", `{"enabledPlugins":{"a@m":false},` + settingsWith("", "")[1:], true},
		// The four review probes.
		{"case-variant command", settingsWith("", `"type":"command","command":"true","Command":"`+settingsGate+`"`), false},
		{"case-variant async", settingsWith("", gateHook+`,"async":true,"Async":false`), false},
		{"case-variant matcher", settingsWith(`"matcher":"Bash","Matcher":".*"`, ""), false},
		{"case-variant hooks", `{"hooks":{"PreToolUse":[{"matcher":".*","hooks":[],"Hooks":[{` + gateHook + `}]}]}}`, false},
		// Exact duplicates at every level, and an escaped spelling of one.
		{"duplicate top-level hooks", `{"hooks":{},` + settingsWith("", "")[1:], false},
		{"duplicate event", `{"hooks":{"PreToolUse":[],"PreToolUse":[{"matcher":".*","hooks":[{` + gateHook + `}]}]}}`, false},
		{"duplicate matcher", settingsWith(`"matcher":"Bash","matcher":".*"`, ""), false},
		{"duplicate command", settingsWith("", `"type":"command","command":"true","command":"`+settingsGate+`"`), false},
		{"escaped duplicate command", settingsWith("", `"type":"command","command":"true","comm\u0061nd":"`+settingsGate+`"`), false},
		{"duplicate plugin", `{"enabledPlugins":{"a@m":false,"a@m":true},` + settingsWith("", "")[1:], false},
		{"case-variant top-level key", `{"Hooks":{}}`, false},
		{"case-variant type", settingsWith("", `"Type":"command","command":"`+settingsGate+`"`), false},
		{"unknown hook key", settingsWith("", gateHook+`,"statusMessage":"x"`), false},
		{"unknown group key", settingsWith(`"matcher":".*","once":true`, ""), false},
		{"missing type", settingsWith("", `"command":"`+settingsGate+`"`), false},
		{"async null", settingsWith("", gateHook+`,"async":null`), false},
		{"async string", settingsWith("", gateHook+`,"async":"false"`), false},
		{"disable null", `{"disableAllHooks":null,` + settingsWith("", "")[1:], false},
		{"timeout tiny", settingsWith("", gateHook+`,"timeout":0.001`), false},
		{"timeout zero", settingsWith("", gateHook+`,"timeout":0`), false},
		{"timeout negative", settingsWith("", gateHook+`,"timeout":-5`), false},
		{"timeout under floor", settingsWith("", gateHook+`,"timeout":9.99`), false},
		{"timeout over ceiling", settingsWith("", gateHook+`,"timeout":601`), false},
		{"timeout overflowing setTimeout", settingsWith("", gateHook+`,"timeout":1e9`), false},
		{"timeout string", settingsWith("", gateHook+`,"timeout":"10"`), false},
		{"timeout null", settingsWith("", gateHook+`,"timeout":null`), false},
		{"hooks null", `{"hooks":{"PreToolUse":[{"matcher":".*","hooks":null}]}}`, false},
		{"trailing data", settingsWith("", "") + `{}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := claudeSettingsCompatibility([]byte(test.raw), settingsGate)
			if (err == nil) != test.admit {
				t.Fatalf("admit=%v, got %v for %s", test.admit, err, test.raw)
			}
		})
	}
}

// The timeout floor must stay above everything the wrapper can spend before
// it answers deny itself; otherwise native cancellation (not a deny) wins.
func TestClaudeHookTimeoutFloorExceedsWrapperBudget(t *testing.T) {
	worst := HookGateBudget + HookEndpointBudget + HookEndpointBudget // + endpoint WaitDelay
	if float64(claudeHookTimeoutMinSeconds) <= worst.Seconds()+2 {
		t.Fatalf("timeout floor %ds leaves no margin over the wrapper's %v worst case", claudeHookTimeoutMinSeconds, worst)
	}
}

// The native get_settings reply passes through the same strict admission as
// the supplied file (F-09/F-21 "apply identically to supplied and effective").
// The fake writes the effective object's raw bytes, so duplicate keys survive.
func TestClaudeEffectiveSettingsUseStrictAdmission(t *testing.T) {
	gateHook := `"type":"command","command":"` + settingsGate + `"`
	for _, test := range []struct{ name, effective string }{
		{"case-variant command", settingsWith("", `"type":"command","command":"true","Command":"`+settingsGate+`"`)},
		{"exact-list matcher missing NotebookEdit", settingsWith(`"matcher":"Edit|Write"`, "")},
		{"timeout below floor", settingsWith("", gateHook+`,"timeout":1`)},
		{"duplicate event", `{"hooks":{"PreToolUse":[],"PreToolUse":[{"matcher":".*","hooks":[{` + gateHook + `}]}]}}`},
		{"positive control", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, launch, ctx := claudeFixture(t)
			source := "open(sys.argv[sys.argv.index('--settings')+1]).read()"
			if test.effective != "" {
				path := filepath.Join(t.TempDir(), "effective.json")
				if err := os.WriteFile(path, []byte(test.effective), 0600); err != nil {
					t.Fatal(err)
				}
				source = "open(" + pyQuote(path) + ").read()"
			}
			needle := "  if m['request']['subtype']=='get_settings':\n   body={'effective':json.load(open(sys.argv[sys.argv.index('--settings')+1]))}\n"
			raw := "  if m['request']['subtype']=='get_settings':\n   sys.stdout.write('{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":'+json.dumps(m['request_id'])+',\"response\":{\"effective\":'+" + source + ".strip()+'}}}\\n'); sys.stdout.flush(); continue\n"
			fake := strings.Replace(claudeFake, needle, raw, 1)
			if fake == claudeFake {
				t.Fatal("effective-settings fixture mutation did not apply")
			}
			if err := os.WriteFile(b.(*claudeBackend).executable, []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			_, err := b.Start(ctx, launch, testAuthorization(t))
			if test.effective == "" {
				if err != nil {
					t.Fatalf("raw effective settings positive control refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("launch admitted effective settings the native harness reads differently")
			}
		})
	}
}

func pyQuote(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
}

// Every native Claude runner writes the adapter's hook with an anchored
// matcher and a 10 s timeout, exactly at the floor. A runner whose settings
// admission refuses would surface only in an operator-authorized native run,
// so read what each runner writes, from its source, and admit it here
// (verification brief, adversarial item 9). The floor on the number of sites
// keeps a respelled runner from turning this into a check of nothing.
func TestClaudeSettingsAdmitWhatTheRunnersWrite(t *testing.T) {
	runners, err := filepath.Glob("../../scripts/qualify-*.py")
	if err != nil {
		t.Fatal(err)
	}
	site := regexp.MustCompile(`"PreToolUse":\s*\[\s*\{\s*"matcher":\s*"([^"\\]*)",\s*"hooks":\s*\[\{"type":\s*"command",\s*"command":\s*gate,\s*"timeout":\s*(\d+)\}\]`)
	found := 0
	for _, runner := range runners {
		src, err := os.ReadFile(runner)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range site.FindAllSubmatch(src, -1) {
			found++
			settings := `{"enabledPlugins":{},"hooks":{"PreToolUse":[{"matcher":"` + string(m[1]) + `","hooks":[{"type":"command","command":"` + settingsGate + `","timeout":` + string(m[2]) + `}]}]}}`
			if err := claudeSettingsCompatibility([]byte(settings), settingsGate); err != nil {
				t.Errorf("%s writes hook settings the adapter refuses: %v\n%s", filepath.Base(runner), err, settings)
			}
		}
	}
	if found < 6 {
		t.Fatalf("read %d runner hook sites, want at least 6: a runner was respelled and is no longer checked", found)
	}
}
