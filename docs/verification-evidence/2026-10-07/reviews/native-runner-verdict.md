**Confirmed P2 — Forced adapter cleanup can be reported as PASS.** At [qualify-claude-jobs.py:104](~/Projects/buddy-system/scripts/qualify-claude-jobs.py:104), success checks the adapter’s exit code and job disappearance but ignores `client.forced`.

Failing case: EOF shutdown exceeds five seconds; [Adapter.close:401](~/Projects/buddy-system/scripts/qualify-claude.py:401) sends fallback SIGTERM; the adapter then ends the job and exits zero. All three checks pass, and line 107 discards the client without recording the forced shutdown. This masks a cleanup failure and loses its distinction from normal adapter cleanup.

Record `client.forced` separately and require it to be empty for PASS. The escaped-job cleanup already preserves the earlier RED verdict.

Source review only; no builds, tests, or native launches performed.
