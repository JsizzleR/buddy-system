package cli

// shellLiteral decodes a single-quoted word through mvdan/sh's expand.Literal.
// Handed a nil config, that library "prepares" its own package-global zero
// config IN PLACE (expand.go: `cfg = cmp.Or(cfg, zeroConfig)`, then writes
// cfg.Env and cfg.ifs; the library's own TODO says so), so two gate calls
// decoding quoted words at once race on it. The hermetic tier saw it on the
// D-108 squash: 14 internal/cli tests "race detected", every pair one
// shellWalker.words against another test's gate call. The call is on main
// too; D-108 widened the window (gh's words are now decoded). The fix hands
// expand.Literal a fresh config per call. This test runs the decoder from
// many goroutines at once; under -race it FAILED before the fix (watched)
// and passes after it. Without -race it still checks every decode.

import (
	"sync"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestShellLiteralConcurrentDecodesAreRaceFree(t *testing.T) {
	const goroutines, rounds = 16, 200
	var wg sync.WaitGroup
	errs := make(chan string, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				got, ok := shellLiteral([]syntax.WordPart{&syntax.SglQuoted{Value: "gh stack rebase"}}, false)
				if !ok || got != "gh stack rebase" {
					errs <- got
					return
				}
				got, ok = shellLiteral([]syntax.WordPart{&syntax.SglQuoted{Dollar: true, Value: `a\tb`}}, false)
				if !ok || got != "a\tb" {
					errs <- got
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for got := range errs {
		t.Fatalf("a concurrent decode returned %q", got)
	}
}
